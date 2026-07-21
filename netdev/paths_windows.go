//go:build windows

package netdev

import "os"

func hostsPath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return root + `\System32\drivers\etc\hosts`
}

func resolvPath() string {
	// Windows has no /etc/resolv.conf; dnsQuery falls back to 8.8.8.8.
	return ""
}
