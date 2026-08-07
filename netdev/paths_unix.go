//go:build darwin || linux || wasip1

package netdev

func hostsPath() string  { return "/etc/hosts" }
func resolvPath() string { return "/etc/resolv.conf" }
