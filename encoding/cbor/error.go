package cbor

import "fmt"

// Error locates a decode failure. It wraps one of the package sentinels, so
// errors.Is keeps working unchanged, and adds the offset at which the failing
// item began.
//
// A byte-format error is otherwise only as useful as the caller's ability to
// find it. Rejecting one attestation needs the sentinel; comparing two builds
// that disagree about a message needs the position.
type Error struct {
	// Offset is the byte position in the input at which the failing item began.
	Offset int64
	// Path names the container route to the failing item where one is known,
	// as it would be written in diagnostic notation, and is empty otherwise.
	Path string
	Err  error
}

func (e *Error) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("%v (at byte %d, %s)", e.Err, e.Offset, e.Path)
	}
	return fmt.Sprintf("%v (at byte %d)", e.Err, e.Offset)
}

func (e *Error) Unwrap() error { return e.Err }

// at wraps err with a position and the container route to it, unless it
// already carries one. The route costs a second walk of the input, which is
// why it is computed here rather than tracked while decoding.
func (r *Reader) at(offset int, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*Error); ok {
		return err
	}
	return &Error{Offset: int64(offset), Path: r.pathTo(offset), Err: err}
}

// at wraps err with a position unless it already carries one.
func at(offset int, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*Error); ok {
		return err
	}
	return &Error{Offset: int64(offset), Err: err}
}
