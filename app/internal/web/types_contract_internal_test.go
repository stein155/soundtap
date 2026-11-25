// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/favorites"
	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/recent"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/store"
	"github.com/stein155/soundtap/app/internal/upnp"
	"github.com/stein155/soundtap/app/internal/web/apierror"
	"github.com/stein155/soundtap/app/internal/web/firmware"
)

// jsonFields lists the JSON names of a struct's fields, and the ones it always writes:
// those without omitempty.
func jsonFields(rt reflect.Type) ([]string, []string) {
	var all, always []string

	for field := range rt.Fields() {
		tag, opts, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == "" || tag == "-" || !field.IsExported() {
			continue
		}

		all = append(all, tag)
		if !strings.Contains(opts, "omitempty") {
			always = append(always, tag)
		}
	}

	slices.Sort(all)
	slices.Sort(always)

	return all, always
}

// The UI's API types are generated from openapi.json, so each schema of a response encoded
// from a Go struct must name exactly the struct's JSON fields and require exactly the ones
// it always writes. A renamed JSON tag or a new omitempty fails here. The responses built
// from maps are checked against the spec by running their handlers (openapi_schema_test.go).
func schemaContract() map[string]reflect.Type {
	return map[string]reflect.Type{
		"NowPlaying":        reflect.TypeFor[speaker.NowPlaying](),
		"Preset":            reflect.TypeFor[speaker.Preset](),
		"StoredPreset":      reflect.TypeFor[store.Preset](),
		"RecentEntry":       reflect.TypeFor[recent.Entry](),
		"Favorite":          reflect.TypeFor[favorites.Entry](),
		"BassCaps":          reflect.TypeFor[speaker.Bass](),
		"ToneControl":       reflect.TypeFor[speaker.ToneControl](),
		"Tone":              reflect.TypeFor[speaker.Tone](),
		"Soundbar":          reflect.TypeFor[speaker.Soundbar](),
		"NetworkInfo":       reflect.TypeFor[speaker.Network](),
		"WifiNetwork":       reflect.TypeFor[speaker.WifiNetwork](),
		"Display":           reflect.TypeFor[settings.Display](),
		"Alarm":             reflect.TypeFor[settings.Alarm](),
		"MultiroomClient":   reflect.TypeFor[multiroom.Client](),
		"FirmwareReport":    reflect.TypeFor[firmware.Report](),
		"UPnPServer":        reflect.TypeFor[upnp.MediaServer](),
		"UPnPItem":          reflect.TypeFor[upnp.Item](),
		"Error":             reflect.TypeFor[apierror.Body](),
		"Health":            reflect.TypeFor[health](),
		"Boot":              reflect.TypeFor[bootState](),
		"CrashReport":       reflect.TypeFor[crashReport](),
		"CrashReportAnswer": reflect.TypeFor[crashReportAnswer](),
	}
}

// specSchema is one schema of openapi.json's components: its property names and the ones
// it requires.
type specSchema struct {
	Properties map[string]any `json:"properties"`
	Required   []string       `json:"required"`
}

// specComponents is openapi.json's components section.
type specComponents struct {
	Schemas map[string]specSchema `json:"schemas"`
}

// schemaSpec is the part of openapi.json the struct contract reads.
type schemaSpec struct {
	Components specComponents `json:"components"`
}

func TestOpenAPIMatchesGoStructs(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}

	var spec schemaSpec

	err = json.Unmarshal(raw, &spec)
	if err != nil {
		t.Fatal(err)
	}

	for name, goType := range schemaContract() {
		schema, ok := spec.Components.Schemas[name]
		if !ok {
			t.Errorf("openapi.json has no schema %s", name)

			continue
		}

		all, always := jsonFields(goType)
		if props := slices.Sorted(maps.Keys(schema.Properties)); !slices.Equal(all, props) {
			t.Errorf("openapi.json %s properties %v, Go %v %v", name, props, goType, all)
		}

		if req := slices.Sorted(slices.Values(schema.Required)); !slices.Equal(always, req) {
			t.Errorf("openapi.json %s required %v, Go %v always writes %v", name, req, goType, always)
		}
	}
}
