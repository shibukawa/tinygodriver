package xml

import (
	"bytes"
	"errors"
	"io"
	"strconv"
)

// Kind is the kind of token the reader is positioned on.
type Kind uint8

const (
	// None is the kind before the first Next and after an error.
	None Kind = iota
	// StartElement is an opening tag. A self-closing tag is a StartElement
	// followed by an EndElement, as encoding/xml reports it.
	StartElement
	// EndElement is a closing tag.
	EndElement
	// Text is character data between tags, as written: entities are not
	// decoded and whitespace is not trimmed.
	Text
	// CData is the content of a CDATA section. It contains no entities.
	CData
	// Comment is the body of a comment.
	Comment
	// ProcInst is a processing instruction, including the XML declaration,
	// whose target is "xml".
	ProcInst
	// Directive is a DOCTYPE, reported only when Options.AllowDoctype is set.
	Directive
	// EOF is reported once at the end of a well-formed document.
	EOF
)

var kindNames = [...]string{"None", "StartElement", "EndElement", "Text", "CData", "Comment", "ProcInst", "Directive", "EOF"}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// Options bounds a Reader. The zero value selects the defaults noted on each
// field.
type Options struct {
	// BufferSize is the initial buffer, 64 KiB by default. It grows only when
	// one token does not fit, and only up to MaxBufferBytes.
	BufferSize int
	// MaxBufferBytes bounds the buffer, and so the largest single token, the
	// largest ElementText and the largest RawElement. 1 MiB by default.
	MaxBufferBytes int
	// MaxDepth bounds element nesting, 1024 by default.
	MaxDepth int
	// AllowDoctype reports a DOCTYPE as a Directive instead of refusing it.
	// An internal subset is refused either way.
	AllowDoctype bool
}

const (
	defaultBufferSize     = 64 << 10
	defaultMaxBufferBytes = 1 << 20
	defaultMaxDepth       = 1024
)

var (
	// ErrTruncated is returned when the input ends inside a token or inside an
	// open element.
	ErrTruncated = errors.New("xml: unexpected end of input")
	// ErrTooLarge is returned when a token or a capture does not fit in
	// Options.MaxBufferBytes.
	ErrTooLarge = errors.New("xml: token exceeds MaxBufferBytes")
	// ErrTooDeep is returned when nesting exceeds Options.MaxDepth.
	ErrTooDeep = errors.New("xml: nesting exceeds MaxDepth")
	// ErrDoctype is returned for a DOCTYPE unless Options.AllowDoctype is set.
	ErrDoctype = errors.New("xml: DOCTYPE refused")
	// ErrEncoding is returned when the XML declaration names an encoding other
	// than UTF-8.
	ErrEncoding = errors.New("xml: declared encoding is not UTF-8")
	// ErrNotStart is returned by the element-level calls when the reader is
	// not positioned on a StartElement.
	ErrNotStart = errors.New("xml: not positioned on a start element")
)

// SyntaxError reports malformed input at a byte offset from the start of the
// document.
type SyntaxError struct {
	Offset int64
	Msg    string
}

func (e *SyntaxError) Error() string {
	return "xml: " + e.Msg + " at byte " + strconv.FormatInt(e.Offset, 10)
}

// Element identifies an element the reader has entered, for NextChild. It is
// a value, held on the caller's stack.
type Element struct{ depth int }

// Reader reads XML tokens from an io.Reader or from a byte slice.
//
// Every slice a Reader returns aliases its buffer and is valid until the next
// call that advances the reader: Next, Skip, NextChild, ElementText,
// RawElement and Decode. Callers that keep a name or a value copy it.
//
// A Reader is not safe for concurrent use. Reuse one across documents with
// Reset rather than allocating one per document.
type Reader struct {
	src   io.Reader
	buf   []byte
	r, w  int   // buf[r:w] is unread; r is the start of the token being scanned
	base  int64 // bytes discarded before buf[0]
	eof   bool
	owned bool // buf may be compacted and grown
	pin   int  // an index compaction must keep, or -1

	kind       Kind
	depth      int
	pendingEnd bool
	stack      []uint32 // name hashes of the open elements, len == depth

	tokStart int // where the current token began
	nameOff  int
	nameLen  int
	attrOff  int // raw attribute region of a StartElement
	attrLen  int
	textOff  int // body of Text, CData, Comment, ProcInst, Directive
	textLen  int
	attrPos  int // NextAttr cursor, relative to attrOff

	scratch []byte
	opts    Options
	err     error
}

// NewReader returns a Reader over src.
func NewReader(src io.Reader, opts Options) *Reader {
	r := &Reader{}
	r.init(opts)
	r.Reset(src)
	return r
}

// NewBytesReader returns a Reader over data, which it neither copies nor
// modifies. BufferSize and MaxBufferBytes do not apply: the buffer is data.
func NewBytesReader(data []byte, opts Options) *Reader {
	r := &Reader{}
	r.init(opts)
	r.ResetBytes(data)
	return r
}

func (r *Reader) init(opts Options) {
	if opts.BufferSize <= 0 {
		opts.BufferSize = defaultBufferSize
	}
	if opts.MaxBufferBytes <= 0 {
		opts.MaxBufferBytes = defaultMaxBufferBytes
	}
	if opts.BufferSize > opts.MaxBufferBytes {
		opts.BufferSize = opts.MaxBufferBytes
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = defaultMaxDepth
	}
	r.opts = opts
	r.stack = make([]uint32, 0, 32)
}

func (r *Reader) reset() {
	r.r, r.w, r.base = 0, 0, 0
	r.eof = false
	r.pin = -1
	r.kind = None
	r.depth = 0
	r.pendingEnd = false
	r.stack = r.stack[:0]
	r.tokStart, r.nameOff, r.nameLen, r.attrOff, r.attrLen, r.textOff, r.textLen, r.attrPos = 0, 0, 0, 0, 0, 0, 0, 0
	r.scratch = r.scratch[:0]
	r.err = nil
}

// Reset points the Reader at a new source, keeping its options and its
// buffer.
func (r *Reader) Reset(src io.Reader) {
	r.reset()
	r.src = src
	if !r.owned || r.buf == nil {
		r.buf = make([]byte, r.opts.BufferSize)
	}
	r.owned = true
}

// ResetBytes points the Reader at data, keeping its options. Slices returned
// before the call still alias the old input.
func (r *Reader) ResetBytes(data []byte) {
	r.reset()
	r.src = nil
	r.buf = data
	r.w = len(data)
	r.eof = true
	r.owned = false
}

// Kind reports the kind of the current token.
func (r *Reader) Kind() Kind { return r.kind }

// Depth reports how many elements are open. On a StartElement it counts that
// element; on its EndElement it no longer does.
func (r *Reader) Depth() int { return r.depth }

// Offset reports how many bytes of the document precede the next token.
func (r *Reader) Offset() int64 { return r.base + int64(r.r) }

// Name returns the qualified name of a StartElement or EndElement, or the
// target of a ProcInst, as written.
func (r *Reader) Name() []byte { return r.buf[r.nameOff : r.nameOff+r.nameLen] }

// NameIs reports whether the qualified name equals s. It allocates nothing.
func (r *Reader) NameIs(s string) bool { return string(r.Name()) == s }

// LocalName returns the name after the prefix, or the whole name when there
// is none.
func (r *Reader) LocalName() []byte {
	n := r.Name()
	if i := bytes.IndexByte(n, ':'); i >= 0 {
		return n[i+1:]
	}
	return n
}

// Prefix returns the namespace prefix of the name, or nil when there is none.
func (r *Reader) Prefix() []byte {
	n := r.Name()
	if i := bytes.IndexByte(n, ':'); i >= 0 {
		return n[:i]
	}
	return nil
}

// Text returns the body of a Text, CData, Comment, ProcInst or Directive
// token as written. For Text the entities are still encoded; see Value.
func (r *Reader) Text() Value { return Value(r.buf[r.textOff : r.textOff+r.textLen]) }

// Element returns a handle to the element the reader is in, for NextChild.
// On a StartElement that is the element itself; anywhere else it is the
// innermost open element.
func (r *Reader) Element() Element { return Element{depth: r.depth} }

// more reads more input into the buffer, compacting and growing as needed.
// It reports false when the source is exhausted.
func (r *Reader) more() (bool, error) {
	if r.eof {
		return false, nil
	}
	keep := r.r
	if r.pin >= 0 && r.pin < keep {
		keep = r.pin
	}
	if keep > 0 {
		n := copy(r.buf, r.buf[keep:r.w])
		r.w = n
		r.r -= keep
		r.base += int64(keep)
		r.tokStart -= keep
		r.nameOff -= keep
		r.attrOff -= keep
		r.textOff -= keep
		if r.pin >= 0 {
			r.pin -= keep
		}
	}
	if r.w == len(r.buf) {
		if len(r.buf) >= r.opts.MaxBufferBytes {
			return false, ErrTooLarge
		}
		n := 2 * len(r.buf)
		if n > r.opts.MaxBufferBytes {
			n = r.opts.MaxBufferBytes
		}
		nb := make([]byte, n)
		copy(nb, r.buf[:r.w])
		r.buf = nb
	}
	for range 100 {
		n, err := r.src.Read(r.buf[r.w:])
		r.w += n
		if err == io.EOF {
			r.eof = true
			return n > 0, nil
		}
		if err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, io.ErrNoProgress
}

// avail makes sure buf[r+rel] exists, reading more if needed. It reports
// false at the end of input.
func (r *Reader) avail(rel int) (bool, error) {
	for r.r+rel >= r.w {
		ok, err := r.more()
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// indexByte returns the offset of c at or after rel, relative to r.r, or -1
// at the end of input.
func (r *Reader) indexByte(rel int, c byte) (int, error) {
	for {
		if i := bytes.IndexByte(r.buf[r.r+rel:r.w], c); i >= 0 {
			return rel + i, nil
		}
		rel = r.w - r.r
		ok, err := r.more()
		if err != nil {
			return -1, err
		}
		if !ok {
			return -1, nil
		}
	}
}

// index is indexByte for a short terminator such as "-->".
func (r *Reader) index(rel int, s string) (int, error) {
	for {
		if i := bytes.Index(r.buf[r.r+rel:r.w], []byte(s)); i >= 0 {
			return rel + i, nil
		}
		if n := r.w - r.r - (len(s) - 1); n > rel {
			rel = n
		}
		ok, err := r.more()
		if err != nil {
			return -1, err
		}
		if !ok {
			return -1, nil
		}
	}
}

func (r *Reader) syntax(rel int, msg string) error {
	return &SyntaxError{Offset: r.base + int64(r.r+rel), Msg: msg}
}

// Next advances to the next token and reports its kind. After an error every
// later call returns the same error.
func (r *Reader) Next() (Kind, error) {
	if r.err != nil {
		return None, r.err
	}
	k, err := r.next()
	if err != nil {
		r.err = err
		r.kind = None
		return None, err
	}
	r.kind = k
	return k, nil
}

func (r *Reader) next() (Kind, error) {
	if r.pendingEnd {
		r.pendingEnd = false
		r.depth--
		r.stack = r.stack[:r.depth]
		return EndElement, nil
	}
	r.nameLen, r.attrLen, r.textLen, r.attrPos = 0, 0, 0, 0
	ok, err := r.avail(0)
	if err != nil {
		return None, err
	}
	if !ok {
		if r.depth > 0 {
			return None, ErrTruncated
		}
		return EOF, nil
	}
	r.tokStart = r.r
	if r.buf[r.r] != '<' {
		return r.scanText()
	}
	if ok, err = r.avail(1); err != nil {
		return None, err
	} else if !ok {
		return None, ErrTruncated
	}
	switch r.buf[r.r+1] {
	case '/':
		return r.scanEndTag()
	case '?':
		return r.scanProcInst()
	case '!':
		return r.scanBang()
	}
	return r.scanStartTag()
}

func (r *Reader) scanText() (Kind, error) {
	if r.base == 0 && r.r == 0 && r.buf[0] == 0xEF {
		// A byte order mark is not text.
		if ok, err := r.avail(2); err != nil {
			return None, err
		} else if ok && r.buf[1] == 0xBB && r.buf[2] == 0xBF {
			r.r += 3
			return r.next()
		}
	}
	i, err := r.indexByte(1, '<')
	if err != nil {
		return None, err
	}
	if i < 0 {
		i = r.w - r.r
	}
	r.textOff, r.textLen = r.r, i
	r.r += i
	return Text, nil
}

// nameByte marks the bytes a name may contain: everything but whitespace,
// the tag delimiters and the attribute punctuation. It is deliberately looser
// than the XML production, which a matching consumer does not need.
var nameByte = func() (t [256]bool) {
	for c := range 256 {
		t[c] = true
	}
	for _, c := range " \t\r\n<>/=\"'?&" {
		t[c] = false
	}
	return
}()

const (
	fnvOffset = 2166136261
	fnvPrime  = 16777619
)

// scanName scans a name starting at rel and returns the offset after it and
// its hash.
func (r *Reader) scanName(rel int) (int, uint32, error) {
	h := uint32(fnvOffset)
	for {
		if r.r+rel >= r.w {
			ok, err := r.more()
			if err != nil {
				return 0, 0, err
			}
			if !ok {
				return 0, 0, ErrTruncated
			}
			continue
		}
		c := r.buf[r.r+rel]
		if !nameByte[c] {
			return rel, h, nil
		}
		h = (h ^ uint32(c)) * fnvPrime
		rel++
	}
}

func (r *Reader) scanStartTag() (Kind, error) {
	end, h, err := r.scanName(1)
	if err != nil {
		return None, err
	}
	if end == 1 {
		return None, r.syntax(1, "expected element name")
	}
	nameLen := end - 1
	attrStart := end
	i := end
	selfClose := false
	var quote byte
scan:
	for {
		if r.r+i >= r.w {
			ok, err := r.more()
			if err != nil {
				return None, err
			}
			if !ok {
				return None, ErrTruncated
			}
			continue
		}
		c := r.buf[r.r+i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			i++
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '>':
			break scan
		case '/':
			ok, err := r.avail(i + 1)
			if err != nil {
				return None, err
			}
			if !ok {
				return None, ErrTruncated
			}
			if r.buf[r.r+i+1] != '>' {
				return None, r.syntax(i, "unexpected '/' in tag")
			}
			selfClose = true
			i++
			break scan
		case '<':
			return None, r.syntax(i, "unexpected '<' in tag")
		}
		i++
	}
	attrEnd := i
	if selfClose {
		attrEnd--
	}
	if r.depth >= r.opts.MaxDepth {
		return None, ErrTooDeep
	}
	r.nameOff, r.nameLen = r.r+1, nameLen
	r.attrOff, r.attrLen = r.r+attrStart, attrEnd-attrStart
	r.r += i + 1
	r.stack = append(r.stack, h)
	r.depth++
	r.pendingEnd = selfClose
	return StartElement, nil
}

func (r *Reader) scanEndTag() (Kind, error) {
	end, h, err := r.scanName(2)
	if err != nil {
		return None, err
	}
	if end == 2 {
		return None, r.syntax(2, "expected element name")
	}
	i := end
	for {
		ok, err := r.avail(i)
		if err != nil {
			return None, err
		}
		if !ok {
			return None, ErrTruncated
		}
		c := r.buf[r.r+i]
		if c == '>' {
			break
		}
		if !isSpace(c) {
			return None, r.syntax(i, "expected '>' after end tag name")
		}
		i++
	}
	if r.depth == 0 {
		return None, r.syntax(0, "end tag with no open element")
	}
	if r.stack[r.depth-1] != h {
		return None, r.syntax(0, "end tag does not match open element")
	}
	r.nameOff, r.nameLen = r.r+2, end-2
	r.r += i + 1
	r.depth--
	r.stack = r.stack[:r.depth]
	return EndElement, nil
}

func (r *Reader) scanProcInst() (Kind, error) {
	end, _, err := r.scanName(2)
	if err != nil {
		return None, err
	}
	if end == 2 {
		return None, r.syntax(2, "expected processing instruction target")
	}
	i, err := r.index(end, "?>")
	if err != nil {
		return None, err
	}
	if i < 0 {
		return None, ErrTruncated
	}
	r.nameOff, r.nameLen = r.r+2, end-2
	r.attrOff, r.attrLen = r.r+end, i-end
	r.textOff, r.textLen = r.attrOff, r.attrLen
	isDecl := string(r.Name()) == "xml"
	r.r += i + 2
	if isDecl {
		if enc, ok := r.Attr("encoding"); ok && !enc.EqualFold("utf-8") {
			return None, ErrEncoding
		}
	}
	return ProcInst, nil
}

func (r *Reader) hasPrefix(s string) (bool, error) {
	ok, err := r.avail(len(s) - 1)
	if err != nil || !ok {
		return false, err
	}
	return string(r.buf[r.r:r.r+len(s)]) == s, nil
}

func (r *Reader) scanBang() (Kind, error) {
	if ok, err := r.hasPrefix("<!--"); err != nil {
		return None, err
	} else if ok {
		i, err := r.index(4, "-->")
		if err != nil {
			return None, err
		}
		if i < 0 {
			return None, ErrTruncated
		}
		r.textOff, r.textLen = r.r+4, i-4
		r.r += i + 3
		return Comment, nil
	}
	if ok, err := r.hasPrefix("<![CDATA["); err != nil {
		return None, err
	} else if ok {
		i, err := r.index(9, "]]>")
		if err != nil {
			return None, err
		}
		if i < 0 {
			return None, ErrTruncated
		}
		r.textOff, r.textLen = r.r+9, i-9
		r.r += i + 3
		return CData, nil
	}
	if ok, err := r.hasPrefix("<!DOCTYPE"); err != nil {
		return None, err
	} else if ok {
		if !r.opts.AllowDoctype {
			return None, ErrDoctype
		}
		i, err := r.indexByte(9, '>')
		if err != nil {
			return None, err
		}
		if i < 0 {
			return None, ErrTruncated
		}
		if bytes.IndexByte(r.buf[r.r+9:r.r+i], '[') >= 0 {
			return None, r.syntax(9, "DOCTYPE internal subset not supported")
		}
		r.textOff, r.textLen = r.r+9, i-9
		r.r += i + 1
		return Directive, nil
	}
	return None, r.syntax(0, "unexpected '<!'")
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// attrs is the raw attribute region of the current StartElement.
func (r *Reader) attrs() []byte { return r.buf[r.attrOff : r.attrOff+r.attrLen] }

// scanAttr parses one attribute of b starting at i. It returns the name, the
// raw value and the offset after the value; ok is false at the end of the
// region. A malformed attribute is recorded as the reader's error.
func (r *Reader) scanAttr(b []byte, i int) (name []byte, value Value, next int, ok bool) {
	for i < len(b) && isSpace(b[i]) {
		i++
	}
	if i >= len(b) {
		return nil, nil, i, false
	}
	ns := i
	for i < len(b) && b[i] != '=' && !isSpace(b[i]) {
		i++
	}
	name = b[ns:i]
	for i < len(b) && isSpace(b[i]) {
		i++
	}
	if i >= len(b) || b[i] != '=' || len(name) == 0 {
		r.err = &SyntaxError{Offset: r.base + int64(r.attrOff+i), Msg: "attribute without a value"}
		return nil, nil, i, false
	}
	i++
	for i < len(b) && isSpace(b[i]) {
		i++
	}
	if i >= len(b) || (b[i] != '"' && b[i] != '\'') {
		r.err = &SyntaxError{Offset: r.base + int64(r.attrOff+i), Msg: "attribute value is not quoted"}
		return nil, nil, i, false
	}
	q := b[i]
	i++
	vs := i
	j := bytes.IndexByte(b[i:], q)
	if j < 0 {
		// scanStartTag balanced the quotes, so this cannot happen.
		r.err = &SyntaxError{Offset: r.base + int64(r.attrOff+i), Msg: "unterminated attribute value"}
		return nil, nil, i, false
	}
	return name, Value(b[vs : vs+j]), vs + j + 1, true
}

// Attr returns the value of the named attribute of the current StartElement,
// as written. It scans the tag each time, which for the handful of attributes
// an Office element carries is cheaper than building a table.
func (r *Reader) Attr(name string) (Value, bool) {
	b := r.attrs()
	i := 0
	for {
		n, v, next, ok := r.scanAttr(b, i)
		if !ok {
			return nil, false
		}
		if string(n) == name {
			return v, true
		}
		i = next
	}
}

// NextAttr returns the attributes of the current StartElement in document
// order, one per call, and reports false after the last. It restarts on each
// new token.
func (r *Reader) NextAttr() (name []byte, value Value, ok bool) {
	name, value, r.attrPos, ok = r.scanAttr(r.attrs(), r.attrPos)
	return name, value, ok
}

// Skip advances from a StartElement to its matching EndElement, reading
// nothing in between.
func (r *Reader) Skip() error {
	if r.kind != StartElement {
		return ErrNotStart
	}
	d := r.depth
	for {
		k, err := r.Next()
		if err != nil {
			return err
		}
		if k == EndElement && r.depth == d-1 {
			return nil
		}
	}
}

// NextChild advances to the next direct child element of e and reports
// whether there was one. Anything between children is passed over, and a
// child the caller did not consume is skipped, so a loop over NextChild ends
// on the EndElement of e however much of each child it read.
func (r *Reader) NextChild(e Element) (bool, error) {
	for {
		if r.depth < e.depth {
			return false, nil
		}
		if r.depth > e.depth && r.kind == StartElement {
			if err := r.Skip(); err != nil {
				return false, err
			}
			continue
		}
		k, err := r.Next()
		if err != nil {
			return false, err
		}
		switch k {
		case StartElement:
			if r.depth == e.depth+1 {
				return true, nil
			}
		case EndElement:
			if r.depth == e.depth-1 {
				return false, nil
			}
		case EOF:
			return false, nil
		}
	}
}

// ElementText advances from a StartElement to its EndElement and returns the
// element's own character data with entities decoded. Text in child elements
// is not included; the children are skipped. When the text is one run with
// no entity the result aliases the buffer; otherwise it is assembled in a
// scratch buffer the next call reuses.
func (r *Reader) ElementText() (Value, error) {
	if r.kind != StartElement {
		return nil, ErrNotStart
	}
	v, err := r.elementText()
	r.pin = -1
	return v, err
}

func (r *Reader) elementText() (Value, error) {
	d := r.depth
	r.scratch = r.scratch[:0]
	chunks := 0
	firstLen := 0
	firstCData := false
	inScratch := false
	for {
		k, err := r.Next()
		if err != nil {
			return nil, err
		}
		switch k {
		case Text, CData:
			if chunks == 0 {
				r.pin = r.textOff
				firstLen = r.textLen
				firstCData = k == CData
			} else {
				if !inScratch {
					inScratch = true
					first := r.buf[r.pin : r.pin+firstLen]
					if firstCData {
						r.scratch = append(r.scratch, first...)
					} else {
						r.scratch = Unescape(r.scratch, first)
					}
					r.pin = -1
				}
				if k == CData {
					r.scratch = append(r.scratch, r.Text()...)
				} else {
					r.scratch = Unescape(r.scratch, r.Text())
				}
			}
			chunks++
		case StartElement:
			if err := r.Skip(); err != nil {
				return nil, err
			}
		case EndElement:
			if r.depth == d-1 {
				if inScratch {
					return Value(r.scratch), nil
				}
				if chunks == 0 {
					return Value{}, nil
				}
				v := Value(r.buf[r.pin : r.pin+firstLen])
				if !firstCData && v.HasEntities() {
					r.scratch = Unescape(r.scratch, v)
					return Value(r.scratch), nil
				}
				return v, nil
			}
		}
	}
}

// RawElement advances from a StartElement to its EndElement and returns the
// bytes of the whole element, tags included, as written. The result aliases
// the buffer, so the element must fit in MaxBufferBytes.
func (r *Reader) RawElement() ([]byte, error) {
	if r.kind != StartElement {
		return nil, ErrNotStart
	}
	r.pin = r.tokStart
	err := r.Skip()
	if err != nil {
		r.pin = -1
		return nil, err
	}
	raw := r.buf[r.pin:r.r]
	r.pin = -1
	return raw, nil
}

// Decodable is implemented by a type that reads itself from an element. The
// reader is positioned on the element's StartElement when the method is
// called, and the method returns with the reader on the matching EndElement;
// NextChild and ElementText both end there.
type Decodable interface {
	DecodeXMLFrom(r *Reader) error
}

// Decode reads the current element into d and checks that d consumed exactly
// that element.
func (r *Reader) Decode(d Decodable) error {
	if r.kind != StartElement {
		return ErrNotStart
	}
	want := r.depth - 1
	if err := d.DecodeXMLFrom(r); err != nil {
		return err
	}
	if r.kind != EndElement || r.depth != want {
		return errors.New("xml: decoder did not end on the element's end tag")
	}
	return nil
}
