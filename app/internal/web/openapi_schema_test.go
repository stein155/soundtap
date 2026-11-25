// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

// The UI's API types are generated from openapi.json (app/frontend/src/lib/api/schema.d.ts),
// so the spec has to say what the handlers really write. This runs the handlers the test
// speaker can answer and checks each body against its 200 schema: an undeclared field, a
// missing required one or a wrong type fails here, not as an undefined on a real speaker.
// The struct-backed schemas are also checked field by field in types_contract_internal_test.go,
// which covers the ones this speaker cannot answer (Wi-Fi scan, firmware check, UPnP).
func TestResponsesMatchOpenAPI(t *testing.T) {
	t.Parallel()

	spec := loadSpec(t)

	handler, spk := newServer(t)

	err := spk.StorePreset(context.Background(), 1, "TUNEIN", "stationurl", "/v1/playback/station/s12345",
		"Radio 2", "https://example.org/logo.png")
	if err != nil {
		t.Fatal(err)
	}

	const favorite = `{"source":"TUNEIN","name":"Radio 2","location":"s12345","art":""}`
	if rec := do(t, handler, "POST", "/api/favorites", favorite); rec.Code != 204 {
		t.Fatalf("add favorite: %d %s", rec.Code, rec.Body)
	}

	for _, path := range []string{
		"/api/info", "/api/version", "/api/settings", "/api/now", "/api/presets", "/api/recent",
		"/api/favorites", "/api/volume", "/api/sleep", "/api/devices", "/api/channel", "/api/position", "/api/health",
	} {
		checkResponse(t, spec, path, do(t, handler, "GET", path, ""))
	}
	// The release list cannot be fetched without the network; this is its unreachable answer.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequestWithContext(ctx, "GET", "/api/releases", nil)
	req.Host = "127.0.0.1:8000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	checkResponse(t, spec, "/api/releases", rec)
}

// The checker itself: a renamed field must fail it.
func TestSchemaCheckCatchesRename(t *testing.T) {
	t.Parallel()

	spec := loadSpec(t)

	var doc any

	err := json.Unmarshal([]byte(`{"minutes":0,"endAt":""}`), &doc)
	if err != nil {
		t.Fatal(err)
	}

	got := checkSchema(spec, map[string]any{"$ref": "#/components/schemas/Sleep"}, doc, "Sleep")

	want := []string{"Sleep: endsAt is required", "Sleep.endAt is not in the schema"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func loadSpec(t *testing.T) map[string]any {
	t.Helper()

	raw, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}

	var spec map[string]any

	err = json.Unmarshal(raw, &spec)
	if err != nil {
		t.Fatal(err)
	}

	return spec
}

func checkResponse(t *testing.T, spec map[string]any, path string, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != 200 {
		t.Errorf("GET %s: %d %s", path, rec.Code, rec.Body)

		return
	}

	node := dig(spec, "paths", path, "get", "responses", "200", "content", "application/json", "schema")

	schema, ok := node.(map[string]any)
	if !ok {
		t.Errorf("openapi.json has no 200 schema for GET %s", path)

		return
	}

	var doc any

	err := json.Unmarshal(rec.Body.Bytes(), &doc)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}

	for _, p := range checkSchema(spec, schema, doc, path) {
		t.Error(p)
	}
}

func dig(node any, keys ...string) any {
	for _, k := range keys {
		m, _ := node.(map[string]any)
		node = m[k]
	}

	return node
}

// checkSchema checks v against the subset of OpenAPI 3.0 the spec uses: $ref, type,
// properties, required, items, enum, nullable, and allOf with one $ref (how 3.0 makes a
// $ref nullable). An object with properties is closed: a field the schema does not name
// is a problem, so a rename shows on both ends.
func checkSchema(spec, schema map[string]any, value any, where string) []string {
	if value == nil {
		if nullable, _ := schema["nullable"].(bool); nullable {
			return nil
		}

		return []string{where + " is null"}
	}

	if all, ok := schema["allOf"].([]any); ok && len(all) == 1 {
		schema, _ = all[0].(map[string]any)
	}

	if ref, ok := schema["$ref"].(string); ok {
		target, _ := dig(spec, strings.Split(strings.TrimPrefix(ref, "#/"), "/")...).(map[string]any)
		if target == nil {
			return []string{where + ": unresolved " + ref}
		}

		return checkSchema(spec, target, value, where)
	}

	if enum, ok := schema["enum"].([]any); ok && !slices.Contains(enum, value) {
		return []string{fmt.Sprintf("%s = %v, not one of %v", where, value, enum)}
	}

	var out []string

	switch schema["type"] {
	case "object":
		obj, ok := value.(map[string]any)

		if !ok {
			return typeProblem(where, "an object")
		}

		props, _ := schema["properties"].(map[string]any)
		if props == nil {
			return nil
		}

		required, _ := schema["required"].([]any)
		for _, r := range required {
			if _, ok := obj[r.(string)]; !ok {
				out = append(out, fmt.Sprintf("%s: %s is required", where, r))
			}
		}

		for _, key := range slices.Sorted(maps.Keys(obj)) {
			propSchema, ok := props[key].(map[string]any)
			if !ok {
				out = append(out, where+"."+key+" is not in the schema")

				continue
			}

			out = append(out, checkSchema(spec, propSchema, obj[key], where+"."+key)...)
		}
	case "array":
		elems, ok := value.([]any)
		if !ok {
			return typeProblem(where, "an array")
		}

		items, _ := schema["items"].(map[string]any)
		for i, e := range elems {
			out = append(out, checkSchema(spec, items, e, fmt.Sprintf("%s[%d]", where, i))...)
		}
	case "string":
		if _, ok := value.(string); !ok {
			return typeProblem(where, "a string")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return typeProblem(where, "a boolean")
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return typeProblem(where, "a number")
		}
	case "integer":
		if f, ok := value.(float64); !ok || f != math.Trunc(f) {
			return typeProblem(where, "an integer")
		}
	default:
		// A schema without a type takes any value.
	}

	return out
}

// typeProblem says the value at the given place is not of the wanted type.
func typeProblem(at, want string) []string {
	return []string{at + " is not " + want}
}
