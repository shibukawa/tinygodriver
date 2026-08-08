package s3

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// A hand-rolled reader for the two XML documents this package consumes, and a
// renderer for the one it produces. encoding/xml handles arbitrary documents
// through reflection, which is a large dependency for a TinyGo binary that
// only ever sees ListBucketResult and Error; both are flat, fixed schemas.
//
// The subset understood here: elements with attributes, character data with
// the five predefined entities plus numeric references, comments, and
// processing instructions. CDATA sections and DTDs are not, because S3 and the
// S3-compatible servers do not emit them for these documents.

var errMalformedXML = errors.New("s3: malformed XML document")

// xmlScan walks a document tag by tag. Text preceding the most recent tag is
// kept in text, already unescaped.
type xmlScan struct {
	b    []byte
	i    int
	text string
}

// Tag events. Self-closing tags are reported as xmlSelf rather than a
// synthetic open/close pair, so consumers see exactly what the bytes say.
const (
	xmlOpen = iota
	xmlSelf
	xmlClose
	xmlEOF
)

// next advances to the next tag, capturing the character data in front of it.
// Comments and processing instructions are skipped as if they were not there.
func (s *xmlScan) next() (int, string, error) {
	textStart := s.i
	for {
		start := s.i
		for s.i < len(s.b) && s.b[s.i] != '<' {
			s.i++
		}
		if start == textStart {
			s.text = xmlUnescape(s.b[textStart:s.i])
		} else {
			// Text resumed after a skipped comment or PI; append the new run.
			s.text += xmlUnescape(s.b[start:s.i])
		}
		if s.i == len(s.b) {
			return xmlEOF, "", nil
		}
		s.i++ // consume '<'
		if s.i == len(s.b) {
			return 0, "", errMalformedXML
		}

		switch s.b[s.i] {
		case '?':
			if !s.skipPast("?>") {
				return 0, "", errMalformedXML
			}
			textStart = s.i
			continue
		case '!':
			if strings.HasPrefix(string(s.b[s.i:]), "!--") {
				s.i += 3
				if !s.skipPast("-->") {
					return 0, "", errMalformedXML
				}
			} else if !s.skipPast(">") {
				return 0, "", errMalformedXML
			}
			textStart = s.i
			continue
		case '/':
			s.i++
			name := s.readName()
			s.skipSpace()
			if s.i >= len(s.b) || s.b[s.i] != '>' {
				return 0, "", errMalformedXML
			}
			s.i++
			return xmlClose, name, nil
		}

		name := s.readName()
		if name == "" {
			return 0, "", errMalformedXML
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
					return 0, "", errMalformedXML
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
		return 0, "", errMalformedXML
	}
}

// readName reads an element name, dropping any namespace prefix so
// <s3:Contents> and <Contents> read the same, as they do under encoding/xml.
func (s *xmlScan) readName() string {
	start := s.i
	for s.i < len(s.b) {
		switch s.b[s.i] {
		case ' ', '\t', '\r', '\n', '>', '/':
			goto done
		}
		s.i++
	}
done:
	name := string(s.b[start:s.i])
	if colon := strings.IndexByte(name, ':'); colon >= 0 {
		name = name[colon+1:]
	}
	return name
}

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
	at := strings.Index(string(s.b[s.i:]), delim)
	if at < 0 {
		return false
	}
	s.i += at + len(delim)
	return true
}

// root finds the document element and checks its name.
func (s *xmlScan) root(name string) error {
	ev, got, err := s.next()
	if err != nil || ev != xmlOpen || got != name {
		return errMalformedXML
	}
	return nil
}

// textElement consumes the rest of an element just opened and returns its
// character data. Nested markup is flattened, which is what chardata mapping
// under encoding/xml did.
func (s *xmlScan) textElement(name string) (string, error) {
	var out string
	depth := 0
	for {
		ev, got, err := s.next()
		if err != nil {
			return "", err
		}
		out += s.text
		switch ev {
		case xmlOpen:
			depth++
		case xmlClose:
			if depth == 0 {
				if got != name {
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

// skipElement consumes the rest of an element just opened, contents included.
func (s *xmlScan) skipElement(name string) error {
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
				if got != name {
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
			if name != "ListBucketResult" {
				return nil, errMalformedXML
			}
			return result, nil
		case xmlOpen:
			switch name {
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
			if name != "Contents" {
				return info, errMalformedXML
			}
			return info, nil
		case xmlOpen:
			text, err := s.textElement(name)
			if err != nil {
				return info, err
			}
			switch name {
			case "Key":
				info.Key = text
			case "ETag":
				info.ETag = text
			case "Size":
				size, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
				if err != nil {
					return info, errMalformedXML
				}
				info.Size = size
			case "LastModified":
				// RFC 3339 with the fractional seconds S3 includes; Parse
				// accepts the fraction whether or not the layout names it.
				when, err := time.Parse(time.RFC3339, strings.TrimSpace(text))
				if err != nil {
					return info, errMalformedXML
				}
				info.LastModified = when
			}
		}
	}
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
			if name != "CommonPrefixes" {
				return "", errMalformedXML
			}
			return prefix, nil
		case xmlOpen:
			text, err := s.textElement(name)
			if err != nil {
				return "", err
			}
			if name == "Prefix" {
				prefix = text
			}
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
			if name != "Error" {
				return "", "", "", false
			}
			return code, message, requestID, true
		case xmlOpen:
			text, err := s.textElement(name)
			if err != nil {
				return "", "", "", false
			}
			switch name {
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
