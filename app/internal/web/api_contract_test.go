// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/web"
)

// The stable API's contract at each level (ADR 0108), recorded in testdata/api-contract-<level>.json:
// every documented route and method, every response field with its type, and the request
// fields a route requires. A pull request that removes or retypes one of them, or makes a
// request field required, fails here unless it raises web.APILevel and records the new
// level's contract. Additions pass.
//
// Record a contract with: UPDATE_API_CONTRACT=1 go test ./internal/web -run TestAPIContract
// Before the first public release (0.1.0) the API is still mutable, and re-recording level 1
// is allowed; from then on a level's file is never re-recorded.

type contract struct {
	Level int `json:"level"`
	// Routes are "METHOD /path".
	Routes []string `json:"routes"`
	// Fields are "METHOD /path STATUS .field.sub[]" mapped to the field's type.
	Fields map[string]string `json:"fields"`
	// Required are "METHOD /path .field": request fields a client must send.
	Required []string `json:"required"`
}

func contractFile(level int) string {
	return filepath.Join("testdata", fmt.Sprintf("api-contract-%d.json", level))
}

func TestAPIContract(t *testing.T) {
	now := currentContract(t)
	path := contractFile(web.APILevel)
	if os.Getenv("UPDATE_API_CONTRACT") != "" {
		b, err := json.MarshalIndent(now, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no recorded contract for API level %d (%v): a new level records its contract "+
			"with UPDATE_API_CONTRACT=1 go test ./internal/web -run TestAPIContract", web.APILevel, err)
	}
	var was contract
	if err := json.Unmarshal(b, &was); err != nil {
		t.Fatal(err)
	}
	for _, p := range contractBreaks(was, now) {
		t.Errorf("%s: a breaking change at API level %d (ADR 0108); deprecate it first, then raise web.APILevel", p, web.APILevel)
	}
}

// contractBreaks lists what was in the old contract and is gone or changed in the new one.
func contractBreaks(was, now contract) []string {
	var out []string
	routes := map[string]bool{}
	for _, r := range now.Routes {
		routes[r] = true
	}
	for _, r := range was.Routes {
		if !routes[r] {
			out = append(out, "route removed: "+r)
		}
	}
	for f, typ := range was.Fields {
		got, ok := now.Fields[f]
		switch {
		case !ok:
			out = append(out, "response field removed: "+f)
		case got != typ:
			out = append(out, fmt.Sprintf("response field retyped: %s %s -> %s", f, typ, got))
		}
	}
	required := map[string]bool{}
	for _, r := range was.Required {
		required[r] = true
	}
	oldRoute := map[string]bool{}
	for _, r := range was.Routes {
		oldRoute[r] = true
	}
	for _, r := range now.Required {
		route := r[:strings.LastIndex(r, " ")]
		if oldRoute[route] && !required[r] {
			out = append(out, "request field now required: "+r)
		}
	}
	sort.Strings(out)
	return out
}

type schemaDoc struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas   map[string]json.RawMessage `json:"schemas"`
		Responses map[string]json.RawMessage `json:"responses"`
	} `json:"components"`
}

type schema struct {
	Ref        string             `json:"$ref"`
	Type       string             `json:"type"`
	Properties map[string]*schema `json:"properties"`
	Items      *schema            `json:"items"`
	Required   []string           `json:"required"`
	AllOf      []*schema          `json:"allOf"`
	OneOf      []*schema          `json:"oneOf"`
	AnyOf      []*schema          `json:"anyOf"`
}

func currentContract(t *testing.T) contract {
	t.Helper()
	b, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc schemaDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	c, err := buildContract(doc)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func buildContract(doc schemaDoc) (contract, error) {
	c := contract{Level: web.APILevel, Fields: map[string]string{}}
	resolve := func(s *schema) (*schema, error) {
		for depth := 0; s != nil && s.Ref != ""; depth++ {
			if depth > 20 {
				return nil, fmt.Errorf("$ref loop at %s", s.Ref)
			}
			name := strings.TrimPrefix(s.Ref, "#/components/schemas/")
			raw, ok := doc.Components.Schemas[name]
			if !ok {
				return nil, fmt.Errorf("unknown $ref %s", s.Ref)
			}
			var next schema
			if err := json.Unmarshal(raw, &next); err != nil {
				return nil, err
			}
			s = &next
		}
		return s, nil
	}
	var walk func(prefix, at string, s *schema, seen map[string]bool, emit func(string, *schema)) error
	walk = func(prefix, at string, s *schema, seen map[string]bool, emit func(string, *schema)) error {
		if s == nil {
			return nil
		}
		if s.Ref != "" {
			if seen[s.Ref] {
				return nil // a recursive schema: its fields were recorded at the first level
			}
			seen = cloneSeen(seen)
			seen[s.Ref] = true
		}
		r, err := resolve(s)
		if err != nil {
			return err
		}
		if at != "" {
			emit(prefix+" "+at, r)
		}
		for _, sub := range r.AllOf {
			if err := walk(prefix, at, sub, seen, emit); err != nil {
				return err
			}
		}
		for name, p := range r.Properties {
			if err := walk(prefix, at+"."+name, p, seen, emit); err != nil {
				return err
			}
		}
		if r.Items != nil {
			return walk(prefix, at+"[]", r.Items, seen, emit)
		}
		return nil
	}
	for path, methods := range doc.Paths {
		for method, raw := range methods {
			if method == "parameters" || strings.HasPrefix(method, "x-") {
				continue
			}
			route := strings.ToUpper(method) + " " + path
			c.Routes = append(c.Routes, route)
			var op struct {
				Responses   map[string]json.RawMessage `json:"responses"`
				RequestBody struct {
					Content map[string]struct {
						Schema *schema `json:"schema"`
					} `json:"content"`
				} `json:"requestBody"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				return c, fmt.Errorf("%s: %w", route, err)
			}
			for status, rraw := range op.Responses {
				if !strings.HasPrefix(status, "2") {
					continue
				}
				var resp struct {
					Content map[string]struct {
						Schema *schema `json:"schema"`
					} `json:"content"`
				}
				if err := json.Unmarshal(rraw, &resp); err != nil {
					return c, fmt.Errorf("%s %s: %w", route, status, err)
				}
				body, ok := resp.Content["application/json"]
				if !ok {
					continue
				}
				emit := func(key string, s *schema) {
					if s.Type != "" {
						c.Fields[key] = s.Type
					}
				}
				if err := walk(route+" "+status, "", body.Schema, map[string]bool{}, emit); err != nil {
					return c, fmt.Errorf("%s %s: %w", route, status, err)
				}
			}
			if body, ok := op.RequestBody.Content["application/json"]; ok && body.Schema != nil {
				r, err := resolve(body.Schema)
				if err != nil {
					return c, fmt.Errorf("%s request: %w", route, err)
				}
				for _, name := range r.Required {
					c.Required = append(c.Required, route+" ."+name)
				}
			}
		}
	}
	sort.Strings(c.Routes)
	sort.Strings(c.Required)
	return c, nil
}

func cloneSeen(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// The check itself: a removed route, a removed or retyped field and a newly required request
// field are breaks; additions are not.
func TestContractBreaks(t *testing.T) {
	was := contract{
		Routes:   []string{"GET /api/a", "POST /api/b"},
		Fields:   map[string]string{"GET /api/a 200 .x": "string", "GET /api/a 200 .y": "integer"},
		Required: []string{"POST /api/b .name"},
	}
	same := contract{
		Routes:   []string{"GET /api/a", "POST /api/b", "GET /api/new"},
		Fields:   map[string]string{"GET /api/a 200 .x": "string", "GET /api/a 200 .y": "integer", "GET /api/a 200 .z": "boolean"},
		Required: []string{"POST /api/b .name", "GET /api/new .q"},
	}
	if got := contractBreaks(was, same); len(got) != 0 {
		t.Errorf("additions counted as breaks: %v", got)
	}
	broken := contract{
		Routes:   []string{"GET /api/a"},
		Fields:   map[string]string{"GET /api/a 200 .x": "integer"},
		Required: []string{"GET /api/a .key"},
	}
	want := []string{
		"request field now required: GET /api/a .key",
		"response field removed: GET /api/a 200 .y",
		"response field retyped: GET /api/a 200 .x string -> integer",
		"route removed: POST /api/b",
	}
	if got := contractBreaks(was, broken); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("breaks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
