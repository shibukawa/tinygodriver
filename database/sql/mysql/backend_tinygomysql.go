//go:build tinygo || force_tinygo_logic

package mysql

import (
	"github.com/shibukawa/tinygodriver/database/sql/mysql/tinygomysql"
	"github.com/shibukawa/tinygodriver/https"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "tinygomysql"

// RegisterTLSConfig registers trust settings under a name usable as tls=<name>
// in a DSN. This backend performs TLS through the OS stack, which takes the PEM
// bytes in cfg directly.
func RegisterTLSConfig(name string, cfg *https.Config) error {
	return tinygomysql.RegisterTLSConfig(name, cfg)
}

// DeregisterTLSConfig removes a configuration registered by RegisterTLSConfig.
func DeregisterTLSConfig(name string) { tinygomysql.DeregisterTLSConfig(name) }
