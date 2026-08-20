package cbor

import (
	"encoding/binary"
	"errors"
	"strconv"
)

// errStopWalk unwinds the route walk without describing a route.
//
// Giving up has to stop the whole walk, not just the item in front of it. A
// container loop asks for its next child until one reports the target or an
// error, so a child that declines by returning neither -- and without consuming
// anything -- is asked again forever. An indefinite-length container has no
// count to end that loop, so the spin is unbounded, which turned a refusal into
// a hang.
var errStopWalk = errors.New("cbor: route walk stopped")

// Nothing tracks a container route while decoding succeeds. A path is built
// only once something has already failed, by walking the input again from the
// start until the offset that failed is reached.
//
// That ordering is the point. A decoder that maintained a route would pay for
// it on every item of every message, and a hot decode loop is exactly where
// this package must cost nothing. A failure is not the steady state, so it can
// afford one extra walk.

// maxRouteDepth bounds how deep a route is worth describing, and with it how
// deep this walk recurses and how long the string it builds can get.
//
// Both bounds matter on the error path specifically. The nesting limit is a
// stack safety net set far past any schema, so the input that trips it is
// nested thousands deep; describing the route to it would recurse thousands
// deep a second time and produce tens of kilobytes of "[0][0][0]" that says
// nothing. Past this depth the offset is the whole answer.
const maxRouteDepth = 32

// maxRouteKeyBytes bounds one rendered map key. A text key may be megabytes
// under the world profile, and an error message is not the place to repeat it.
const maxRouteKeyBytes = 40

// pathTo describes the container route to the item beginning at target, in a
// form close to diagnostic notation: [i] indexes an array, {k} selects a map
// value under key k, and {#i key} names the i-th key of a map. The root item,
// and anything nested past maxRouteDepth, has no route and yields "".
func (r *Reader) pathTo(target int) string {
	if target <= 0 || len(r.data) == 0 {
		return ""
	}
	w := pathWalker{r: Reader{data: r.data, opts: r.opts}, target: target}
	found, _ := w.item(0)
	if !found || len(w.route) == 0 {
		return ""
	}
	return "at " + w.route
}

type pathWalker struct {
	r      Reader
	target int
	route  string
	// marks records where each pushed segment began, so pop can trim.
	marks []int
}

func (w *pathWalker) push(segment string) {
	w.marks = append(w.marks, len(w.route))
	w.route += segment
}

func (w *pathWalker) pop() {
	if n := len(w.marks); n > 0 {
		w.route = w.route[:w.marks[n-1]]
		w.marks = w.marks[:n-1]
	}
}

// item walks one item, reporting whether the target was found inside it. When
// it reports true the route is left in place; otherwise the caller pops.
func (w *pathWalker) item(depth int) (bool, error) {
	if w.r.off == w.target {
		return true, nil
	}
	if depth >= maxRouteDepth || depth > w.r.opts.MaxNestedLevels || w.r.off > w.target {
		return false, errStopWalk
	}
	major, _, arg, indefinite, err := w.r.head()
	if err != nil {
		return false, err
	}
	switch major {
	case 2, 3:
		if indefinite {
			return w.walkIndefiniteString(major)
		}
		if arg > uint64(maxSliceLen) {
			return false, ErrLimitExceeded
		}
		_, err := w.r.take(int(arg))
		return false, err
	case 4:
		return w.walkArray(depth, arg, indefinite)
	case 5:
		return w.walkMap(depth, arg, indefinite)
	case 6:
		if indefinite {
			return false, ErrMalformed
		}
		w.push("(tagged)")
		found, err := w.item(depth + 1)
		if found {
			return true, nil
		}
		w.pop()
		return false, err
	default:
		return false, nil
	}
}

func (w *pathWalker) walkArray(depth int, arg uint64, indefinite bool) (bool, error) {
	for i := 0; ; i++ {
		if !indefinite && uint64(i) >= arg {
			return false, nil
		}
		if indefinite {
			done, err := w.atBreak()
			if err != nil || done {
				return false, err
			}
		}
		w.push("[" + strconv.Itoa(i) + "]")
		found, err := w.item(depth + 1)
		if found {
			return true, nil
		}
		w.pop()
		if err != nil {
			return false, err
		}
	}
}

func (w *pathWalker) walkMap(depth int, arg uint64, indefinite bool) (bool, error) {
	for i := 0; ; i++ {
		if !indefinite && uint64(i) >= arg {
			return false, nil
		}
		if indefinite {
			done, err := w.atBreak()
			if err != nil || done {
				return false, err
			}
		}
		keyStart := w.r.off
		w.push("{#" + strconv.Itoa(i) + " key}")
		found, err := w.item(depth + 1)
		if found {
			return true, nil
		}
		w.pop()
		if err != nil {
			return false, err
		}
		w.push("{" + describeKey(w.r.data[keyStart:w.r.off]) + "}")
		found, err = w.item(depth + 1)
		if found {
			return true, nil
		}
		w.pop()
		if err != nil {
			return false, err
		}
	}
}

func (w *pathWalker) walkIndefiniteString(major byte) (bool, error) {
	for {
		done, err := w.atBreak()
		if err != nil || done {
			return false, err
		}
		chunkMajor, _, chunkArg, chunkIndefinite, err := w.r.head()
		if err != nil {
			return false, err
		}
		if chunkIndefinite || chunkMajor != major || chunkArg > uint64(maxSliceLen) {
			return false, ErrMalformed
		}
		if _, err := w.r.take(int(chunkArg)); err != nil {
			return false, err
		}
	}
}

// atBreak reports whether the next byte ends an indefinite container, consuming
// it when it does.
func (w *pathWalker) atBreak() (bool, error) {
	if w.r.off >= len(w.r.data) {
		return false, ErrTruncated
	}
	if w.r.data[w.r.off] == 0xff {
		w.r.off++
		return true, nil
	}
	return false, nil
}

// describeKey renders an encoded map key compactly enough to name a field.
func describeKey(key []byte) string {
	if len(key) == 0 {
		return "?"
	}
	r := Reader{data: key, opts: DecoderOptions{MaxStringBytes: len(key), MaxNestedLevels: 1, MaxContainerItems: 1, MaxInputBytes: int64(len(key)), MaxRawMessageBytes: len(key)}}
	major, ai, arg, indefinite, err := r.head()
	if err != nil || indefinite {
		return "?"
	}
	switch major {
	case 0:
		return strconv.FormatUint(arg, 10)
	case 1:
		if arg > 1<<63-1 {
			return "-" + strconv.FormatUint(arg, 10) + "-1"
		}
		return strconv.FormatInt(-1-int64(arg), 10)
	case 3:
		if arg <= uint64(len(key)-r.off) {
			text := key[r.off : r.off+int(arg)]
			if len(text) > maxRouteKeyBytes {
				return strconv.Quote(string(text[:maxRouteKeyBytes])) + "..."
			}
			return strconv.Quote(string(text))
		}
	case 2:
		return "h'" + strconv.FormatUint(uint64(arg), 10) + " bytes'"
	case 7:
		switch ai {
		case 20:
			return "false"
		case 21:
			return "true"
		case 22:
			return "null"
		case 25:
			return strconv.FormatFloat(float16(binary.BigEndian.Uint16(key[r.off-2:r.off])), 'g', -1, 64)
		}
	}
	return "?"
}
