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
	"slices"
	"strings"
	"testing"
)

// internalRoutesDoc is the part of openapi.json that lists the internal routes.
type internalRoutesDoc struct {
	Internal internalRouteList `json:"x-soundtap-internal"`
}

// internalRouteList is openapi.json's x-soundtap-internal section.
type internalRouteList struct {
	Routes []string `json:"routes"`
}

// internalRoutes are registered but deliberately absent from the public reference, as
// openapi.json's x-soundtap-internal lists them (ADR 0108): loopback hooks used by the
// co-located player and receivers, and the UI itself.
func internalRoutes(t *testing.T) map[string]bool {
	t.Helper()

	raw, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}

	var doc internalRoutesDoc

	err = json.Unmarshal(raw, &doc)
	if err != nil {
		t.Fatal(err)
	}

	if len(doc.Internal.Routes) == 0 {
		t.Fatal("openapi.json has no x-soundtap-internal routes")
	}

	out := map[string]bool{}
	for _, r := range doc.Internal.Routes {
		out[r] = true
	}

	return out
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

	raw, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}

	err = json.Unmarshal(raw, &doc)
	if err != nil {
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
	t.Parallel()

	reg, spec, internal := registeredRoutes(t), specRoutes(t), internalRoutes(t)
	if len(reg) < 50 {
		t.Fatalf("found only %d registered routes; the scanner is broken", len(reg))
	}

	var problems []string

	for route := range reg {
		if !spec[route] && !internal[route] {
			problems = append(problems,
				"route missing from openapi.json (document it or add to x-soundtap-internal): "+route)
		}

		if spec[route] && internal[route] {
			problems = append(problems, "internal route is documented: "+route)
		}
	}

	for r := range spec {
		if !reg[r] {
			problems = append(problems, "openapi.json names a route that is not registered: "+r)
		}
	}

	for r := range internal {
		if !reg[r] {
			problems = append(problems, "x-soundtap-internal names a route that is not registered: "+r)
		}
	}

	slices.Sort(problems)

	for _, p := range problems {
		t.Error(p)
	}
}

func TestDocsServedWithoutToken(t *testing.T) {
	t.Parallel()

	handler, _ := newServer(t)
	for path, ct := range map[string]string{"/api/docs": "text/html", "/api/openapi.json": "application/json"} {
		rec := do(t, handler, "GET", path, "")
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), ct) {
			t.Errorf("%s: status %d, type %q", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}

	var v map[string]any

	err := json.Unmarshal(do(t, handler, "GET", "/api/openapi.json", "").Body.Bytes(), &v)
	if err != nil || v["openapi"] == nil {
		t.Errorf("openapi.json is not an OpenAPI document: %v", err)
	}
}

// TestNoPlainTextErrors keeps the API's one error shape (ADR 0091): http.Error writes
// text/plain, which the UI cannot read as {"error": ...}. Use apiError or badRequest.
// It parses the package and its firmware lane as a lint rule does, so a comment or a
// string that mentions http.Error is no hit and a call written another way is one.
func TestNoPlainTextErrors(t *testing.T) {
	t.Parallel()

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
						t.Errorf("%s writes a plain-text error with http.Error; use apiError",
							fset.Position(call.Pos()))
					}
				}

				return true
			})
		}
	}
}

// The router's own errors under /api/ use the same JSON shape as the handlers'.
func TestRouterErrorsAreJSON(t *testing.T) {
	t.Parallel()

	handler, _ := newServer(t)
	for _, testCase := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/api/not-a-route", http.StatusNotFound},
		{"PATCH", "/api/volume", http.StatusMethodNotAllowed},
	} {
		rec := do(t, handler, testCase.method, testCase.path, "")

		var body struct {
			Error string `json:"error"`
		}
		if rec.Code != testCase.code || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") ||
			json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error == "" {
			t.Errorf("%s %s: %d %q %q",
				testCase.method, testCase.path, rec.Code, rec.Header().Get("Content-Type"), rec.Body)
		}
	}
}
