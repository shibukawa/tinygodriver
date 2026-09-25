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

// nsEntry is one xmlns declaration, in scope while the element at depth is
// open. The strings are copies: a declaration is made at the root and read
// at every depth below it, long after the buffer has moved on.
type nsEntry struct {
	depth  int
	prefix string
	uri    string
}

// XMLNamespace is the namespace the xml prefix is bound to without a
// declaration.
const XMLNamespace = "http://www.w3.org/XML/1998/namespace"

// attrEntry locates one attribute of the current start tag. The offsets are
// relative to the token start, which compaction keeps in step, so an entry
// stays valid for as long as the token does.
type attrEntry struct {
	nameOff, nameLen int32
	valOff, valLen   int32
}

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
	attrOff  int // raw attribute region of a StartElement or ProcInst
	attrLen  int
	textOff  int // body of Text, CData, Comment, ProcInst, Directive
	textLen  int
	attrs    []attrEntry // attributes of the current StartElement, offsets relative to tokStart
	attrPos  int         // NextAttr cursor into attrs
	ns       []nsEntry   // namespace declarations in scope, innermost last

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
	r.attrs = make([]attrEntry, 0, 8)
	r.ns = make([]nsEntry, 0, 8)
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
	r.attrs = r.attrs[:0]
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
func (r *Reader) NameIs(s string) bool { return Equal(r.Name(), s) }

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
		if len(r.ns) > 0 {
			r.popNamespaces()
		}
		return EndElement, nil
	}
	r.nameLen, r.attrLen, r.textLen, r.attrPos = 0, 0, 0, 0
	r.attrs = r.attrs[:0]
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
	if r.base == 0 && r.r == 0 {
		switch r.buf[0] {
		case 0xEF:
			// A UTF-8 byte order mark is not text.
			if ok, err := r.avail(2); err != nil {
				return None, err
			} else if ok && r.buf[1] == 0xBB && r.buf[2] == 0xBF {
				r.r += 3
				return r.next()
			}
		case 0xFF, 0xFE:
			// A UTF-16 byte order mark, in either order, is a document this
			// reader does not decode.
			if ok, err := r.avail(1); err != nil {
				return None, err
			} else if ok && r.buf[1] == r.buf[0]^1 {
				return None, ErrEncoding
			}
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
	// One pass over the attributes, recording where each name and value
	// sits, so a later Attr is a lookup in a short table rather than a
	// rescan of the tag. The scanner reads every byte of the tag anyway.
	//
	// The loop indexes the buffered window directly and refills only when
	// it runs off the end; every offset is relative to r.r, which a refill
	// keeps, so the parse resumes where it stopped.
	buf := r.buf[r.r:r.w]
	for {
		for i >= len(buf) {
			ok, err := r.more()
			if err != nil {
				return None, err
			}
			if !ok {
				return None, ErrTruncated
			}
			buf = r.buf[r.r:r.w]
		}
		c := buf[i]
		if isSpace(c) {
			i++
			continue
		}
		if c == '>' {
			break
		}
		if c == '/' {
			for i+1 >= len(buf) {
				ok, err := r.more()
				if err != nil {
					return None, err
				}
				if !ok {
					return None, ErrTruncated
				}
				buf = r.buf[r.r:r.w]
			}
			if buf[i+1] != '>' {
				return None, r.syntax(i, "unexpected '/' in tag")
			}
			selfClose = true
			i++
			break
		}
		// Attribute name, then optional space, '=', optional space, a quote.
		ns := i
		for {
			if i >= len(buf) {
				ok, err := r.more()
				if err != nil {
					return None, err
				}
				if !ok {
					return None, ErrTruncated
				}
				buf = r.buf[r.r:r.w]
				continue
			}
			c = buf[i]
			if !nameByte[c] {
				break
			}
			i++
		}
		if i == ns {
			return None, r.syntax(i, "unexpected byte in tag")
		}
		ne := i
		for {
			if i >= len(buf) {
				ok, err := r.more()
				if err != nil {
					return None, err
				}
				if !ok {
					return None, ErrTruncated
				}
				buf = r.buf[r.r:r.w]
				continue
			}
			c = buf[i]
			if !isSpace(c) {
				break
			}
			i++
		}
		if c != '=' {
			return None, r.syntax(i, "attribute without a value")
		}
		i++
		for {
			if i >= len(buf) {
				ok, err := r.more()
				if err != nil {
					return None, err
				}
				if !ok {
					return None, ErrTruncated
				}
				buf = r.buf[r.r:r.w]
				continue
			}
			c = buf[i]
			if !isSpace(c) {
				break
			}
			i++
		}
		if c != '"' && c != '\'' {
			return None, r.syntax(i, "attribute value is not quoted")
		}
		i++
		vs := i
		ve, err := r.indexByte(i, c)
		if err != nil {
			return None, err
		}
		if ve < 0 {
			return None, ErrTruncated
		}
		buf = r.buf[r.r:r.w]
		r.attrs = append(r.attrs, attrEntry{
			nameOff: int32(ns), nameLen: int32(ne - ns),
			valOff: int32(vs), valLen: int32(ve - vs),
		})
		i = ve + 1
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
	r.declareNamespaces()
	return StartElement, nil
}

// declareNamespaces records the xmlns attributes of the start tag just
// scanned. Almost every tag has none, and the check is one byte per
// attribute.
func (r *Reader) declareNamespaces() {
	for i := range r.attrs {
		e := &r.attrs[i]
		if e.nameLen < 5 || r.buf[r.tokStart+int(e.nameOff)] != 'x' {
			continue
		}
		name := r.attrName(*e)
		if !Equal(name[:5], "xmlns") {
			continue
		}
		var prefix string
		if len(name) > 5 {
			if name[5] != ':' {
				continue
			}
			prefix = string(name[6:])
		}
		r.ns = append(r.ns, nsEntry{depth: r.depth, prefix: prefix, uri: r.attrValue(*e).String()})
	}
}

// popNamespaces drops the declarations of elements no longer open.
func (r *Reader) popNamespaces() {
	for n := len(r.ns); n > 0 && r.ns[n-1].depth > r.depth; n-- {
		r.ns = r.ns[:n-1]
	}
}

// Namespace returns the namespace the current element's name is in: the
// one its prefix is bound to, or the default namespace when it has none.
// It is empty when nothing binds the prefix. Names are still matched as
// written; this is for the caller that must tell one vocabulary from
// another under the same prefix, or an unprefixed name under a default
// namespace from one without.
func (r *Reader) Namespace() string {
	uri, _ := r.LookupNamespace(r.Prefix())
	return uri
}

// LookupNamespace returns the namespace bound to prefix at the current
// position. An empty prefix looks up the default namespace. The xml prefix
// is always bound.
func (r *Reader) LookupNamespace(prefix []byte) (string, bool) {
	for i := len(r.ns) - 1; i >= 0; i-- {
		if Equal(prefix, r.ns[i].prefix) {
			return r.ns[i].uri, r.ns[i].uri != ""
		}
	}
	if Equal(prefix, "xml") {
		return XMLNamespace, true
	}
	return "", false
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
	if len(r.ns) > 0 {
		r.popNamespaces()
	}
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
	isDecl := Equal(r.Name(), "xml")
	r.r += i + 2
	if isDecl {
		if enc, ok := r.declAttr("encoding"); ok && !enc.EqualFold("utf-8") {
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
	return Equal(r.buf[r.r:r.r+len(s)], s), nil
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

// attrValue returns the value of entry e.
func (r *Reader) attrValue(e attrEntry) Value {
	return Value(r.buf[r.tokStart+int(e.valOff) : r.tokStart+int(e.valOff+e.valLen)])
}

// attrName returns the name of entry e.
func (r *Reader) attrName(e attrEntry) []byte {
	return r.buf[r.tokStart+int(e.nameOff) : r.tokStart+int(e.nameOff+e.nameLen)]
}

// Attr returns the value of the named attribute of the current StartElement,
// as written. The start tag was indexed as it was scanned, so this is a
// comparison against each of the element's few names, not a rescan.
func (r *Reader) Attr(name string) (Value, bool) {
	for i := range r.attrs {
		e := &r.attrs[i]
		if int(e.nameLen) == len(name) && Equal(r.attrName(*e), name) {
			return r.attrValue(*e), true
		}
	}
	return nil, false
}

// NextAttr returns the attributes of the current StartElement in document
// order, one per call, and reports false after the last. It restarts on each
// new token.
func (r *Reader) NextAttr() (name []byte, value Value, ok bool) {
	if r.attrPos >= len(r.attrs) {
		return nil, nil, false
	}
	e := r.attrs[r.attrPos]
	r.attrPos++
	return r.attrName(e), r.attrValue(e), true
}

// declAttr finds a pseudo-attribute of the XML declaration, whose body is
// small, in the buffer, and only ever asked one question.
func (r *Reader) declAttr(name string) (Value, bool) {
	b := r.buf[r.attrOff : r.attrOff+r.attrLen]
	i := 0
	for {
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i >= len(b) {
			return nil, false
		}
		ns := i
		for i < len(b) && b[i] != '=' && !isSpace(b[i]) {
			i++
		}
		n := b[ns:i]
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i >= len(b) || b[i] != '=' {
			return nil, false
		}
		i++
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i >= len(b) || (b[i] != '"' && b[i] != '\'') {
			return nil, false
		}
		q := b[i]
		i++
		j := bytes.IndexByte(b[i:], q)
		if j < 0 {
			return nil, false
		}
		if Equal(n, name) {
			return Value(b[i : i+j]), true
		}
		i += j + 1
	}
}

// Skip advances from a StartElement to its matching EndElement, reading
// nothing in between. It scans the subtree raw, tracking only tags, quotes
// and depth, so it neither indexes attributes nor checks that end tags
// match inside what it skips; the reader is left on the end tag with Name
// set, exactly as Next would leave it.
func (r *Reader) Skip() error {
	if r.kind != StartElement {
		return ErrNotStart
	}
	if r.err != nil {
		return r.err
	}
	if r.pendingEnd {
		_, err := r.Next()
		return err
	}
	if err := r.skipRaw(); err != nil {
		r.err = err
		r.kind = None
		return err
	}
	r.kind = EndElement
	return nil
}

func (r *Reader) skipRaw() error {
	r.attrs = r.attrs[:0]
	r.attrLen, r.textLen, r.attrPos = 0, 0, 0
	d := 1
	for d > 0 {
		i, err := r.indexByte(0, '<')
		if err != nil {
			return err
		}
		if i < 0 {
			return ErrTruncated
		}
		if ok, err := r.avail(i + 1); err != nil {
			return err
		} else if !ok {
			return ErrTruncated
		}
		switch r.buf[r.r+i+1] {
		case '/':
			end, _, err := r.scanName(i + 2)
			if err != nil {
				return err
			}
			j, err := r.indexByte(end, '>')
			if err != nil {
				return err
			}
			if j < 0 {
				return ErrTruncated
			}
			d--
			if d == 0 {
				r.tokStart = r.r + i
				r.nameOff, r.nameLen = r.r+i+2, end-i-2
			}
			r.r += j + 1
		case '!':
			var term string
			var skip int
			if ok, err := r.hasPrefixAt(i, "<!--"); err != nil {
				return err
			} else if ok {
				term, skip = "-->", 4
			} else if ok, err := r.hasPrefixAt(i, "<![CDATA["); err != nil {
				return err
			} else if ok {
				term, skip = "]]>", 9
			} else {
				return r.syntax(i, "unexpected '<!'")
			}
			j, err := r.index(i+skip, term)
			if err != nil {
				return err
			}
			if j < 0 {
				return ErrTruncated
			}
			r.r += j + len(term)
		case '?':
			j, err := r.index(i+2, "?>")
			if err != nil {
				return err
			}
			if j < 0 {
				return ErrTruncated
			}
			r.r += j + 2
		default:
			// A start tag: find its end, honouring quotes.
			j := i + 1
			var quote byte
			selfClose := false
			buf := r.buf[r.r:r.w]
		tag:
			for {
				for j >= len(buf) {
					ok, err := r.more()
					if err != nil {
						return err
					}
					if !ok {
						return ErrTruncated
					}
					buf = r.buf[r.r:r.w]
				}
				c := buf[j]
				if quote != 0 {
					if c == quote {
						quote = 0
					}
					j++
					continue
				}
				switch c {
				case '"', '\'':
					quote = c
				case '>':
					if j > 0 && buf[j-1] == '/' {
						selfClose = true
					}
					break tag
				case '<':
					return r.syntax(j, "unexpected '<' in tag")
				}
				j++
			}
			if !selfClose {
				d++
				if r.depth+d > r.opts.MaxDepth {
					return ErrTooDeep
				}
			}
			r.r += j + 1
		}
	}
	r.depth--
	r.stack = r.stack[:r.depth]
	if len(r.ns) > 0 {
		r.popNamespaces()
	}
	return nil
}

// hasPrefixAt reports whether the input at rel starts with s.
func (r *Reader) hasPrefixAt(rel int, s string) (bool, error) {
	ok, err := r.avail(rel + len(s) - 1)
	if err != nil || !ok {
		return false, err
	}
	return Equal(r.buf[r.r+rel:r.r+rel+len(s)], s), nil
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
