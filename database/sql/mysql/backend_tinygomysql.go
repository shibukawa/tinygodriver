//go:build tinygo || force_tinygo_logic

package mysql

import (
	"database/sql/driver"

	"github.com/shibukawa/tinygodriver/database/sql/mysql/tinygomysql"
	"github.com/shibukawa/tinygodriver/https"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "tinygomysql"

// driverInstance identifies this backend's driver for sqlbatch.Register, which
// keys on the driver's type. It is the value this backend registers with
// database/sql, so every handle Open returns finds the adapter.
func driverInstance() driver.Driver { return &tinygomysql.MySQLDriver{} }

// RegisterTLSConfig registers trust settings under a name usable as tls=<name>
// in a DSN. This backend performs TLS through the OS stack, which takes the PEM
// bytes in cfg directly.
func RegisterTLSConfig(name string, cfg *https.Config) error {
	return tinygomysql.RegisterTLSConfig(name, cfg)
}

// DeregisterTLSConfig removes a configuration registered by RegisterTLSConfig.
func DeregisterTLSConfig(name string) { tinygomysql.DeregisterTLSConfig(name) }
