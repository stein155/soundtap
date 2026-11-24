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
func jsonFields(v any) (all, always []string) {
	rt := reflect.TypeOf(v)
	for i := range rt.NumField() {
		tag, opts, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if tag == "" || tag == "-" || !rt.Field(i).IsExported() {
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
var schemaContract = map[string]any{
	"NowPlaying":        speaker.NowPlaying{},
	"Preset":            speaker.Preset{},
	"StoredPreset":      store.Preset{},
	"RecentEntry":       recent.Entry{},
	"Favorite":          favorites.Entry{},
	"BassCaps":          speaker.Bass{},
	"ToneControl":       speaker.ToneControl{},
	"Tone":              speaker.Tone{},
	"Soundbar":          speaker.Soundbar{},
	"NetworkInfo":       speaker.Network{},
	"WifiNetwork":       speaker.WifiNetwork{},
	"Display":           settings.Display{},
	"Alarm":             settings.Alarm{},
	"MultiroomClient":   multiroom.Client{},
	"FirmwareReport":    firmware.Report{},
	"UPnPServer":        upnp.MediaServer{},
	"UPnPItem":          upnp.Item{},
	"Error":             apierror.Body{},
	"Health":            health{},
	"Boot":              bootState{},
	"CrashReport":       crashReport{},
	"CrashReportAnswer": crashReportAnswer{},
}

func TestOpenAPIMatchesGoStructs(t *testing.T) {
	b, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `json:"properties"`
				Required   []string       `json:"required"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	for name, v := range schemaContract {
		s, ok := spec.Components.Schemas[name]
		if !ok {
			t.Errorf("openapi.json has no schema %s", name)
			continue
		}
		all, always := jsonFields(v)
		if props := slices.Sorted(maps.Keys(s.Properties)); !slices.Equal(all, props) {
			t.Errorf("openapi.json %s properties %v, Go %T %v", name, props, v, all)
		}
		if req := slices.Sorted(slices.Values(s.Required)); !slices.Equal(always, req) {
			t.Errorf("openapi.json %s required %v, Go %T always writes %v", name, req, v, always)
		}
	}
}

// The UI translates an error by its code, so openapi.json lists exactly the codes the
// speaker writes, in the order apierror.Codes keeps them.
func TestOpenAPIListsEveryErrorCode(t *testing.T) {
	b, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Components struct {
			Schemas struct {
				Error struct {
					Properties struct {
						Code struct {
							Enum []apierror.Code `json:"enum"`
						} `json:"code"`
					} `json:"properties"`
				} `json:"Error"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	if got := spec.Components.Schemas.Error.Properties.Code.Enum; !slices.Equal(got, apierror.Codes) {
		t.Errorf("openapi.json Error.code enum %v, apierror.Codes %v", got, apierror.Codes)
	}
}
