//go:build tinygo

package netdev

import _ "unsafe"

//go:linkname useNetdev net.useNetdev
func useNetdev(dev netdever)

func init() {
	Use(New())
}
