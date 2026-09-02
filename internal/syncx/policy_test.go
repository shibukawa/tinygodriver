//go:build !tinygo

package syncx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoStdRWMutexOnTinyGoPath enforces the policy: no file a TinyGo build can
// compile names sync.RWMutex in code. It asks the go tool which files a
// tinygo-tagged build of every package would include, for each GOOS this
// repository targets, and parses those. Comments may say sync.RWMutex all they
// like; test files are exempt, since GoFiles excludes them and they never
// ship.
func TestNoStdRWMutexOnTinyGoPath(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var offenders []string
	for _, goos := range []string{"darwin", "linux", "windows"} {
		cmd := exec.Command("go", "list", "-e", "-tags", "tinygo",
			"-f", `{{.Dir}}{{"\x00"}}{{join .GoFiles " "}}`, "./...")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list (GOOS=%s): %v", goos, err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			dir, files, ok := strings.Cut(line, "\x00")
			if !ok {
				continue
			}
			for _, f := range strings.Fields(files) {
				path := filepath.Join(dir, f)
				if seen[path] {
					continue
				}
				seen[path] = true
				for _, pos := range stdRWMutexUses(t, path) {
					rel, _ := filepath.Rel(root, path)
					offenders = append(offenders, rel+":"+strconv.Itoa(pos))
				}
			}
		}
	}
	if len(offenders) > 0 {
		t.Errorf("sync.RWMutex on the TinyGo path; use internal/syncx.RWMutex (see its package comment):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// stdRWMutexUses returns the lines on which path's code names sync.RWMutex
// through whatever local name the file imports "sync" under.
func stdRWMutexUses(t *testing.T, path string) []int {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	local := ""
	for _, imp := range file.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == "sync" {
			local = "sync"
			if imp.Name != nil {
				local = imp.Name.Name
			}
		}
	}
	if local == "" || local == "_" || local == "." {
		return nil
	}
	var lines []int
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "RWMutex" {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == local && x.Obj == nil {
			lines = append(lines, fset.Position(sel.Pos()).Line)
		}
		return true
	})
	return lines
}
