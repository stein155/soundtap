// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// internalRoutes are registered but deliberately absent from the public reference:
// loopback hooks used by the co-located player and receivers,
// the UI itself and the profiler.
var internalRoutes = map[string]bool{
	"POST /api/source":              true,
	"POST /api/nowplaying":          true,
	"POST /api/position":            true,
	"POST /api/bluetooth/connected": true,
	"GET /api/speaker/audio/{name}": true,
	"GET /":                         true,
}

// registeredRoutes is every "METHOD /path" pattern the server's Handler registers, as
// the server registers it: no reading of its source, so a route added by a loop or a
// helper is found like any other.
func registeredRoutes(t *testing.T) map[string]bool {
	t.Helper()
	srv, _, _ := newServerSrv(t)
	out := map[string]bool{}
	for _, pat := range srv.RoutesForTest() {
		out[strings.ReplaceAll(pat, "...}", "}")] = true // OpenAPI has no rest-of-path wildcard
	}
	return out
}

func specRoutes(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for p, ms := range doc.Paths {
		for m := range ms {
			out[strings.ToUpper(m)+" "+p] = true
		}
	}
	return out
}

// The reference must not rot: every registered public route is documented and every
// documented route exists.
func TestOpenAPIMatchesRoutes(t *testing.T) {
	reg, spec := registeredRoutes(t), specRoutes(t)
	if len(reg) < 50 {
		t.Fatalf("found only %d registered routes; the scanner is broken", len(reg))
	}
	var problems []string
	for r := range reg {
		if !spec[r] && !internalRoutes[r] {
			problems = append(problems, "route missing from openapi.json (document it or add to internalRoutes): "+r)
		}
		if spec[r] && internalRoutes[r] {
			problems = append(problems, "internal route is documented: "+r)
		}
	}
	for r := range spec {
		if !reg[r] {
			problems = append(problems, "openapi.json names a route that is not registered: "+r)
		}
	}
	for r := range internalRoutes {
		if !reg[r] {
			problems = append(problems, "internalRoutes names a route that is not registered: "+r)
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

func TestDocsServedWithoutToken(t *testing.T) {
	h, _ := newServer(t)
	for path, ct := range map[string]string{"/api/docs": "text/html", "/api/openapi.json": "application/json"} {
		rec := do(t, h, "GET", path, "")
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), ct) {
			t.Errorf("%s: status %d, type %q", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	var v map[string]any
	if err := json.Unmarshal(do(t, h, "GET", "/api/openapi.json", "").Body.Bytes(), &v); err != nil || v["openapi"] == nil {
		t.Errorf("openapi.json is not an OpenAPI document: %v", err)
	}
}

// TestNoPlainTextErrors keeps the API's one error shape (ADR 0091): http.Error writes
// text/plain, which the UI cannot read as {"error": ...}. Use apiError or badRequest.
// It parses the package and its firmware lane as a lint rule does, so a comment or a
// string that mentions http.Error is no hit and a call written another way is one.
func TestNoPlainTextErrors(t *testing.T) {
	fset := token.NewFileSet()
	for _, glob := range []string{"*.go", "firmware/*.go"} {
		files, err := filepath.Glob(glob)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" {
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "http" {
						t.Errorf("%s writes a plain-text error with http.Error; use apiError", fset.Position(call.Pos()))
					}
				}
				return true
			})
		}
	}
}

// The router's own errors under /api/ use the same JSON shape as the handlers'.
func TestRouterErrorsAreJSON(t *testing.T) {
	h, _ := newServer(t)
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/api/not-a-route", http.StatusNotFound},
		{"PATCH", "/api/volume", http.StatusMethodNotAllowed},
	} {
		rec := do(t, h, tc.method, tc.path, "")
		var body struct{ Error string }
		if rec.Code != tc.code || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") ||
			json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error == "" {
			t.Errorf("%s %s: %d %q %q", tc.method, tc.path, rec.Code, rec.Header().Get("Content-Type"), rec.Body)
		}
	}
}
