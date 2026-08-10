package pgconn

import (
	"reflect"
	"slices"
	"testing"

	"github.com/shibukawa/tinygodriver/https"
)

func TestHTTPSConfigAdapter(t *testing.T) {
	pem := []byte("pem")
	in := &TLSConfig{
		Host:               "db.example",
		ServerName:         "verify.example",
		RootCAsPEM:         [][]byte{pem},
		RootCAsOnly:        true,
		InsecureSkipVerify: true,
	}
	out := toHTTPSConfig(in)
	if out.ServerName != in.ServerName || out.RootCAsOnly != in.RootCAsOnly ||
		out.InsecureSkipVerify != in.InsecureSkipVerify ||
		len(out.RootCAs) != 1 || !slices.Equal(out.RootCAs[0], pem) {
		t.Fatalf("adapter output = %#v, input = %#v", out, in)
	}
	if out.Certificates != nil || out.MinVersion != 0 {
		t.Fatal("adapter populated intentionally unsupported HTTPS fields")
	}
}

func TestHTTPSConfigAdapterFieldInventory(t *testing.T) {
	assertExportedFields(t, reflect.TypeFor[TLSConfig](), []string{
		"Host", "ServerName", "RootCAsPEM", "RootCAsOnly", "InsecureSkipVerify",
	})
	assertExportedFields(t, reflect.TypeFor[https.Config](), []string{
		"RootCAs", "RootCAsOnly", "Certificates", "InsecureSkipVerify", "ServerName", "MinVersion",
	})
}

func assertExportedFields(t *testing.T, typ reflect.Type, want []string) {
	t.Helper()
	var got []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.IsExported() {
			got = append(got, field.Name)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s exported fields = %v, want %v; update the adapter and inventory", typ, got, want)
	}
}
