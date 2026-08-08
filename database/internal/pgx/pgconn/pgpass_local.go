package pgconn

// Local patch: see ../../PATCHES.md.
//
// A package-local .pgpass reader replacing github.com/jackc/pgpassfile. That
// module keeps an unused package-level regexp.MustCompile, which alone links
// the whole regexp engine into every TinyGo binary; the parser itself is the
// few lines below, behavior-identical to upstream.

import (
	"bufio"
	"io"
	"os"
	"strings"
)

type pgPassfile struct {
	entries []pgPassEntry
}

type pgPassEntry struct {
	hostname string
	port     string
	database string
	username string
	password string
}

// readPassfile reads the file at path, matching pgpassfile.ReadPassfile.
func readPassfile(path string) (*pgPassfile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parsePassfile(f)
}

func parsePassfile(r io.Reader) (*pgPassfile, error) {
	pf := &pgPassfile{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if entry, ok := parsePassfileLine(scanner.Text()); ok {
			pf.entries = append(pf.entries, entry)
		}
	}
	return pf, scanner.Err()
}

// parsePassfileLine parses one line. Comments and unparsable lines are
// skipped, as upstream skips them.
func parsePassfileLine(line string) (pgPassEntry, bool) {
	const (
		tmpBackslash = "\r"
		tmpColon     = "\n"
	)
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "#") {
		return pgPassEntry{}, false
	}
	line = strings.ReplaceAll(line, `\\`, tmpBackslash)
	line = strings.ReplaceAll(line, `\:`, tmpColon)
	parts := strings.Split(line, ":")
	if len(parts) != 5 {
		return pgPassEntry{}, false
	}
	for i := range parts {
		parts[i] = strings.ReplaceAll(parts[i], tmpBackslash, `\`)
		parts[i] = strings.ReplaceAll(parts[i], tmpColon, `:`)
	}
	return pgPassEntry{
		hostname: parts[0], port: parts[1], database: parts[2],
		username: parts[3], password: parts[4],
	}, true
}

// FindPassword matches pgpassfile's method of the same name, so the call in
// config.go keeps its shape.
func (pf *pgPassfile) FindPassword(hostname, port, database, username string) string {
	for _, e := range pf.entries {
		if (e.hostname == "*" || e.hostname == hostname) &&
			(e.port == "*" || e.port == port) &&
			(e.database == "*" || e.database == database) &&
			(e.username == "*" || e.username == username) {
			return e.password
		}
	}
	return ""
}
