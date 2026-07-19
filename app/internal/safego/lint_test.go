// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package safego

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// goStatements lists the go statements in the non-test Go files under root, as
// "path:line", skipping the files allowed to hold one.
func goStatements(t *testing.T, root string, allowed map[string]bool) []string {
	t.Helper()
	var found []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if allowed[filepath.ToSlash(rel)] {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if g, ok := n.(*ast.GoStmt); ok {
				found = append(found, filepath.ToSlash(rel)+":"+strconv.Itoa(fset.Position(g.Pos()).Line))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// Every goroutine in internal/ starts through Go, so its panic reaches panic.log
// (ADR 0052). A bare go statement is a goroutine whose crash leaves no trace.
func TestNoBareGoStatements(t *testing.T) {
	if found := goStatements(t, "..", map[string]bool{"safego/safego.go": true}); len(found) > 0 {
		t.Fatalf("bare go statements; start them with safego.Go:\n%s", strings.Join(found, "\n"))
	}
}

// The check finds go statements wherever they sit, and only outside tests.
func TestGoStatementsFindsEveryForm(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a\nfunc f(ch chan int) {\n\tgo f(ch)\n\tif true { go func() {}() }\n\tdefer func() { go f(ch) }()\n}\n")
	write("a_test.go", "package a\nfunc g() { go g() }\n")
	write("ok.go", "package a\nfunc h() { go h() }\n")
	got := strings.Join(goStatements(t, dir, map[string]bool{"ok.go": true}), " ")
	if got != "a.go:3 a.go:4 a.go:5" {
		t.Fatalf("found %q", got)
	}
}
