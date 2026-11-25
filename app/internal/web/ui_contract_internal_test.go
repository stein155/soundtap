// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/mqttbridge"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/store"
)

// frontendSrc is the web UI's source, which mirrors limits and deadlines enforced here.
const frontendSrc = "../../frontend/src"

// uiConst reads a numeric constant from a web UI source file: a product of integer
// literals such as 15000 or 2 * 60 * 1000. A rename on the UI side fails the test here,
// rather than leaving it comparing against a number nobody uses any more.
func uiConst(t *testing.T, file, name string) int {
	t.Helper()

	src, err := os.ReadFile(filepath.Join(frontendSrc, file))
	if err != nil {
		t.Fatal(err)
	}

	match := regexp.MustCompile(`(?m)^(?:export )?const ` + name + ` = ([0-9_ *]+);`).FindSubmatch(src)
	if match == nil {
		t.Fatalf("%s: no numeric const %s", file, name)
	}

	value := 1

	for f := range strings.SplitSeq(string(match[1]), "*") {
		n, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(f), "_", ""))
		if err != nil {
			t.Fatalf("%s: %s: %v", file, name, err)
		}

		value *= n
	}

	return value
}

// uiMillis is a UI constant in milliseconds, as a Duration.
func uiMillis(t *testing.T, file, name string) time.Duration {
	t.Helper()

	return time.Duration(uiConst(t, file, name)) * time.Millisecond
}

// A handler that outlasts the web UI's request reports "failed" for work the speaker
// then finishes. Each deadline in deadlines.go names the UI constant it
// must stay under; a new deadline without one, or without an entry here, fails.
func TestDeadlinesEndBeforeTheWebUIGivesUp(t *testing.T) {
	t.Parallel()

	deadlines := map[string]time.Duration{
		"slowReadDeadline":       slowReadDeadline,
		"writeDeadline":          writeDeadline,
		"spotifyDeadline":        spotifyDeadline,
		"bluetoothClearDeadline": bluetoothClearDeadline,
		"wifiScanDeadline":       wifiScanDeadline,
		"wifiJoinDeadline":       wifiJoinDeadline,
		"restoreDeadline":        restoreDeadline,
	}
	uiFiles := map[string]string{
		"SLOW_READ_TIMEOUT_MS":       "lib/api/http.ts",
		"WRITE_TIMEOUT_MS":           "lib/api/http.ts",
		"SPOTIFY_TIMEOUT_MS":         "lib/spotify.ts",
		"BLUETOOTH_CLEAR_TIMEOUT_MS": "lib/api/settings.ts",
		"WIFI_SCAN_TIMEOUT_MS":       "lib/api/settings.ts",
		"WIFI_JOIN_TIMEOUT_MS":       "lib/api/settings.ts",
		"RESTORE_TIMEOUT_MS":         "lib/api/settings.ts",
	}

	astFile, err := parser.ParseFile(token.NewFileSet(), "deadlines.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	uiName := regexp.MustCompile(`[A-Z][A-Z_]+_MS`)
	seen := 0

	ast.Inspect(astFile, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}

		for _, ident := range spec.Names {
			seen++

			deadline, ok := deadlines[ident.Name]
			if !ok {
				t.Errorf("%s: add it to this test", ident.Name)

				continue
			}

			uiNames := uiName.FindString(spec.Doc.Text())

			file, ok := uiFiles[uiNames]
			if !ok {
				t.Errorf("%s: comment names no known UI deadline (%q)", ident.Name, uiNames)

				continue
			}

			if limit := uiMillis(t, file, uiNames); deadline >= limit {
				t.Errorf("%s = %v, not shorter than the UI's %s = %v", ident.Name, deadline, uiNames, limit)
			}
		}

		return true
	})

	if seen != len(deadlines) {
		t.Errorf("deadlines.go has %d deadlines, this test knows %d", seen, len(deadlines))
	}

	if got := uiMillis(t, "lib/base.ts", "PRESS_WINDOW_MS"); got != ConfirmWindow {
		t.Errorf("PRESS_WINDOW_MS = %v, ConfirmWindow = %v", got, ConfirmWindow)
	}
	// A route held for a key press: the UI allows the press window plus its own timeout
	// for the work after it, and has to outlast a speaker proxying it to another.
	for route, uiConstName := range map[string]string{
		"/api/wifi":             "WIFI_JOIN_TIMEOUT_MS",
		restorePath:             "RESTORE_TIMEOUT_MS",
		"/api/factory-reset":    "RESTORE_TIMEOUT_MS",
		"/api/bluetooth/clear":  "BLUETOOTH_CLEAR_TIMEOUT_MS",
		"/api/bluetooth/forget": "BLUETOOTH_CLEAR_TIMEOUT_MS",
	} {
		limit := ConfirmWindow + uiMillis(t, uiFiles[uiConstName], uiConstName)
		if d := proxyTimeout(route); d >= limit {
			t.Errorf("proxied %s = %v, not shorter than the UI's %v", route, d, limit)
		}
	}
	// Other press routes do little after the press and go through send's default.
	limit := ConfirmWindow + uiMillis(t, "lib/api/http.ts", "WRITE_TIMEOUT_MS")
	if d := proxyTimeout("/api/reboot"); d >= limit {
		t.Errorf("proxied /api/reboot = %v, not shorter than the UI's %v", d, limit)
	}
}

// A handler with a deadline of its own, written where it is used rather than in
// deadlines.go, still has to end before the proxy (peerTimeout) and the web UI
// (WRITE_TIMEOUT_MS) give up on it: writeDeadline is the longest such a deadline may be.
func TestInlineHandlerDeadlinesFitWriteDeadline(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// d reads N * time.Second or N * time.Minute, also with something added (offSlack).
	var eval func(ast.Expr) (time.Duration, bool)

	eval = func(e ast.Expr) (time.Duration, bool) {
		bin, ok := e.(*ast.BinaryExpr)
		if !ok {
			return 0, false
		}

		if bin.Op == token.ADD {
			return eval(bin.X)
		}

		lit, nok := bin.X.(*ast.BasicLit)

		unit, uok := bin.Y.(*ast.SelectorExpr)
		if bin.Op != token.MUL || !nok || !uok {
			return 0, false
		}

		v, _ := strconv.Atoi(lit.Value)

		units := map[string]time.Duration{"Second": time.Second, "Minute": time.Minute}

		return units[unit.Sel.Name] * time.Duration(v), true
	}
	fset := token.NewFileSet()
	checked := 0

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}

			fn, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || fn.Sel.Name != "WithTimeout" {
				return true
			}
			// Only a request's own context: background work has no caller waiting.
			parent, ok := call.Args[0].(*ast.CallExpr)
			if !ok {
				return true
			}

			if sel, ok := parent.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Context" {
				return true
			}

			if v, ok := eval(call.Args[1]); ok {
				checked++

				if v > writeDeadline {
					t.Errorf("%s: handler deadline %v is longer than writeDeadline %v",
						fset.Position(call.Pos()), v, writeDeadline)
				}
			}

			return true
		})
	}

	if checked == 0 {
		t.Error("found no inline handler deadline; the parser above no longer matches the code")
	}
}

// A request for another speaker goes through the proxy, so the proxy must wait out the
// peer's handler, or it reports "failed" for work the peer then finishes, and
// must end before the web UI gives up on the same call.
func TestProxyOutlastsThePeerAndEndsBeforeTheUI(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		route    string
		handler  time.Duration
		ui, file string
	}{
		{"/api/search", slowReadDeadline, "SLOW_READ_TIMEOUT_MS", "lib/api/http.ts"},
		{"/api/play/1", writeDeadline, "WRITE_TIMEOUT_MS", "lib/api/http.ts"},
		{"/api/spotify/play", spotifyDeadline, "SPOTIFY_TIMEOUT_MS", "lib/spotify.ts"},
		{"/api/wifi/scan", wifiScanDeadline, "WIFI_SCAN_TIMEOUT_MS", "lib/api/settings.ts"},
	} {
		got := proxyTimeout(testCase.route)
		if got <= testCase.handler {
			t.Errorf("%s: proxy gives up after %v, before the peer's %v", testCase.route, got, testCase.handler)
		}

		if ui := uiMillis(t, testCase.file, testCase.ui); got >= ui {
			t.Errorf("%s: proxy waits %v, not shorter than the UI's %s = %v", testCase.route, got, testCase.ui, ui)
		}
	}
}

// The port field shows the broker port the speaker uses when the field is left empty.
func TestUIMqttPortsMatchTheBridge(t *testing.T) {
	t.Parallel()

	if got := uiConst(t, "hooks/useHouseholdMqtt.ts", "DEFAULT_PORT"); got != mqttbridge.DefaultPort {
		t.Errorf("DEFAULT_PORT = %d, mqttbridge.DefaultPort = %d", got, mqttbridge.DefaultPort)
	}

	if got := uiConst(t, "hooks/useHouseholdMqtt.ts", "DEFAULT_TLS_PORT"); got != mqttbridge.DefaultTLSPort {
		t.Errorf("DEFAULT_TLS_PORT = %d, mqttbridge.DefaultTLSPort = %d", got, mqttbridge.DefaultTLSPort)
	}
}

// The UI stops input at the speaker's own limits; each is checked against the code
// that enforces it, so the UI cannot offer what the speaker refuses or refuse what it takes.
func TestUILimitsMatchTheSpeaker(t *testing.T) {
	t.Parallel()

	t.Run("name", func(t *testing.T) {
		t.Parallel()

		maxLen := uiConst(t, "lib/limits.ts", "NAME_MAX_LENGTH")

		_, err := settings.NormalizeName(strings.Repeat("a", maxLen))
		if err != nil {
			t.Errorf("a %d-byte name is refused: %v", maxLen, err)
		}

		_, err = settings.NormalizeName(strings.Repeat("a", maxLen+1))
		if err == nil {
			t.Errorf("a %d-byte name is accepted", maxLen+1)
		}
	})
	t.Run("presets", func(t *testing.T) {
		t.Parallel()

		count := uiConst(t, "lib/limits.ts", "PRESET_COUNT")

		presets, err := store.Open(filepath.Join(t.TempDir(), "presets.json"))
		if err != nil {
			t.Fatal(err)
		}

		err = presets.Set(store.Preset{
			Slot: count, StationID: "", Source: "", Type: "", Location: "", Name: "a", Logo: "",
		})
		if err != nil {
			t.Errorf("slot %d is refused: %v", count, err)
		}

		err = presets.Set(store.Preset{
			Slot: count + 1, StationID: "", Source: "", Type: "", Location: "", Name: "a", Logo: "",
		})
		if err == nil {
			t.Errorf("slot %d is accepted", count+1)
		}
	})
	t.Run("stream URL", func(t *testing.T) {
		t.Parallel()

		maxLen := uiConst(t, "components/organisms/settings/CustomStation/index.tsx", "MAX_URL")

		url := func(length int) string {
			const head = "http://radio.example/"

			return head + strings.Repeat("a", length-len(head))
		}
		if !validOwnStream(url(maxLen)) {
			t.Errorf("a %d-byte stream URL is refused", maxLen)
		}

		if validOwnStream(url(maxLen + 1)) {
			t.Errorf("a %d-byte stream URL is accepted", maxLen+1)
		}
	})
	t.Run("SSID", func(t *testing.T) {
		t.Parallel()

		maxLen := uiConst(t, "components/organisms/settings/NetworkSettings/useWifiJoin.ts", "SSID_MAX_LENGTH")

		err := speakerstate.CheckWifi(strings.Repeat("a", maxLen), "none", "")
		if err != nil {
			t.Errorf("a %d-byte SSID is refused: %v", maxLen, err)
		}

		err = speakerstate.CheckWifi(strings.Repeat("a", maxLen+1), "none", "")
		if err == nil {
			t.Errorf("a %d-byte SSID is accepted", maxLen+1)
		}
	})
}
