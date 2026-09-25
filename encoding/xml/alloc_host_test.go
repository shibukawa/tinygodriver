//go:build !tinygo

package xml

// The Go compiler inlines the iterator at the call site and keeps the loop
// body's closure on the stack, so a range loop costs what the explicit
// loop costs.
var iterBudget = struct{ tokens, children, decodeLoop uint64 }{0, 0, 0}
