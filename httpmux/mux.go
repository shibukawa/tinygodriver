//go:build tinygo || force_tinygo_logic

package httpmux

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/shibukawa/tinygodriver/internal/syncx"
)

// ServeMux is an HTTP request multiplexer implementing the Go 1.22+ ServeMux
// routing semantics documented by net/http.
type ServeMux struct {
	mu     syncx.RWMutex
	routes []route

	// trailingSlash records whether any registered pattern can exactly match a
	// path that ends in "/" — one ending in "/", "{name...}", or "{$}". When
	// none can, the redirect probe that re-matches every non-exact request with
	// a slash appended is pure waste, so matchOrRedirect skips it.
	trailingSlash bool
}

type route struct {
	pattern *pattern
	handler http.Handler
}

type pathValue struct {
	name  string
	value string
}

// NewServeMux allocates and returns a new ServeMux.
func NewServeMux() *ServeMux {
	return &ServeMux{}
}

// Handle registers handler for pattern. It panics for an invalid pattern, a
// nil handler, a duplicate pattern, or a conflicting pattern.
func (mux *ServeMux) Handle(pattern string, handler http.Handler) {
	if err := mux.register(pattern, handler); err != nil {
		panic(err)
	}
}

// HandleFunc registers handler for pattern.
func (mux *ServeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	mux.Handle(pattern, http.HandlerFunc(handler))
}

func (mux *ServeMux) register(patternString string, handler http.Handler) error {
	if patternString == "" {
		return errors.New("httpmux: invalid pattern")
	}
	if handler == nil {
		return errors.New("httpmux: nil handler")
	}
	if f, ok := handler.(http.HandlerFunc); ok && f == nil {
		return errors.New("httpmux: nil handler")
	}
	p, err := parsePattern(patternString)
	if err != nil {
		return fmt.Errorf("httpmux: parsing %q: %w", patternString, err)
	}

	mux.mu.Lock()
	defer mux.mu.Unlock()
	for _, registered := range mux.routes {
		if p.conflictsWith(registered.pattern) {
			return fmt.Errorf("httpmux: pattern %q conflicts with pattern %q", p.str, registered.pattern.str)
		}
	}
	mux.routes = append(mux.routes, route{pattern: p, handler: handler})
	if last := p.lastSegment(); last.multi || last.s == "/" {
		mux.trailingSlash = true
	}
	return nil
}

// Handler returns the handler and registered pattern that match r. Redirect,
// not-found, and method-not-allowed handlers have an empty pattern except that
// a trailing-slash redirect returns the pattern it redirects toward.
// Handler does not populate r.PathValue values.
func (mux *ServeMux) Handler(r *http.Request) (http.Handler, string) {
	h, patternString, _, _ := mux.findHandler(r)
	return h, patternString
}

// ServeHTTP dispatches r to the most specific matching handler.
func (mux *ServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.RequestURI == "*" {
		if r.ProtoAtLeast(1, 1) {
			w.Header().Set("Connection", "close")
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	h, _, _, values := mux.findHandler(r)
	for _, value := range values {
		r.SetPathValue(value.name, value.value)
	}
	h.ServeHTTP(w, r)
}

func (mux *ServeMux) findHandler(r *http.Request) (http.Handler, string, *pattern, []pathValue) {
	host := r.URL.Host
	escapedPath := r.URL.EscapedPath()
	requestPath := escapedPath

	if r.Method == "CONNECT" {
		_, _, redirectTo := mux.matchOrRedirect(host, r.Method, requestPath, r.URL)
		if redirectTo != nil {
			return http.RedirectHandler(redirectTo.String(), http.StatusTemporaryRedirect), redirectTo.Path, nil, nil
		}
		matched, values, _ := mux.matchOrRedirect(r.Host, r.Method, requestPath, nil)
		return mux.finishMatch(matched, values, r.Host, requestPath)
	}

	host = stripHostPort(r.Host)
	requestPath = cleanPath(requestPath)
	matched, values, redirectTo := mux.matchOrRedirect(host, r.Method, requestPath, r.URL)
	if redirectTo != nil {
		return http.RedirectHandler(redirectTo.String(), http.StatusTemporaryRedirect), matched.pattern.str, nil, nil
	}
	if requestPath != escapedPath {
		patternString := ""
		if matched != nil {
			patternString = matched.pattern.str
		}
		redirectTo := urlFromEscaped(requestPath, r.URL.RawQuery)
		return http.RedirectHandler(redirectTo.String(), http.StatusTemporaryRedirect), patternString, nil, nil
	}
	return mux.finishMatch(matched, values, host, requestPath)
}

func (mux *ServeMux) finishMatch(matched *route, values []pathValue, host, requestPath string) (http.Handler, string, *pattern, []pathValue) {
	if matched != nil {
		return matched.handler, matched.pattern.str, matched.pattern, values
	}
	methods := mux.matchingMethods(host, requestPath)
	if len(methods) != 0 {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Allow", strings.Join(methods, ", "))
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		}), "", nil, nil
	}
	return http.NotFoundHandler(), "", nil, nil
}

func (mux *ServeMux) matchOrRedirect(host, method, requestPath string, u *url.URL) (*route, []pathValue, *url.URL) {
	mux.mu.RLock()
	defer mux.mu.RUnlock()

	split := splitPath(requestPath)
	matched, values := bestMatch(mux.routes, host, method, split)
	if mux.trailingSlash && !exactMatch(matched, requestPath) && u != nil && requestPath != "" && !strings.HasSuffix(requestPath, "/") {
		withSlash := requestPath + "/"
		redirectMatch, _ := bestMatch(mux.routes, host, method, splitPath(withSlash))
		if exactMatch(redirectMatch, withSlash) {
			return redirectMatch, nil, urlFromEscaped(withSlash, u.RawQuery)
		}
	}
	return matched, values, nil
}

// splitRequestPath is a request path cut into the segments matchPath consumes.
// Splitting and unescaping once per request replaces doing both once per
// candidate route.
type splitRequestPath struct {
	// segs holds the unescaped segment values in firstSegment order, with a
	// final "/" entry for a path that ends in one.
	segs []string
	// rawTails[i] is the still-escaped remainder of the path from segment i
	// on, which is what a {name...} wildcard captures.
	rawTails []string
}

func splitPath(requestPath string) splitRequestPath {
	var split splitRequestPath
	if requestPath == "" {
		return split
	}
	// Each iteration consumes one "/" from the front, so counting them sizes
	// both slices in one allocation apiece instead of growing through append.
	n := strings.Count(requestPath, "/") + 1
	split.segs = make([]string, 0, n)
	split.rawTails = make([]string, 0, n)
	rest := requestPath
	for rest != "" {
		split.rawTails = append(split.rawTails, rest)
		value, remaining := firstSegment(rest)
		split.segs = append(split.segs, value)
		rest = remaining
	}
	return split
}

func bestMatch(routes []route, host, method string, split splitRequestPath) (*route, []pathValue) {
	var best *route
	for i := range routes {
		candidate := &routes[i]
		if candidate.pattern.host != "" && candidate.pattern.host != host {
			continue
		}
		if !matchesMethod(candidate.pattern.method, method) {
			continue
		}
		if !matchesPath(candidate.pattern, split) {
			continue
		}
		if best == nil || isMoreSpecific(candidate.pattern, best.pattern) {
			best = candidate
		}
	}
	if best == nil {
		return nil, nil
	}
	// Values are extracted only for the winner, so a candidate that matches
	// and then loses on specificity never allocates a values slice that gets
	// thrown away.
	return best, pathValues(best.pattern, split)
}

func isMoreSpecific(candidate, current *pattern) bool {
	if candidate.host != current.host {
		return candidate.host != ""
	}
	return candidate.comparePathsAndMethods(current) == moreSpecific
}

func matchesMethod(patternMethod, requestMethod string) bool {
	return patternMethod == "" || patternMethod == requestMethod || (patternMethod == "GET" && requestMethod == "HEAD")
}

// matchesPath reports whether p matches split without extracting wildcard
// values, so probing a candidate costs no allocation.
func matchesPath(p *pattern, split splitRequestPath) bool {
	i := 0
	for _, seg := range p.segments {
		if i >= len(split.segs) {
			return false
		}
		if seg.multi {
			return true
		}
		value := split.segs[i]
		if seg.wild {
			if value == "/" {
				return false
			}
		} else if value != seg.s {
			return false
		}
		i++
	}
	return i == len(split.segs)
}

// pathValues extracts the wildcard values of a pattern matchesPath already
// accepted. The slice is sized exactly and freshly allocated, so it never
// aliases scratch state a later match could clobber.
func pathValues(p *pattern, split splitRequestPath) []pathValue {
	n := 0
	for _, seg := range p.segments {
		if seg.wild && seg.s != "" {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	values := make([]pathValue, 0, n)
	i := 0
	for _, seg := range p.segments {
		if seg.multi {
			if seg.s != "" {
				values = append(values, pathValue{name: seg.s, value: pathUnescape(split.rawTails[i][1:])})
			}
			return values
		}
		if seg.wild {
			values = append(values, pathValue{name: seg.s, value: split.segs[i]})
		}
		i++
	}
	return values
}

func firstSegment(requestPath string) (string, string) {
	if requestPath == "/" {
		return "/", ""
	}
	withoutSlash := requestPath[1:]
	i := strings.IndexByte(withoutSlash, '/')
	if i < 0 {
		i = len(withoutSlash)
	}
	return pathUnescape(withoutSlash[:i]), withoutSlash[i:]
}

func exactMatch(matched *route, requestPath string) bool {
	if matched == nil {
		return false
	}
	if !matched.pattern.lastSegment().multi {
		return true
	}
	if requestPath != "" && requestPath[len(requestPath)-1] != '/' {
		return false
	}
	return len(matched.pattern.segments) == strings.Count(requestPath, "/")
}

// matchingMethods gathers the methods that could have served requestPath. It
// runs once per would-be 405, so the handful of distinct methods deduplicates
// through a linear scan of a small slice rather than a map built and thrown
// away per response.
func (mux *ServeMux) matchingMethods(host, requestPath string) []string {
	mux.mu.RLock()
	defer mux.mu.RUnlock()

	var methods []string
	methods = collectMethods(mux.routes, host, splitPath(requestPath), methods)
	if !strings.HasSuffix(requestPath, "/") {
		methods = collectMethods(mux.routes, host, splitPath(requestPath+"/"), methods)
	}
	if containsString(methods, "GET") && !containsString(methods, "HEAD") {
		methods = append(methods, "HEAD")
	}
	sort.Strings(methods)
	return methods
}

func collectMethods(routes []route, host string, split splitRequestPath, methods []string) []string {
	for i := range routes {
		p := routes[i].pattern
		if p.method == "" || (p.host != "" && p.host != host) {
			continue
		}
		if !matchesPath(p, split) {
			continue
		}
		if !containsString(methods, p.method) {
			methods = append(methods, p.method)
		}
	}
	return methods
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// urlFromEscaped builds a redirect target from an escaped path, keeping Path
// and RawPath in sync the way net/http does. Filling in Path alone makes
// URL.String either escape an already escaped path a second time ("%2f" ->
// "%252f") or drop the escaping altogether, and either one sends the client to
// a path that is no longer the one it asked for.
func urlFromEscaped(escaped, rawQuery string) *url.URL {
	unescaped, err := url.PathUnescape(escaped)
	if err != nil {
		unescaped = escaped
	}
	return &url.URL{Path: unescaped, RawPath: escaped, RawQuery: rawQuery}
}

func stripHostPort(hostPort string) string {
	if !strings.Contains(hostPort, ":") {
		return hostPort
	}
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return hostPort
	}
	return host
}
