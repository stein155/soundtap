// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
	t.Parallel()

	now := currentContract(t)
	path := contractFile(web.APILevel)

	if os.Getenv("UPDATE_API_CONTRACT") != "" {
		b, err := json.MarshalIndent(now, "", " ")
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(path, append(b, '\n'), 0o644)
		if err != nil {
			t.Fatal(err)
		}

		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no recorded contract for API level %d (%v): a new level records its contract "+
			"with UPDATE_API_CONTRACT=1 go test ./internal/web -run TestAPIContract", web.APILevel, err)
	}

	var was contract

	err = json.Unmarshal(data, &was)
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range contractBreaks(was, now) {
		t.Errorf(
			"%s: a breaking change at API level %d (ADR 0108); deprecate it first, then raise web.APILevel",
			p,
			web.APILevel,
		)
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

	for field, typ := range was.Fields {
		got, ok := now.Fields[field]
		switch {
		case !ok:
			out = append(out, "response field removed: "+field)
		case got != typ:
			out = append(out, fmt.Sprintf("response field retyped: %s %s -> %s", field, typ, got))
		default:
			// The field is there with its type: no break.
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

	slices.Sort(out)

	return out
}

type schemaDoc struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components schemaComponents                      `json:"components"`
}

// schemaComponents is the components section of an OpenAPI document.
type schemaComponents struct {
	Schemas   map[string]json.RawMessage `json:"schemas"`
	Responses map[string]json.RawMessage `json:"responses"`
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

	data, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}

	var doc schemaDoc

	err = json.Unmarshal(data, &doc)
	if err != nil {
		t.Fatal(err)
	}

	c, err := buildContract(doc)
	if err != nil {
		t.Fatal(err)
	}

	return c
}

// Why a schema reference does not resolve.
var (
	errRefLoop    = errors.New("$ref loop")
	errUnknownRef = errors.New("unknown $ref")
)

// schemaContent is a request or response body: one schema per media type.
type schemaContent struct {
	Content map[string]mediaSchema `json:"content"`
}

// mediaSchema is the schema of one media type.
type mediaSchema struct {
	Schema *schema `json:"schema"`
}

// maxRefDepth is how many $ref hops a schema may take before it counts as a loop.
const maxRefDepth = 20

// schemaWalker resolves and walks the schemas of one OpenAPI document.
type schemaWalker struct {
	doc schemaDoc
}

// resolve follows node's $ref chain to the schema it names.
func (sw schemaWalker) resolve(node *schema) (*schema, error) {
	for depth := 0; node != nil && node.Ref != ""; depth++ {
		if depth > maxRefDepth {
			return nil, fmt.Errorf("%w at %s", errRefLoop, node.Ref)
		}

		name := strings.TrimPrefix(node.Ref, "#/components/schemas/")

		raw, ok := sw.doc.Components.Schemas[name]
		if !ok {
			return nil, fmt.Errorf("%w %s", errUnknownRef, node.Ref)
		}

		var next schema

		err := json.Unmarshal(raw, &next)
		if err != nil {
			return nil, fmt.Errorf("schema %s: %w", name, err)
		}

		node = &next
	}

	return node, nil
}

// walk emits every field under node, at location below prefix.
func (sw schemaWalker) walk(
	prefix, location string, node *schema, seen map[string]bool, emit func(string, *schema),
) error {
	if node == nil {
		return nil
	}

	if node.Ref != "" {
		if seen[node.Ref] {
			return nil // a recursive schema: its fields were recorded at the first level
		}

		seen = cloneSeen(seen)
		seen[node.Ref] = true
	}

	resolved, err := sw.resolve(node)
	if err != nil {
		return err
	}

	if location != "" {
		emit(prefix+" "+location, resolved)
	}

	for _, sub := range resolved.AllOf {
		err := sw.walk(prefix, location, sub, seen, emit)
		if err != nil {
			return err
		}
	}

	for name, p := range resolved.Properties {
		err := sw.walk(prefix, location+"."+name, p, seen, emit)
		if err != nil {
			return err
		}
	}

	if resolved.Items != nil {
		return sw.walk(prefix, location+"[]", resolved.Items, seen, emit)
	}

	return nil
}

func buildContract(doc schemaDoc) (contract, error) {
	built := contract{Level: web.APILevel, Fields: map[string]string{}, Routes: nil, Required: nil}
	sw := schemaWalker{doc: doc}
	resolve, walk := sw.resolve, sw.walk

	for path, methods := range doc.Paths {
		for method, raw := range methods {
			if method == "parameters" || strings.HasPrefix(method, "x-") {
				continue
			}

			route := strings.ToUpper(method) + " " + path
			built.Routes = append(built.Routes, route)

			var operation struct {
				Responses   map[string]json.RawMessage `json:"responses"`
				RequestBody schemaContent              `json:"requestBody"`
			}

			err := json.Unmarshal(raw, &operation)
			if err != nil {
				return built, fmt.Errorf("%s: %w", route, err)
			}

			for status, rraw := range operation.Responses {
				if !strings.HasPrefix(status, "2") {
					continue
				}

				var resp schemaContent

				err := json.Unmarshal(rraw, &resp)
				if err != nil {
					return built, fmt.Errorf("%s %s: %w", route, status, err)
				}

				body, ok := resp.Content["application/json"]
				if !ok {
					continue
				}

				emit := func(key string, s *schema) {
					if s.Type != "" {
						built.Fields[key] = s.Type
					}
				}

				err = walk(route+" "+status, "", body.Schema, map[string]bool{}, emit)
				if err != nil {
					return built, fmt.Errorf("%s %s: %w", route, status, err)
				}
			}

			if body, ok := operation.RequestBody.Content["application/json"]; ok && body.Schema != nil {
				r, err := resolve(body.Schema)
				if err != nil {
					return built, fmt.Errorf("%s request: %w", route, err)
				}

				for _, name := range r.Required {
					built.Required = append(built.Required, route+" ."+name)
				}
			}
		}
	}

	slices.Sort(built.Routes)
	slices.Sort(built.Required)

	return built, nil
}

func cloneSeen(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m)+1)
	maps.Copy(out, m)

	return out
}

// The check itself: a removed route, a removed or retyped field and a newly required request
// field are breaks; additions are not.
func TestContractBreaks(t *testing.T) {
	t.Parallel()

	was := contract{
		Routes:   []string{"GET /api/a", "POST /api/b"},
		Fields:   map[string]string{"GET /api/a 200 .x": "string", "GET /api/a 200 .y": "integer"},
		Required: []string{"POST /api/b .name"},
		Level:    0,
	}

	same := contract{
		Routes: []string{"GET /api/a", "POST /api/b", "GET /api/new"},
		Fields: map[string]string{
			"GET /api/a 200 .x": "string",
			"GET /api/a 200 .y": "integer",
			"GET /api/a 200 .z": "boolean",
		},
		Required: []string{"POST /api/b .name", "GET /api/new .q"},
		Level:    0,
	}
	if got := contractBreaks(was, same); len(got) != 0 {
		t.Errorf("additions counted as breaks: %v", got)
	}

	broken := contract{
		Routes:   []string{"GET /api/a"},
		Fields:   map[string]string{"GET /api/a 200 .x": "integer"},
		Required: []string{"GET /api/a .key"},
		Level:    0,
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
