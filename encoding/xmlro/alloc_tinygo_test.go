//go:build tinygo

package xmlro

// TinyGo 0.42 allocates the closure contexts of a range-over-func loop on
// the heap: the iterator's own and the loop body's, so the cost grows with
// what the body captures. Measured per loop, not per iteration: 32 bytes
// for a Tokens loop with an empty body, 80 for a Children loop with an
// empty body, and 208 for a Tokens loop holding a Children loop whose body
// decodes a cell and can fail the test. The explicit calls the iterators
// wrap allocate nothing.
var iterBudget = struct{ tokens, children, decodeLoop uint64 }{32, 80, 208}
