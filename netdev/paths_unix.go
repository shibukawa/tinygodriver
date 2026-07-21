//go:build darwin || linux

package netdev

func hostsPath() string  { return "/etc/hosts" }
func resolvPath() string { return "/etc/resolv.conf" }
