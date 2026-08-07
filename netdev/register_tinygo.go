//go:build tinygo && !wasip1 && !wasip2

package netdev

import _ "unsafe"

//go:linkname useNetdev net.useNetdev
func useNetdev(dev netdever)

func init() {
	Use(New())
}
