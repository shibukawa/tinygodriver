package xmlro

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

// CharsetError reports that Options.CharsetReader refused the encoding the
// XML declaration named, or returned no reader for it.
type CharsetError struct {
	Label string
	Err   error
}

func (e *CharsetError) Error() string {
	return "xml: opening charset " + strconv.Quote(e.Label) + ": " + e.Err.Error()
}

func (e *CharsetError) Unwrap() error { return e.Err }

var errNoCharsetReader = errors.New("CharsetReader returned no reader")

// switchCharset hands the input after the XML declaration to
// Options.CharsetReader and continues from what it returns. It runs before
// the token after the declaration is scanned, so the declaration itself is
// still readable when the caller sees it.
func (r *Reader) switchCharset() error {
	label := r.pendingCharset
	r.pendingCharset = ""
	rest := r.buf[r.r:r.w]
	var src io.Reader
	if r.src == nil {
		// A byte slice: the caller's data stays where it is and is read
		// from here on.
		src = bytes.NewReader(rest)
	} else {
		// The buffer is about to be reused for the converted input.
		src = io.MultiReader(bytes.NewReader(append([]byte(nil), rest...)), r.src)
	}
	cs, err := r.opts.CharsetReader(label, src)
	if err != nil {
		return &CharsetError{Label: label, Err: err}
	}
	if cs == nil {
		return &CharsetError{Label: label, Err: errNoCharsetReader}
	}
	r.base += int64(r.r)
	r.r, r.w = 0, 0
	r.tokStart = 0
	r.eof = false
	r.src = cs
	if !r.owned {
		r.buf = make([]byte, r.opts.BufferSize)
		r.owned = true
	}
	return nil
}

// transcodeUTF16 switches the reader to UTF-8 decoded from UTF-16 input
// whose byte order mark was just seen. A byte slice is decoded whole into a
// buffer of the reader's own; a stream is decoded on the way in. Offset
// counts the decoded bytes from after the mark.
func (r *Reader) transcodeUTF16(bigEndian bool) {
	r.transcoded = true
	raw := r.buf[2:r.w]
	if r.src == nil {
		out := make([]byte, 0, len(raw)+len(raw)/2)
		out, _ = decodeUTF16(out, raw, bigEndian, true)
		r.buf = out
		r.r, r.w = 0, len(out)
		r.owned = false
		return
	}
	pending := append([]byte(nil), raw...)
	r.src = &utf16Reader{src: io.MultiReader(bytes.NewReader(pending), r.src), bigEndian: bigEndian}
	r.r, r.w = 0, 0
	r.eof = false
}

// decodeUTF16 appends raw decoded to dst and returns how many bytes of raw
// it used. An incomplete code unit or an unpaired high surrogate at the end
// is left for the next call unless final, when it becomes U+FFFD, as a lone
// low surrogate or an unpaired high surrogate elsewhere does.
func decodeUTF16(dst, raw []byte, bigEndian, final bool) ([]byte, int) {
	unit := func(i int) rune {
		if bigEndian {
			return rune(raw[i])<<8 | rune(raw[i+1])
		}
		return rune(raw[i+1])<<8 | rune(raw[i])
	}
	i := 0
	for i+1 < len(raw) {
		u := unit(i)
		switch {
		case u < 0x80:
			dst = append(dst, byte(u))
			i += 2
		case u < 0xD800 || u >= 0xE000:
			dst = utf8.AppendRune(dst, u)
			i += 2
		case u < 0xDC00:
			if i+3 >= len(raw) {
				if !final {
					return dst, i
				}
				dst = utf8.AppendRune(dst, utf8.RuneError)
				i += 2
				continue
			}
			if u2 := unit(i + 2); u2 >= 0xDC00 && u2 < 0xE000 {
				dst = utf8.AppendRune(dst, (u-0xD800)<<10|(u2-0xDC00)+0x10000)
				i += 4
			} else {
				dst = utf8.AppendRune(dst, utf8.RuneError)
				i += 2
			}
		default:
			dst = utf8.AppendRune(dst, utf8.RuneError)
			i += 2
		}
	}
	if final && i < len(raw) {
		dst = utf8.AppendRune(dst, utf8.RuneError)
		i = len(raw)
	}
	return dst, i
}

// utf16Reader decodes a UTF-16 stream to UTF-8 as it is read.
type utf16Reader struct {
	src       io.Reader
	bigEndian bool
	raw       []byte // input not yet decoded: a chunk, then up to three bytes carried over
	rawN      int
	out       []byte // decoded bytes not yet delivered
	outPos    int
	err       error
}

const utf16Chunk = 4096

func (u *utf16Reader) Read(p []byte) (int, error) {
	for u.outPos >= len(u.out) {
		if u.err != nil {
			return 0, u.err
		}
		if u.raw == nil {
			u.raw = make([]byte, utf16Chunk)
			u.out = make([]byte, 0, utf16Chunk+utf16Chunk/2)
		}
		n, err := u.src.Read(u.raw[u.rawN:])
		u.rawN += n
		if err != nil {
			u.err = err
		}
		out, used := decodeUTF16(u.out[:0], u.raw[:u.rawN], u.bigEndian, u.err == io.EOF)
		u.out, u.outPos = out, 0
		u.rawN = copy(u.raw, u.raw[used:u.rawN])
		if len(out) == 0 && u.err == nil {
			// Nothing decodable yet and no error: let the caller retry, as
			// it does for any Read that makes no progress.
			return 0, nil
		}
	}
	n := copy(p, u.out[u.outPos:])
	u.outPos += n
	return n, nil
}
