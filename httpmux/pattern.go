// Portions Copyright 2023 The Go Authors. See LICENSE for terms.

//go:build tinygo || force_tinygo_logic

package httpmux

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"
)

type pattern struct {
	str      string
	method   string
	host     string
	segments []segment
}

type segment struct {
	s     string
	wild  bool
	multi bool
}

func (p *pattern) lastSegment() segment {
	return p.segments[len(p.segments)-1]
}

func parsePattern(s string) (_ *pattern, err error) {
	if s == "" {
		return nil, errors.New("empty pattern")
	}
	off := 0
	defer func() {
		if err != nil {
			err = fmt.Errorf("at offset %d: %w", off, err)
		}
	}()

	method, rest, found := s, "", false
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		method, rest, found = s[:i], strings.TrimLeft(s[i+1:], " \t"), true
	}
	if !found {
		rest = method
		method = ""
	}
	if method != "" && !validMethod(method) {
		return nil, fmt.Errorf("invalid method %q", method)
	}
	p := &pattern{str: s, method: method}
	if found {
		off = len(method) + 1
	}

	i := strings.IndexByte(rest, '/')
	if i < 0 {
		return nil, errors.New("host/path missing /")
	}
	p.host = rest[:i]
	rest = rest[i:]
	if j := strings.IndexByte(p.host, '{'); j >= 0 {
		off += j
		return nil, errors.New("host contains '{' (missing initial '/'?)")
	}
	off += i
	if method != "" && method != "CONNECT" && rest != cleanPath(rest) {
		return nil, errors.New("non-CONNECT pattern with unclean path can never match")
	}

	seenNames := map[string]bool{}
	for len(rest) > 0 {
		rest = rest[1:]
		off = len(s) - len(rest)
		if rest == "" {
			p.segments = append(p.segments, segment{wild: true, multi: true})
			break
		}
		i := strings.IndexByte(rest, '/')
		if i < 0 {
			i = len(rest)
		}
		seg, remaining := rest[:i], rest[i:]
		rest = remaining
		wildcardStart := strings.IndexByte(seg, '{')
		if wildcardStart < 0 {
			p.segments = append(p.segments, segment{s: pathUnescape(seg)})
			continue
		}
		if wildcardStart != 0 {
			return nil, errors.New("bad wildcard segment (must start with '{')")
		}
		if seg[len(seg)-1] != '}' {
			return nil, errors.New("bad wildcard segment (must end with '}')")
		}
		name := seg[1 : len(seg)-1]
		if name == "$" {
			if rest != "" {
				return nil, errors.New("{$} not at end")
			}
			p.segments = append(p.segments, segment{s: "/"})
			break
		}
		name, multi := strings.CutSuffix(name, "...")
		if multi && rest != "" {
			return nil, errors.New("{...} wildcard not at end")
		}
		if name == "" {
			return nil, errors.New("empty wildcard")
		}
		if !isValidWildcardName(name) {
			return nil, fmt.Errorf("bad wildcard name %q", name)
		}
		if seenNames[name] {
			return nil, fmt.Errorf("duplicate wildcard name %q", name)
		}
		seenNames[name] = true
		p.segments = append(p.segments, segment{s: name, wild: true, multi: multi})
	}
	return p, nil
}

func validMethod(method string) bool {
	if method == "" {
		return false
	}
	for i := 0; i < len(method); i++ {
		c := method[i]
		if c <= ' ' || c >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={}", rune(c)) {
			return false
		}
	}
	return true
}

func isValidWildcardName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if !unicode.IsLetter(c) && c != '_' && (i == 0 || !unicode.IsDigit(c)) {
			return false
		}
	}
	return true
}

func pathUnescape(s string) string {
	u, err := url.PathUnescape(s)
	if err != nil {
		return s
	}
	return u
}

func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	np := path.Clean(p)
	if p[len(p)-1] == '/' && np != "/" {
		if len(p) == len(np)+1 && strings.HasPrefix(p, np) {
			return p
		}
		np += "/"
	}
	return np
}

type relationship uint8

const (
	equivalent relationship = iota
	moreGeneral
	moreSpecific
	disjoint
	overlaps
)

func (p1 *pattern) conflictsWith(p2 *pattern) bool {
	if p1.host != p2.host {
		return false
	}
	rel := p1.comparePathsAndMethods(p2)
	return rel == equivalent || rel == overlaps
}

func (p1 *pattern) comparePathsAndMethods(p2 *pattern) relationship {
	mrel := p1.compareMethods(p2)
	if mrel == disjoint {
		return disjoint
	}
	return combineRelationships(mrel, p1.comparePaths(p2))
}

func (p1 *pattern) compareMethods(p2 *pattern) relationship {
	if p1.method == p2.method {
		return equivalent
	}
	if p1.method == "" {
		return moreGeneral
	}
	if p2.method == "" {
		return moreSpecific
	}
	if p1.method == "GET" && p2.method == "HEAD" {
		return moreGeneral
	}
	if p2.method == "GET" && p1.method == "HEAD" {
		return moreSpecific
	}
	return disjoint
}

func (p1 *pattern) comparePaths(p2 *pattern) relationship {
	if len(p1.segments) != len(p2.segments) && !p1.lastSegment().multi && !p2.lastSegment().multi {
		return disjoint
	}
	rel := equivalent
	i := 0
	for ; i < len(p1.segments) && i < len(p2.segments); i++ {
		rel = combineRelationships(rel, compareSegments(p1.segments[i], p2.segments[i]))
		if rel == disjoint {
			return rel
		}
	}
	if i == len(p1.segments) && i == len(p2.segments) {
		return rel
	}
	if i == len(p1.segments) && p1.lastSegment().multi {
		return combineRelationships(rel, moreGeneral)
	}
	if i == len(p2.segments) && p2.lastSegment().multi {
		return combineRelationships(rel, moreSpecific)
	}
	return disjoint
}

func compareSegments(s1, s2 segment) relationship {
	if s1.multi && s2.multi {
		return equivalent
	}
	if s1.multi {
		return moreGeneral
	}
	if s2.multi {
		return moreSpecific
	}
	if s1.wild && s2.wild {
		return equivalent
	}
	if s1.wild {
		if s2.s == "/" {
			return disjoint
		}
		return moreGeneral
	}
	if s2.wild {
		if s1.s == "/" {
			return disjoint
		}
		return moreSpecific
	}
	if s1.s == s2.s {
		return equivalent
	}
	return disjoint
}

func combineRelationships(r1, r2 relationship) relationship {
	switch r1 {
	case equivalent:
		return r2
	case disjoint:
		return disjoint
	case overlaps:
		if r2 == disjoint {
			return disjoint
		}
		return overlaps
	case moreGeneral, moreSpecific:
		switch r2 {
		case equivalent:
			return r1
		case inverseRelationship(r1):
			return overlaps
		default:
			return r2
		}
	default:
		panic("unknown pattern relationship")
	}
}

func inverseRelationship(r relationship) relationship {
	if r == moreSpecific {
		return moreGeneral
	}
	if r == moreGeneral {
		return moreSpecific
	}
	return r
}
