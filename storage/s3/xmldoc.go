package s3

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"time"
)

// A hand-rolled reader for the XML documents this package consumes, and a
// renderer for the ones it produces. encoding/xml handles arbitrary documents
// through reflection, which is a large dependency for a TinyGo binary that
// only ever sees ListBucketResult, Error and the two flat multipart replies.
//
// The subset understood here: elements with attributes, character data with
// the five predefined entities plus numeric references, comments, and
// processing instructions. CDATA sections and DTDs are not, because S3 and the
// S3-compatible servers do not emit them for these documents.

var errMalformedXML = errors.New("s3: malformed XML document")

// xmlScan walks a document tag by tag. The character data in front of the
// most recent tag is recorded as a range into b and unescaped only when a
// consumer asks for it through text — most runs are structure the parsers
// discard, and unescaping them was most of what parsing a listing allocated.
type xmlScan struct {
	b            []byte
	i            int
	text0, text1 int
}

// Tag events. Self-closing tags are reported as xmlSelf rather than a
// synthetic open/close pair, so consumers see exactly what the bytes say.
const (
	xmlOpen = iota
	xmlSelf
	xmlClose
	xmlEOF
)

// text unescapes the character data recorded by the last next call.
func (s *xmlScan) text() string {
	return xmlUnescape(s.b[s.text0:s.text1])
}

var xmlCommentOpen = []byte("!--")

// next advances to the next tag, recording the character data in front of it.
// Comments and processing instructions are skipped as if they were not there;
// the recorded run is the one after the last skipped construct, which is what
// this scanner has always reported.
//
// The name is a slice of the document, valid as long as the scanner is: the
// callers compare it and switch on it, and handing out the bytes keeps a
// per-tag string off every element of every listing.
func (s *xmlScan) next() (int, []byte, error) {
	textStart := s.i
	for {
		// IndexByte rather than a byte loop: the runs between tags are short,
		// but there are two of them per element of a listing.
		if at := bytes.IndexByte(s.b[s.i:], '<'); at < 0 {
			s.i = len(s.b)
		} else {
			s.i += at
		}
		s.text0, s.text1 = textStart, s.i
		if s.i == len(s.b) {
			return xmlEOF, nil, nil
		}
		s.i++ // consume '<'
		if s.i == len(s.b) {
			return 0, nil, errMalformedXML
		}

		switch s.b[s.i] {
		case '?':
			if !s.skipPast("?>") {
				return 0, nil, errMalformedXML
			}
			textStart = s.i
			continue
		case '!':
			if bytes.HasPrefix(s.b[s.i:], xmlCommentOpen) {
				s.i += 3
				if !s.skipPast("-->") {
					return 0, nil, errMalformedXML
				}
			} else if !s.skipPast(">") {
				return 0, nil, errMalformedXML
			}
			textStart = s.i
			continue
		case '/':
			s.i++
			name := s.readName()
			s.skipSpace()
			if s.i >= len(s.b) || s.b[s.i] != '>' {
				return 0, nil, errMalformedXML
			}
			s.i++
			return xmlClose, name, nil
		}

		name := s.readName()
		if len(name) == 0 {
			return 0, nil, errMalformedXML
		}
		// Skip attributes; '>' inside a quoted value must not end the tag.
		for s.i < len(s.b) {
			switch c := s.b[s.i]; c {
			case '"', '\'':
				s.i++
				for s.i < len(s.b) && s.b[s.i] != c {
					s.i++
				}
				if s.i == len(s.b) {
					return 0, nil, errMalformedXML
				}
				s.i++
			case '>':
				s.i++
				return xmlOpen, name, nil
			case '/':
				if s.i+1 < len(s.b) && s.b[s.i+1] == '>' {
					s.i += 2
					return xmlSelf, name, nil
				}
				s.i++
			default:
				s.i++
			}
		}
		return 0, nil, errMalformedXML
	}
}

// readName reads an element name, dropping any namespace prefix so
// <s3:Contents> and <Contents> read the same, as they do under encoding/xml.
func (s *xmlScan) readName() []byte {
	start := s.i
	colon := -1
	for s.i < len(s.b) {
		c := s.b[s.i]
		if nameEnd[c] {
			break
		}
		if c == ':' {
			colon = s.i
		}
		s.i++
	}
	if colon >= 0 {
		start = colon + 1
	}
	return s.b[start:s.i]
}

// nameEnd marks the bytes that end an element name: a table, because readName
// runs on every tag and a per-byte switch was a quarter of a listing's parse.
var nameEnd = [256]bool{' ': true, '\t': true, '\r': true, '\n': true, '>': true, '/': true}

func (s *xmlScan) skipSpace() {
	for s.i < len(s.b) {
		switch s.b[s.i] {
		case ' ', '\t', '\r', '\n':
			s.i++
		default:
			return
		}
	}
}

func (s *xmlScan) skipPast(delim string) bool {
	// bytes.Index on the live slice: converting the remainder to a string
	// copied the rest of the document on every skip.
	at := bytes.Index(s.b[s.i:], []byte(delim))
	if at < 0 {
		return false
	}
	s.i += at + len(delim)
	return true
}

// root finds the document element and checks its name.
func (s *xmlScan) root(name string) error {
	ev, got, err := s.next()
	if err != nil || ev != xmlOpen || string(got) != name {
		return errMalformedXML
	}
	return nil
}

// textElement consumes the rest of an element just opened and returns its
// character data. Nested markup is flattened, which is what chardata mapping
// under encoding/xml did.
func (s *xmlScan) textElement(name []byte) (string, error) {
	var out string
	depth := 0
	for {
		ev, got, err := s.next()
		if err != nil {
			return "", err
		}
		out += s.text()
		switch ev {
		case xmlOpen:
			depth++
		case xmlClose:
			if depth == 0 {
				if !bytes.Equal(got, name) {
					return "", errMalformedXML
				}
				return out, nil
			}
			depth--
		case xmlEOF:
			return "", errMalformedXML
		}
	}
}

// textBytes consumes the rest of an element just opened and returns its
// character data as a slice of the document, for the fields that are parsed
// rather than kept: a size, a date. It reports false when the text needs
// unescaping or spans nested markup, and textElement is the general path.
// The slice is valid as long as the document is.
func (s *xmlScan) textBytes(name []byte) ([]byte, bool, error) {
	ev, got, err := s.next()
	if err != nil {
		return nil, false, err
	}
	if ev != xmlClose || !bytes.Equal(got, name) {
		return nil, false, nil
	}
	text := s.b[s.text0:s.text1]
	if bytes.IndexByte(text, '&') >= 0 {
		return nil, false, nil
	}
	return text, true, nil
}

// skipElement consumes the rest of an element just opened, contents included.
func (s *xmlScan) skipElement(name []byte) error {
	depth := 0
	for {
		ev, got, err := s.next()
		if err != nil {
			return err
		}
		switch ev {
		case xmlOpen:
			depth++
		case xmlClose:
			if depth == 0 {
				if !bytes.Equal(got, name) {
					return errMalformedXML
				}
				return nil
			}
			depth--
		case xmlEOF:
			return errMalformedXML
		}
	}
}

// xmlUnescape resolves the predefined entities and numeric character
// references. An unrecognized entity is kept literally rather than failing the
// document, which is the lenient end of what servers actually send.
func xmlUnescape(b []byte) string {
	amp := -1
	for i, c := range b {
		if c == '&' {
			amp = i
			break
		}
	}
	if amp < 0 {
		return string(b)
	}

	var out strings.Builder
	out.Grow(len(b))
	out.Write(b[:amp])
	for i := amp; i < len(b); {
		c := b[i]
		if c != '&' {
			out.WriteByte(c)
			i++
			continue
		}
		semi := -1
		// An entity reference is short; cap the scan so a stray '&' does not
		// pair with a ';' half a document away.
		for j := i + 1; j < len(b) && j <= i+10; j++ {
			if b[j] == ';' {
				semi = j
				break
			}
		}
		if semi < 0 {
			out.WriteByte(c)
			i++
			continue
		}
		entity := string(b[i+1 : semi])
		switch {
		case entity == "amp":
			out.WriteByte('&')
		case entity == "lt":
			out.WriteByte('<')
		case entity == "gt":
			out.WriteByte('>')
		case entity == "quot":
			out.WriteByte('"')
		case entity == "apos":
			out.WriteByte('\'')
		case strings.HasPrefix(entity, "#"):
			digits := entity[1:]
			base := 10
			if strings.HasPrefix(digits, "x") || strings.HasPrefix(digits, "X") {
				digits, base = digits[1:], 16
			}
			r, err := strconv.ParseUint(digits, base, 32)
			if err != nil {
				out.WriteByte('&')
				i++
				continue
			}
			out.WriteRune(rune(r))
		default:
			out.WriteByte('&')
			i++
			continue
		}
		i = semi + 1
	}
	return out.String()
}

// xmlEscapeText escapes character data for the one document this package
// writes.
func xmlEscapeText(s string) string {
	if !strings.ContainsAny(s, "&<>") {
		return s
	}
	var out strings.Builder
	out.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '&':
			out.WriteString("&amp;")
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// parseListBucketResult reads the ListObjectsV2 response document.
func parseListBucketResult(body []byte) (*ListResult, error) {
	s := &xmlScan{b: body}
	if err := s.root("ListBucketResult"); err != nil {
		return nil, err
	}
	result := &ListResult{}
	for {
		ev, name, err := s.next()
		if err != nil {
			return nil, err
		}
		switch ev {
		case xmlEOF:
			return nil, errMalformedXML
		case xmlSelf:
			// An empty optional element, <NextContinuationToken/> style.
		case xmlClose:
			if string(name) != "ListBucketResult" {
				return nil, errMalformedXML
			}
			return result, nil
		case xmlOpen:
			switch string(name) {
			case "IsTruncated":
				text, err := s.textElement(name)
				if err != nil {
					return nil, err
				}
				truncated, err := strconv.ParseBool(strings.TrimSpace(text))
				if err != nil {
					return nil, errMalformedXML
				}
				result.IsTruncated = truncated
			case "NextContinuationToken":
				if result.NextToken, err = s.textElement(name); err != nil {
					return nil, err
				}
			case "Contents":
				info, err := parseContents(s)
				if err != nil {
					return nil, err
				}
				result.Objects = append(result.Objects, info)
			case "CommonPrefixes":
				prefix, err := parseCommonPrefixes(s)
				if err != nil {
					return nil, err
				}
				result.CommonPrefixes = append(result.CommonPrefixes, prefix)
			default:
				if err := s.skipElement(name); err != nil {
					return nil, err
				}
			}
		}
	}
}

func parseContents(s *xmlScan) (ObjectInfo, error) {
	var info ObjectInfo
	for {
		ev, name, err := s.next()
		if err != nil {
			return info, err
		}
		switch ev {
		case xmlEOF:
			return info, errMalformedXML
		case xmlSelf:
		case xmlClose:
			if string(name) != "Contents" {
				return info, errMalformedXML
			}
			return info, nil
		case xmlOpen:
			switch string(name) {
			case "Key":
				if info.Key, err = s.textElement(name); err != nil {
					return info, err
				}
			case "ETag":
				if info.ETag, err = s.textElement(name); err != nil {
					return info, err
				}
			case "Size":
				text, err := s.textValue(name)
				if err != nil {
					return info, err
				}
				size, ok := parseDecimal(text)
				if !ok {
					return info, errMalformedXML
				}
				info.Size = size
			case "LastModified":
				text, err := s.textValue(name)
				if err != nil {
					return info, err
				}
				// RFC 3339 with the fractional seconds S3 includes. The form S3
				// and the compatible servers send is parsed directly; anything
				// else goes to time.Parse, which accepts the fraction whether or
				// not the layout names it.
				when, ok := parseRFC3339UTC(text)
				if !ok {
					if when, err = time.Parse(time.RFC3339, string(text)); err != nil {
						return info, errMalformedXML
					}
				}
				info.LastModified = when
			default:
				// StorageClass, Owner and whatever else: consumed, not kept, so
				// there is no string to build.
				if err := s.skipElement(name); err != nil {
					return info, err
				}
			}
		}
	}
}

// textValue is textElement for a field that is parsed, not kept: the bytes
// come straight from the document when they can, and the unescaped string is
// built only when they cannot. The surrounding space is dropped either way.
func (s *xmlScan) textValue(name []byte) ([]byte, error) {
	if text, ok, err := s.textBytes(name); err != nil || ok {
		return bytes.TrimSpace(text), err
	}
	// textBytes consumed one event; the element is still open after a tag
	// or an entity, so textElement cannot resume. Rewind to the element's
	// content and take the general path.
	s.i = s.text0
	text, err := s.textElement(name)
	return []byte(strings.TrimSpace(text)), err
}

// parseDecimal reads a non-negative decimal integer, which is what every
// numeric field in these documents is.
func parseDecimal(b []byte) (int64, bool) {
	if len(b) == 0 || len(b) > 18 {
		return 0, false
	}
	var n int64
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}

// parseRFC3339UTC reads "2026-08-01T12:30:05Z" and the same with a fraction
// of a second, "2026-08-01T12:30:05.000Z", which is the form S3 sends. It
// reports false for any other spelling, including offsets, leaving those to
// time.Parse.
func parseRFC3339UTC(b []byte) (time.Time, bool) {
	if len(b) < 20 || b[4] != '-' || b[7] != '-' || b[10] != 'T' || b[13] != ':' || b[16] != ':' || b[len(b)-1] != 'Z' {
		return time.Time{}, false
	}
	field := func(at, width int) (int, bool) {
		n := 0
		for _, c := range b[at : at+width] {
			if c < '0' || c > '9' {
				return 0, false
			}
			n = n*10 + int(c-'0')
		}
		return n, true
	}
	year, ok1 := field(0, 4)
	month, ok2 := field(5, 2)
	day, ok3 := field(8, 2)
	hour, ok4 := field(11, 2)
	minute, ok5 := field(14, 2)
	second, ok6 := field(17, 2)
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) {
		return time.Time{}, false
	}
	nanos := 0
	if frac := b[19 : len(b)-1]; len(frac) != 0 {
		if frac[0] != '.' || len(frac) == 1 || len(frac) > 10 {
			return time.Time{}, false
		}
		scale := 1000000000
		for _, c := range frac[1:] {
			if c < '0' || c > '9' {
				return time.Time{}, false
			}
			scale /= 10
			nanos += int(c-'0') * scale
		}
	}
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, hour, minute, second, nanos, time.UTC)
	// time.Date normalizes an impossible day such as February 30 into March;
	// time.Parse rejects it, so this does too.
	if t.Day() != day {
		return time.Time{}, false
	}
	return t, true
}

func parseCommonPrefixes(s *xmlScan) (string, error) {
	var prefix string
	for {
		ev, name, err := s.next()
		if err != nil {
			return "", err
		}
		switch ev {
		case xmlEOF:
			return "", errMalformedXML
		case xmlSelf:
		case xmlClose:
			if string(name) != "CommonPrefixes" {
				return "", errMalformedXML
			}
			return prefix, nil
		case xmlOpen:
			text, err := s.textElement(name)
			if err != nil {
				return "", err
			}
			if string(name) == "Prefix" {
				prefix = text
			}
		}
	}
}

// parseFlatDocument reads a document whose root holds text elements, such as
// InitiateMultipartUploadResult, handing each name and text to visit. An
// unknown element is handed over too, nested markup flattened, and the
// visitor ignores it; that is what keeps an addition from breaking a parse.
func parseFlatDocument(body []byte, root string, visit func(name, text string)) error {
	s := &xmlScan{b: body}
	if err := s.root(root); err != nil {
		return err
	}
	for {
		ev, name, err := s.next()
		if err != nil {
			return err
		}
		switch ev {
		case xmlEOF:
			return errMalformedXML
		case xmlSelf:
		case xmlClose:
			if string(name) != root {
				return errMalformedXML
			}
			return nil
		case xmlOpen:
			text, err := s.textElement(name)
			if err != nil {
				return err
			}
			visit(string(name), text)
		}
	}
}

// parseErrorDocument reads the S3 error document. ok reports whether body was
// one; a false return leaves the caller to fall back to the status code, which
// is the contract xml.Unmarshal's failure used to provide.
func parseErrorDocument(body []byte) (code, message, requestID string, ok bool) {
	s := &xmlScan{b: body}
	if err := s.root("Error"); err != nil {
		return "", "", "", false
	}
	for {
		ev, name, err := s.next()
		if err != nil {
			return "", "", "", false
		}
		switch ev {
		case xmlEOF:
			return "", "", "", false
		case xmlSelf:
		case xmlClose:
			if string(name) != "Error" {
				return "", "", "", false
			}
			return code, message, requestID, true
		case xmlOpen:
			text, err := s.textElement(name)
			if err != nil {
				return "", "", "", false
			}
			switch string(name) {
			case "Code":
				code = text
			case "Message":
				message = text
			case "RequestId":
				requestID = text
			}
		}
	}
}
