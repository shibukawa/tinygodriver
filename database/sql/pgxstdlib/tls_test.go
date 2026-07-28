//go:build !tinygo

package pgxstdlib

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// TLS tests need a PostgreSQL with ssl=on and the CA that signed its
// certificate:
//
//	openssl req -new -x509 -days 365 -nodes -text -out server.crt \
//	    -keyout server.key -subj "/CN=localhost" \
//	    -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"
//	chmod 600 server.key
//	docker run -d --name pgtls -e POSTGRES_PASSWORD=pass -e POSTGRES_USER=user \
//	    -e POSTGRES_DB=db -p 55433:5432 -v "$PWD":/certs:ro postgres:17 \
//	    -c ssl=on -c ssl_cert_file=/certs/server.crt -c ssl_key_file=/certs/server.key
//
//	PGXSTDLIB_TLS_DSN='postgres://user:pass@localhost:55433/db' \
//	PGXSTDLIB_TLS_CA=$PWD/server.crt go test ./database/sql/pgxstdlib/
func tlsEnv(t *testing.T) (dsn, ca string) {
	t.Helper()
	dsn = os.Getenv("PGXSTDLIB_TLS_DSN")
	ca = os.Getenv("PGXSTDLIB_TLS_CA")
	if dsn == "" || ca == "" {
		t.Skip("set PGXSTDLIB_TLS_DSN and PGXSTDLIB_TLS_CA to run TLS tests")
	}
	return dsn, ca
}

// openTLS builds a DSN with the given sslmode and, when asked, the test CA.
func openTLS(t *testing.T, mode string, withCA bool) (*testDB, error) {
	t.Helper()
	dsn, ca := tlsEnv(t)
	full := withSSLMode(t, dsn, mode)
	if withCA {
		full += "&sslrootcert=" + ca
	}
	db, err := Open(full)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &testDB{db, t}, nil
}

type testDB struct {
	*sql.DB
	t *testing.T
}

// The query has to actually run over the encrypted connection; a handshake
// alone would not prove the session survives the upgrade.
func (d *testDB) assertEncrypted() {
	d.t.Helper()
	defer d.Close()

	var one int
	if err := d.QueryRow("SELECT 1").Scan(&one); err != nil {
		d.t.Fatalf("query over TLS: %v", err)
	}
	if one != 1 {
		d.t.Fatalf("got %d, want 1", one)
	}

	// pg_stat_ssl reports what the backend negotiated for this very session.
	var ssl bool
	var version string
	err := d.QueryRow(
		`SELECT ssl, coalesce(version, '') FROM pg_stat_ssl WHERE pid = pg_backend_pid()`).
		Scan(&ssl, &version)
	if err != nil {
		d.t.Fatalf("pg_stat_ssl: %v", err)
	}
	if !ssl {
		d.t.Fatal("connection is not encrypted, but the test asked for TLS")
	}
	d.t.Logf("%s backend negotiated %s", backendName, version)
}

func TestTLSVerifyFull(t *testing.T) {
	db, err := openTLS(t, "verify-full", true)
	if err != nil {
		t.Fatalf("verify-full with the test CA: %v", err)
	}
	db.assertEncrypted()
}

func TestTLSRequire(t *testing.T) {
	// require encrypts without authenticating, so it needs no CA.
	db, err := openTLS(t, "require", false)
	if err != nil {
		t.Fatalf("require: %v", err)
	}
	db.assertEncrypted()
}

// The point of verification: an unknown CA must be refused.
func TestTLSVerifyFullRejectsUnknownCA(t *testing.T) {
	db, err := openTLS(t, "verify-full", false)
	if err == nil {
		db.Close()
		t.Fatal("verify-full without the CA must fail: the certificate is self-signed")
	}
	t.Logf("%s backend rejected as expected: %v", backendName, err)
}

// A wrong host name must be refused even when the chain is trusted. This needs
// a second server whose certificate is valid but issued for another name:
//
//	openssl req -new -x509 -days 365 -nodes -text -out server.crt \
//	    -keyout server.key -subj "/CN=other.example" \
//	    -addext "subjectAltName=DNS:other.example"
//
// served on PGXSTDLIB_TLS_WRONGHOST_DSN with PGXSTDLIB_TLS_WRONGHOST_CA.
func TestTLSVerifyFullRejectsHostnameMismatch(t *testing.T) {
	dsn := os.Getenv("PGXSTDLIB_TLS_WRONGHOST_DSN")
	ca := os.Getenv("PGXSTDLIB_TLS_WRONGHOST_CA")
	if dsn == "" || ca == "" {
		t.Skip("set PGXSTDLIB_TLS_WRONGHOST_DSN and PGXSTDLIB_TLS_WRONGHOST_CA")
	}

	// The CA is trusted, so only the name can fail the check.
	full := withSSLMode(t, dsn, "verify-full") + "&sslrootcert=" + ca
	db, err := Open(full)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = db.PingContext(ctx)
		db.Close()
	}
	if err == nil {
		t.Fatal("verify-full must reject a certificate issued for another host")
	}
	t.Logf("%s backend rejected as expected: %v", backendName, err)

	// The same server must be reachable when verification is not requested,
	// which proves the rejection above was the name check and not a dial
	// failure or an untrusted chain.
	relaxed, err := Open(withSSLMode(t, dsn, "require"))
	if err != nil {
		t.Fatalf("require on the same server: %v", err)
	}
	defer relaxed.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := relaxed.PingContext(ctx); err != nil {
		t.Fatalf("require on the same server: %v", err)
	}
}

func TestTLSDisableStaysPlaintext(t *testing.T) {
	db, err := openTLS(t, "disable", false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	defer db.Close()

	var ssl bool
	err = db.QueryRow(
		`SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()`).Scan(&ssl)
	if err != nil {
		t.Fatalf("pg_stat_ssl: %v", err)
	}
	if ssl {
		t.Fatal("sslmode=disable produced an encrypted connection")
	}
}

// Cancellation has to keep working once the connection is encrypted: the
// cancel request opens a second connection and upgrades that one too.
//
// The query is longer here than in the plaintext test on purpose. Cancelling
// over TLS costs a second dial plus a full handshake, so on a loaded machine
// the cancel can take seconds; a short query would then finish first and the
// test would fail without anything being wrong. Observed cost is about 600ms,
// so ten seconds leaves a wide margin while still proving the query was cut
// short rather than allowed to run out.
func TestTLSContextCancellation(t *testing.T) {
	db, err := openTLS(t, "verify-full", true)
	if err != nil {
		t.Fatalf("verify-full: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = db.ExecContext(ctx, `SELECT pg_sleep(10)`)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("query completed in %v; cancellation did not take effect over TLS", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("cancellation took %v, want well under the 10s query", elapsed)
	}
	t.Logf("%s backend cancelled over TLS in %v", backendName, elapsed.Round(time.Millisecond))
}
