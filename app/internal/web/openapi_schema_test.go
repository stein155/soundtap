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
// The struct-backed schemas are also checked field by field in types_contract_test.go,
// which covers the ones this speaker cannot answer (Wi-Fi scan, firmware check, UPnP).
func TestResponsesMatchOpenAPI(t *testing.T) {
	spec := loadSpec(t)
	h, sp := newServer(t)
	if err := sp.StorePreset(context.Background(), 1, "TUNEIN", "stationurl", "/v1/playback/station/s12345", "Radio 2", "https://example.org/logo.png"); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, h, "POST", "/api/favorites", `{"source":"TUNEIN","name":"Radio 2","location":"s12345","art":""}`); rec.Code != 204 {
		t.Fatalf("add favorite: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{
		"/api/info", "/api/version", "/api/settings", "/api/now", "/api/presets", "/api/recent",
		"/api/favorites", "/api/volume", "/api/sleep", "/api/devices", "/api/channel", "/api/position", "/api/health",
	} {
		checkResponse(t, spec, path, do(t, h, "GET", path, ""))
	}
	// The release list cannot be fetched without the network; this is its unreachable answer.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, "GET", "/api/releases", nil)
	req.Host = "127.0.0.1:8000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	checkResponse(t, spec, "/api/releases", rec)
}

// The checker itself: a renamed field must fail it.
func TestSchemaCheckCatchesRename(t *testing.T) {
	spec := loadSpec(t)
	var v any
	if err := json.Unmarshal([]byte(`{"minutes":0,"endAt":""}`), &v); err != nil {
		t.Fatal(err)
	}
	got := checkSchema(spec, map[string]any{"$ref": "#/components/schemas/Sleep"}, v, "Sleep")
	want := []string{"Sleep: endsAt is required", "Sleep.endAt is not in the schema"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func loadSpec(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(b, &spec); err != nil {
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
	schema, ok := dig(spec, "paths", path, "get", "responses", "200", "content", "application/json", "schema").(map[string]any)
	if !ok {
		t.Errorf("openapi.json has no 200 schema for GET %s", path)
		return
	}
	var v any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	for _, p := range checkSchema(spec, schema, v, path) {
		t.Error(p)
	}
}

func dig(v any, keys ...string) any {
	for _, k := range keys {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

// checkSchema checks v against the subset of OpenAPI 3.0 the spec uses: $ref, type,
// properties, required, items, enum, nullable, and allOf with one $ref (how 3.0 makes a
// $ref nullable). An object with properties is closed: a field the schema does not name
// is a problem, so a rename shows on both ends.
func checkSchema(spec, s map[string]any, v any, at string) []string {
	if v == nil {
		if s["nullable"] == true {
			return nil
		}
		return []string{at + " is null"}
	}
	if all, ok := s["allOf"].([]any); ok && len(all) == 1 {
		s, _ = all[0].(map[string]any)
	}
	if ref, ok := s["$ref"].(string); ok {
		target, _ := dig(spec, strings.Split(strings.TrimPrefix(ref, "#/"), "/")...).(map[string]any)
		if target == nil {
			return []string{at + ": unresolved " + ref}
		}
		return checkSchema(spec, target, v, at)
	}
	if enum, ok := s["enum"].([]any); ok && !slices.Contains(enum, v) {
		return []string{fmt.Sprintf("%s = %v, not one of %v", at, v, enum)}
	}
	var out []string
	switch s["type"] {
	case "object":
		m, ok := v.(map[string]any)
		props, _ := s["properties"].(map[string]any)
		if !ok || props == nil {
			return typeProblem(at, "an object", ok)
		}
		required, _ := s["required"].([]any)
		for _, r := range required {
			if _, ok := m[r.(string)]; !ok {
				out = append(out, fmt.Sprintf("%s: %s is required", at, r))
			}
		}
		for _, k := range slices.Sorted(maps.Keys(m)) {
			ps, ok := props[k].(map[string]any)
			if !ok {
				out = append(out, at+"."+k+" is not in the schema")
				continue
			}
			out = append(out, checkSchema(spec, ps, m[k], at+"."+k)...)
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return typeProblem(at, "an array", false)
		}
		items, _ := s["items"].(map[string]any)
		for i, e := range a {
			out = append(out, checkSchema(spec, items, e, fmt.Sprintf("%s[%d]", at, i))...)
		}
	case "string":
		_, ok := v.(string)
		return typeProblem(at, "a string", ok)
	case "boolean":
		_, ok := v.(bool)
		return typeProblem(at, "a boolean", ok)
	case "number":
		_, ok := v.(float64)
		return typeProblem(at, "a number", ok)
	case "integer":
		f, ok := v.(float64)
		return typeProblem(at, "an integer", ok && f == math.Trunc(f))
	}
	return out
}

func typeProblem(at, want string, ok bool) []string {
	if ok {
		return nil
	}
	return []string{at + " is not " + want}
}
