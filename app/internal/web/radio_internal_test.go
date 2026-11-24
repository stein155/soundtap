// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/clocksync"
	"github.com/stein155/soundtap/app/internal/nowplaying"
	"github.com/stein155/soundtap/app/internal/recent"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/sleeptimer"
	"github.com/stein155/soundtap/app/internal/snapcast"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/spotify"
	"github.com/stein155/soundtap/app/internal/tunein"
	"github.com/stein155/soundtap/app/internal/update"
)

// countingBackend is the in-process speaker with its preset plays counted, so the alarm
// tests can tell one alarm from two.
type countingBackend struct {
	*speakerstate.Speaker
	mu     sync.Mutex
	played []int
	notes  []string
}

func (c *countingBackend) PlayNotification(ctx context.Context, n speaker.Notification) error {
	c.mu.Lock()
	c.notes = append(c.notes, n.URL)
	c.mu.Unlock()
	return nil
}

func (c *countingBackend) notifications() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.notes...)
}

func (c *countingBackend) PlayPreset(ctx context.Context, slot int) error {
	c.mu.Lock()
	c.played = append(c.played, slot)
	c.mu.Unlock()
	return c.Speaker.PlayPreset(ctx, slot)
}

func (c *countingBackend) plays() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int(nil), c.played...)
}

// radioServer is a Server over the in-process speaker state, the way soundtapd wires it,
// with a settings file and a recent list in a temp dir.
func radioServer(t *testing.T) (*Server, *countingBackend, string) {
	return radioServerWith(t, func(sp *countingBackend) Backend { return sp })
}

// radioServerWith is radioServer with the backend wrapped, for tests that need the
// speaker to fail. Standby is watched the way soundtapd's main wires it.
func radioServerWith(t *testing.T, wrap func(*countingBackend) Backend) (*Server, *countingBackend, string) {
	t.Helper()
	dir := t.TempDir()
	sp := &countingBackend{Speaker: speakerstate.New()}
	set := settings.Open(filepath.Join(dir, "settings.json"))
	log := quiet()
	s := New(Options{TuneIn: tunein.New(), Backend: wrap(sp), Settings: set, Update: update.New("test", log),
		NowPlaying: nowplaying.New(sp, tunein.New()), Home: dir, Log: log,
		Speaker: SpeakerOptions{Recent: recent.Open(filepath.Join(dir, "recent.json"))}})
	s.WatchStandby(sp)
	// Standby fsyncs the recent list first, which a busy CI disk can hold for seconds.
	s.offSlack = time.Minute
	return s, sp, dir
}

func radioDo(t *testing.T, h http.Handler, method, path, body string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type sleepView struct {
	Minutes int    `json:"minutes"`
	EndsAt  string `json:"endsAt"`
}

func getSleepView(t *testing.T, h http.Handler) sleepView {
	t.Helper()
	rec := radioDo(t, h, http.MethodGet, "/api/sleep", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/sleep = %d %s", rec.Code, rec.Body)
	}
	var v sleepView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func playStation(t *testing.T, sp *countingBackend) {
	t.Helper()
	if err := sp.Select(context.Background(), "TUNEIN", "stationurl", "/v1/playback/station/s1", "Radio 1", "", ""); err != nil {
		t.Fatal(err)
	}
	sp.ConfirmPlaying(sp.BeginBuffering("TUNEIN"))
}

// A timer set in standby would stop music started later; it is refused instead.
func TestSleepTimerRefusedInStandby(t *testing.T) {
	s, sp, _ := radioServer(t)
	h := s.Handler()
	if rec := radioDo(t, h, http.MethodPost, "/api/sleep", `{"minutes":5}`); rec.Code != http.StatusConflict {
		t.Fatalf("POST in standby = %d, want 409", rec.Code)
	}
	if v := getSleepView(t, h); v.Minutes != 0 {
		t.Fatalf("timer set in standby: %+v", v)
	}
	playStation(t, sp)
	if rec := radioDo(t, h, http.MethodPost, "/api/sleep", `{"minutes":5}`); rec.Code != http.StatusOK {
		t.Fatalf("POST while playing = %d", rec.Code)
	}
}

func TestSleepTimerSetCancelAndValidate(t *testing.T) {
	s, sp, _ := radioServer(t)
	h := s.Handler()
	playStation(t, sp)
	if v := getSleepView(t, h); v != (sleepView{0, ""}) {
		t.Fatalf("idle timer = %+v", v)
	}
	for _, body := range []string{`{"minutes":241}`, `{"minutes":-1}`, `{}`, `{"minutes":"5"}`, `nope`} {
		if rec := radioDo(t, h, http.MethodPost, "/api/sleep", body); rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s = %d, want 400", body, rec.Code)
		}
	}
	before := time.Now()
	rec := radioDo(t, h, http.MethodPost, "/api/sleep", `{"minutes":30}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body)
	}
	var v sleepView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	ends, err := time.Parse(time.RFC3339, v.EndsAt)
	if err != nil || v.Minutes != 30 || !strings.HasSuffix(v.EndsAt, "Z") {
		t.Fatalf("set timer = %+v (%v)", v, err)
	}
	if d := ends.Sub(before); d < 29*time.Minute || d > 31*time.Minute {
		t.Fatalf("endsAt is %s from now, want 30m", d)
	}
	if got := getSleepView(t, h); got != v {
		t.Fatalf("GET = %+v, POST said %+v", got, v)
	}
	rec = radioDo(t, h, http.MethodPost, "/api/sleep", `{"minutes":0}`)
	if rec.Code != http.StatusOK || getSleepView(t, h) != (sleepView{0, ""}) {
		t.Fatalf("cancel = %d %s", rec.Code, rec.Body)
	}
}

// The timer going off is POST /api/stop: what was playing goes into the recent list,
// the speaker goes to standby and the timer is gone.
func TestSleepTimerExpiryStandsTheSpeakerDown(t *testing.T) {
	s, sp, _ := radioServer(t)
	s.sleepUnit = 10 * time.Millisecond
	h := s.Handler()
	playStation(t, sp)
	if rec := radioDo(t, h, http.MethodPost, "/api/sleep", `{"minutes":1}`); rec.Code != http.StatusOK {
		t.Fatalf("POST = %d", rec.Code)
	}
	eventually(t, "standby", sp.IsStandby)
	eventually(t, "timer cleared", func() bool { return getSleepView(t, h).Minutes == 0 })
	if got := s.recentStore.All(); len(got) != 1 || got[0].Name != "Radio 1" {
		t.Fatalf("recent = %+v, want the station that was playing", got)
	}
}

func TestStopAndPowerOffCancelTheSleepTimer(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/api/stop", ""},
		{"/api/power", `{"on":false}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			s, sp, _ := radioServer(t)
			h := s.Handler()
			playStation(t, sp)
			radioDo(t, h, http.MethodPost, "/api/sleep", `{"minutes":30}`)
			if rec := radioDo(t, h, http.MethodPost, tc.path, tc.body); rec.Code != http.StatusOK {
				t.Fatalf("%s = %d %s", tc.path, rec.Code, rec.Body)
			}
			if !sp.IsStandby() {
				t.Fatal("speaker not in standby")
			}
			if v := getSleepView(t, h); v.Minutes != 0 {
				t.Fatalf("timer still running after %s: %+v", tc.path, v)
			}
			if len(s.recentStore.All()) != 1 {
				t.Fatalf("%s did not record what was playing", tc.path)
			}
		})
	}
}

func TestPowerOnWakesAndValidates(t *testing.T) {
	s, sp, _ := radioServer(t)
	h := s.Handler()
	playStation(t, sp)
	radioDo(t, h, http.MethodPost, "/api/power", `{"on":false}`)
	rec := radioDo(t, h, http.MethodPost, "/api/power", `{"on":true}`)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"on":true}` {
		t.Fatalf("power on = %d %s", rec.Code, rec.Body)
	}
	if sp.IsStandby() {
		t.Fatal("speaker still in standby after power on")
	}
	// The tuned station is replayable, so waking goes back to it.
	if snap := sp.StatusSnapshot(); snap.Source != "TUNEIN" {
		t.Fatalf("woke into %q, want TUNEIN", snap.Source)
	}
	for _, body := range []string{`{}`, `{"on":"yes"}`, ``} {
		if rec := radioDo(t, h, http.MethodPost, "/api/power", body); rec.Code != http.StatusBadRequest {
			t.Errorf("POST %q = %d, want 400", body, rec.Code)
		}
	}
}

// ---- alarm ----------------------------------------------------------------------

func alarmServer(t *testing.T, alarm settings.Alarm, tz string) (*Server, *countingBackend) {
	t.Helper()
	s, sp, _ := radioServer(t)
	if err := s.settings.SetAlarm(alarm); err != nil {
		t.Fatal(err)
	}
	if tz != "" {
		if err := s.settings.SetDisplay(settings.Display{Timezone: tz}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sp.StorePreset(context.Background(), 2, "TUNEIN", "stationurl", "/v1/playback/station/s2", "Two", ""); err != nil {
		t.Fatal(err)
	}
	return s, sp
}

func TestAlarmUnmutesTheSpeaker(t *testing.T) {
	for _, level := range []int{30, 5} {
		s, sp := alarmServer(t, settings.Alarm{Enabled: true, Time: "07:00", Slot: 2}, "UTC")
		ctx := context.Background()
		_ = sp.SetVolume(ctx, level)
		_ = sp.Key(ctx, "MUTE")
		if !s.checkAlarm(ctx, time.Date(2026, 11, 14, 7, 0, 0, 0, time.UTC)) {
			t.Fatal("alarm did not trigger")
		}
		if sp.Muted() {
			t.Fatalf("level %d: alarm started the preset but the speaker remains muted", level)
		}
		if v, _ := sp.Volume(ctx); v < alarmMinVolume {
			t.Fatalf("level %d: volume %d is below the alarm floor", level, v)
		}
	}
}

func TestAlarmFiresOncePerMinuteInTheSpeakersZone(t *testing.T) {
	s, sp := alarmServer(t, settings.Alarm{Enabled: true, Time: "07:00", Slot: 2}, "Europe/Amsterdam")
	ctx := context.Background()
	// 4 October is summer time in Amsterdam: 07:00 there is 05:00 UTC.
	at := func(day, h, m, sec int) time.Time { return time.Date(2026, 10, day, h, m, sec, 0, time.UTC) }

	if s.checkAlarm(ctx, at(4, 7, 0, 5)) {
		t.Fatal("fired at 07:00 UTC, which is 09:00 in the speaker's zone")
	}
	if !s.checkAlarm(ctx, at(4, 5, 0, 5)) {
		t.Fatal("did not fire at 07:00 local")
	}
	if s.checkAlarm(ctx, at(4, 5, 0, 25)) || s.checkAlarm(ctx, at(4, 5, 0, 45)) {
		t.Fatal("fired again on a later tick inside the same minute")
	}
	if s.checkAlarm(ctx, at(4, 5, 1, 5)) {
		t.Fatal("fired in the minute after the alarm")
	}
	if !s.checkAlarm(ctx, at(5, 5, 0, 15)) {
		t.Fatal("did not fire the next day")
	}
	if got := sp.plays(); len(got) != 2 || got[0] != 2 || got[1] != 2 {
		t.Fatalf("preset plays = %v, want [2 2]", got)
	}
	if sp.StatusSnapshot().Source != "TUNEIN" {
		t.Fatal("the alarm did not start the preset")
	}
}

// Before the first NTP sync of a boot the clock is the one restored from flash, hours off
// after a power cut: the alarm waits, as stock's clock does.
func TestAlarmWaitsForTheFirstNTPSync(t *testing.T) {
	s, sp := alarmServer(t, settings.Alarm{Enabled: true, Time: "07:00", Slot: 2}, "UTC")
	synced := clocksync.Path
	clocksync.Path = filepath.Join(t.TempDir(), "missing")
	defer func() { clocksync.Path = synced }()
	if s.checkAlarm(context.Background(), time.Date(2026, 10, 4, 7, 0, 5, 0, time.UTC)) || len(sp.plays()) != 0 {
		t.Fatal("the alarm fired on a clock NTP has not set")
	}
	clocksync.Path = synced
	if !s.checkAlarm(context.Background(), time.Date(2026, 10, 4, 7, 0, 25, 0, time.UTC)) {
		t.Fatal("the alarm did not fire once the clock synced")
	}
}

// Without a town the alarm reads the system zone.
func TestAlarmWithoutATownUsesTheSystemZone(t *testing.T) {
	s, sp := alarmServer(t, settings.Alarm{Enabled: true, Time: "06:30", Slot: 2}, "")
	now := time.Date(2026, 10, 4, 6, 30, 0, 0, time.Local)
	if !s.checkAlarm(context.Background(), now) || len(sp.plays()) != 1 {
		t.Fatal("alarm did not fire at 06:30 system time")
	}
}

// A speaker without a display has no town; the zone the alarm was saved from stands in
// , and a town, once picked, still wins.
func TestAlarmWithoutATownUsesTheZoneItWasSavedIn(t *testing.T) {
	s, sp := alarmServer(t, settings.Alarm{Enabled: true, Time: "07:00", Slot: 2, Timezone: "Europe/Amsterdam"}, "")
	ctx := context.Background()
	if s.checkAlarm(ctx, time.Date(2026, 10, 4, 7, 0, 5, 0, time.UTC)) {
		t.Fatal("fired at 07:00 UTC, which is 09:00 where the alarm was set")
	}
	if !s.checkAlarm(ctx, time.Date(2026, 10, 4, 5, 0, 5, 0, time.UTC)) || len(sp.plays()) != 1 {
		t.Fatal("did not fire at 07:00 Amsterdam time")
	}
	if err := s.settings.SetDisplay(settings.Display{Timezone: "Asia/Tokyo"}); err != nil {
		t.Fatal(err)
	}
	if !s.checkAlarm(ctx, time.Date(2026, 10, 4, 22, 0, 5, 0, time.UTC)) {
		t.Fatal("the town's zone did not take precedence")
	}
}

func TestAlarmSkipsWhenDisabled(t *testing.T) {
	now := time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC)
	s, sp := alarmServer(t, settings.Alarm{Enabled: false, Time: "07:00", Slot: 2}, "Europe/Amsterdam")
	if s.checkAlarm(context.Background(), now) || len(sp.plays()) != 0 {
		t.Fatal("a disabled alarm went off")
	}
}

// The settings page shows the first stored preset once the alarm's own was cleared, so
// that is what must play, not a tone under a station name.
func TestAlarmOnAClearedPresetPlaysTheOneThePageShows(t *testing.T) {
	now := time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC)
	s, sp := alarmServer(t, settings.Alarm{Enabled: true, Time: "07:00", Slot: 5}, "Europe/Amsterdam")
	if !s.checkAlarm(context.Background(), now) {
		t.Fatal("an alarm on a cleared preset did not play")
	}
	if got := sp.plays(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("preset plays = %v, want [2]", got)
	}
}

func TestAlarmSlot(t *testing.T) {
	presets := []speaker.Preset{{Slot: 4}, {Slot: 2}}
	for _, c := range []struct {
		slot, want int
		ok         bool
		presets    []speaker.Preset
	}{
		{slot: 4, want: 4, ok: true, presets: presets},
		{slot: 1, want: 2, ok: true, presets: presets},
		{slot: 1, presets: nil},
	} {
		if got, ok := alarmSlot(c.slot, c.presets); got != c.want || ok != c.ok {
			t.Errorf("alarmSlot(%d) = %d, %v; want %d, %v", c.slot, got, ok, c.want, c.ok)
		}
	}
}

// An unset clock (1970, before the first NTP sync) does not decide when to wake.
func TestAlarmSkipsWhileTheClockIsNotSet(t *testing.T) {
	s, sp := alarmServer(t, settings.Alarm{Enabled: true, Time: "07:00", Slot: 2}, "")
	if s.checkAlarm(context.Background(), time.Date(1970, 1, 1, 7, 0, 0, 0, time.Local)) || len(sp.plays()) != 0 {
		t.Fatal("the alarm went off on a clock that was never set")
	}
}

// The alarm raises a speaker left near silent, and beeps when the preset does not play.
func TestAlarmRaisesTheVolumeAndFallsBackToATone(t *testing.T) {
	s, sp := alarmServer(t, settings.Alarm{Enabled: true, Time: "07:00", Slot: 5}, "")
	ctx := context.Background()
	if err := sp.RemovePreset(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := sp.SetVolume(ctx, 3); err != nil {
		t.Fatal(err)
	}
	s.SetBuiltinClip(AlarmClip, []byte("RIFF"), "audio/wav")
	s.ReconfigureForTest(func(o *Options) { o.Speaker.NotifyBaseURL = "http://127.0.0.1:1" })
	s.checkAlarm(ctx, time.Date(2026, 10, 4, 7, 0, 0, 0, time.Local))
	if v, _ := sp.Volume(ctx); v != alarmMinVolume {
		t.Fatalf("volume = %d, want %d", v, alarmMinVolume)
	}
	eventually(t, "fallback tone", func() bool {
		n := sp.notifications()
		return len(n) > 0 && n[0] == "http://127.0.0.1:1/api/speaker/audio/"+AlarmClip
	})
}

// Standby while the fallback waits dismisses the alarm: the fallback must not wake the
// speaker or beep afterwards.
func TestAlarmStandbyDismissesThePendingFallback(t *testing.T) {
	s, sp := alarmServer(t, settings.Alarm{Enabled: true, Time: "07:00", Slot: 2}, "")
	// The fallback never comes due here: the test is that standby cancels it. A short
	// wait raced enterStandby (snapshotRecent alone can take longer under -race on a
	// loaded runner) and the tone went off first.
	defer func(d time.Duration) { alarmFallbackAfter = d }(alarmFallbackAfter)
	alarmFallbackAfter = time.Hour
	s.SetBuiltinClip(AlarmClip, []byte("RIFF"), "audio/wav")
	s.ReconfigureForTest(func(o *Options) { o.Speaker.NotifyBaseURL = "http://127.0.0.1:1" })
	ctx := context.Background()
	if !s.checkAlarm(ctx, time.Date(2026, 10, 4, 7, 0, 0, 0, time.Local)) {
		t.Fatal("alarm did not fire")
	}
	s.alarmMu.Lock()
	pending := s.alarmStop != nil
	s.alarmMu.Unlock()
	if !pending {
		t.Fatal("no fallback pending after the alarm")
	}
	if err := s.enterStandby(ctx); err != nil {
		t.Fatal(err)
	}
	s.alarmMu.Lock()
	pending = s.alarmStop != nil
	s.alarmMu.Unlock()
	if pending {
		t.Fatal("standby left the alarm's fallback running")
	}
	if n := sp.notifications(); len(n) != 0 {
		t.Fatalf("fallback beeped after standby: %v", n)
	}
	if src := sp.StatusSnapshot().Source; src != "STANDBY" {
		t.Fatalf("fallback woke the speaker into %q", src)
	}
}

// The scheduler loop drives checkAlarm with the injected clock.
func TestAlarmLoopUsesTheInjectedClock(t *testing.T) {
	s, sp := alarmServer(t, settings.Alarm{Enabled: true, Time: "07:00", Slot: 2}, "Europe/Amsterdam")
	var ticks atomic.Int64
	s.clock = func() time.Time {
		ticks.Add(1)
		return time.Date(2026, 10, 4, 5, 0, 30, 0, time.UTC)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.runAlarm(ctx, 2*time.Millisecond); close(done) }()
	eventually(t, "alarm", func() bool { return len(sp.plays()) > 0 })
	// Several more ticks inside the same minute, counted rather than slept for.
	after := ticks.Load()
	eventually(t, "five more ticks", func() bool { return ticks.Load() >= after+5 })
	cancel()
	<-done
	if got := sp.plays(); len(got) != 1 {
		t.Fatalf("plays = %v, want exactly one", got)
	}
}

// ---- settings -------------------------------------------------------------------

// Spotify Connect and AirPlay are on by default; switching one restarts the receivers
// and leaves the other alone.
func TestSettingsSwitchIntegrations(t *testing.T) {
	s, _, _ := radioServer(t)
	restarts := 0
	s.ReconfigureForTest(func(o *Options) { o.Changed.Receivers = func() { restarts++ } })
	h := s.Handler()
	get := func() map[string]bool {
		t.Helper()
		var v struct {
			Integrations map[string]bool `json:"integrations"`
		}
		if err := json.Unmarshal(radioDo(t, h, http.MethodGet, "/api/settings", "").Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v.Integrations
	}
	if v := get(); !v["spotify"] || !v["airplay"] {
		t.Fatalf("defaults = %v, want both on", v)
	}
	if rec := radioDo(t, h, http.MethodPut, "/api/settings", `{"integrations":{"spotify":false}}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	if v := get(); v["spotify"] || !v["airplay"] {
		t.Fatalf("after switching Spotify off = %v", v)
	}
	if restarts != 1 {
		t.Fatalf("receiver restarts = %d, want 1", restarts)
	}
	radioDo(t, h, http.MethodPut, "/api/settings", `{"language":"nl"}`)
	if restarts != 1 {
		t.Fatalf("an unrelated setting restarted the receivers")
	}
}

func TestSettingsCarryAlarmAndMultiroomPreferences(t *testing.T) {
	s, _, dir := radioServer(t)
	h := s.Handler()
	type view struct {
		Language  string          `json:"language"`
		Alarm     settings.Alarm  `json:"alarm"`
		Multiroom map[string]bool `json:"multiroom"`
	}
	get := func() view {
		t.Helper()
		rec := radioDo(t, h, http.MethodGet, "/api/settings", "")
		var v view
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	v := get()
	if v.Alarm != settings.DefaultAlarm || len(v.Multiroom) != 2 || !v.Multiroom["keepGroups"] || v.Multiroom["syncVolume"] {
		t.Fatalf("defaults = %+v", v)
	}

	rec := radioDo(t, h, http.MethodPut, "/api/settings", `{"alarm":{"enabled":true,"time":"06:15","timezone":"Europe/Amsterdam"},"multiroom":{"keepGroups":false,"syncVolume":true}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	v = get()
	if v.Alarm != (settings.Alarm{Enabled: true, Time: "06:15", Slot: 1, Timezone: "Europe/Amsterdam"}) || v.Multiroom["keepGroups"] || !v.Multiroom["syncVolume"] {
		t.Fatalf("after PUT = %+v", v)
	}
	// One switch at a time leaves the other alone.
	radioDo(t, h, http.MethodPut, "/api/settings", `{"multiroom":{"keepGroups":true}}`)
	if v = get(); !v.Multiroom["keepGroups"] || !v.Multiroom["syncVolume"] {
		t.Fatalf("partial multiroom patch = %+v", v.Multiroom)
	}

	// An invalid alarm stores nothing, not even the valid fields beside it.
	for _, bad := range []string{
		`{"language":"nl","alarm":{"time":"24:00"}}`,
		`{"language":"nl","alarm":{"time":"7:00"}}`,
		`{"language":"nl","alarm":{"slot":7}}`,
		`{"language":"nl","alarm":{"slot":0}}`,
		`{"language":"nl","alarm":{"timezone":"Mars/Olympus"}}`,
	} {
		if rec := radioDo(t, h, http.MethodPut, "/api/settings", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", bad, rec.Code)
		}
	}
	if v = get(); v.Language != "en" || v.Alarm.Time != "06:15" {
		t.Fatalf("a rejected PUT stored something: %+v", v)
	}

	// On disk keepGroups is stored as its inverse; the API never shows that.
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	var disk struct {
		Multiroom map[string]bool `json:"multiroom"`
	}
	if err != nil || json.Unmarshal(data, &disk) != nil || len(disk.Multiroom) != 2 ||
		disk.Multiroom["dissolveGroups"] || !disk.Multiroom["syncVolume"] {
		t.Fatalf("settings file = %s (%v)", data, err)
	}
}

// ---- multiroom preferences ------------------------------------------------------

// fakeFollower stands in for another speaker's soundtapd: a volume and a follow target.
// It fails the test on a body it cannot read, and records whether two requests to it
// were ever in flight at once.
type fakeFollower struct {
	t         *testing.T
	mu        sync.Mutex
	volume    int
	posts     []string // path + forwarded header
	following string
	active    atomic.Int32
	overlap   atomic.Bool
}

func (f *fakeFollower) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.active.Add(1) > 1 {
		f.overlap.Store(true)
	}
	defer f.active.Add(-1)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/volume":
		writeJSON(w, 200, map[string]int{"volume": f.volume})
	case r.Method == http.MethodPost && r.URL.Path == "/api/volume":
		var body struct{ Volume *int }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Volume == nil {
			f.t.Errorf("follower: unreadable volume body: %v", err)
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		f.volume = *body.Volume
		f.posts = append(f.posts, r.URL.Path+" "+r.Header.Get(volumeSyncHeader))
		writeJSON(w, 200, map[string]int{"volume": f.volume})
	case r.Method == http.MethodPost && r.URL.Path == "/api/multiroom/follow":
		var body struct{ Host *string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("follower: unreadable follow body: %v", err)
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if body.Host != nil {
			f.following = *body.Host
		}
		f.posts = append(f.posts, r.URL.Path)
		writeJSON(w, 200, map[string]string{"following": f.following})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeFollower) snapshot() (int, []string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.volume, append([]string(nil), f.posts...), f.following
}

// groupServer is a leader with two followers it can reach, one that dropped out, and a
// snapclient that is not a SoundTap speaker at all.
func groupServer(t *testing.T, prefs settings.Multiroom) (*Server, *countingBackend, *fakeFollower, *fakeFollower) {
	t.Helper()
	s, sp, _ := radioServer(t)
	if err := s.settings.SetMultiroom(prefs); err != nil {
		t.Fatal(err)
	}
	a, b := &fakeFollower{t: t, volume: 50, following: "leader"}, &fakeFollower{t: t, volume: 95, following: "leader"}
	for id, f := range map[string]*fakeFollower{"SN-A": a, "SN-B": b} {
		srv := httptest.NewServer(f)
		t.Cleanup(srv.Close)
		host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
		s.SetDevice(Device{ID: id, Name: id, IP: host, Port: atoiPort(t, port)})
	}
	client := func(id string, connected bool) snapcast.ClientInfo {
		var c snapcast.ClientInfo
		c.ID, c.Connected = id, connected
		return c
	}
	status := snapcast.Status{Groups: []snapcast.Group{
		{ID: "g1", Clients: []snapcast.ClientInfo{client("soundtap-SELF", true), client("soundtap-SN-A", true)}},
		{ID: "g2", Clients: []snapcast.ClientInfo{client("soundtap-SN-B", true), client("soundtap-SN-GONE", false), client("kodi", true)}},
	}}
	s.ReconfigureForTest(func(o *Options) {
		o.Multiroom.Backend, o.Multiroom.LocalID = &fakeMultiroom{statuses: []snapcast.Status{status}}, "soundtap-SELF"
	})
	if err := sp.SetVolume(context.Background(), 30); err != nil {
		t.Fatal(err)
	}
	return s, sp, a, b
}

func TestSyncVolumeMovesFollowersByTheSameStep(t *testing.T) {
	s, _, a, b := groupServer(t, settings.Multiroom{SyncVolume: true})
	h := s.Handler()
	if rec := radioDo(t, h, http.MethodPost, "/api/volume", `{"volume":40,"group":true}`); rec.Code != http.StatusOK {
		t.Fatalf("POST volume = %d %s", rec.Code, rec.Body)
	}
	s.group.WaitPeers()
	va, pa, _ := a.snapshot()
	vb, pb, _ := b.snapshot()
	if va != 60 || vb != 100 {
		t.Fatalf("followers = %d, %d; want 60 and 100 (clamped)", va, vb)
	}
	// Each forwarded change is marked, so the follower does not pass it on again.
	if len(pa) != 1 || pa[0] != "/api/volume 1" || len(pb) != 1 || pb[0] != "/api/volume 1" {
		t.Fatalf("follower requests = %v, %v", pa, pb)
	}
}

// A leader at its bound still passes the group's step on: the slider asks for 110, the
// leader stays at 100 and the followers move the full +10.
func TestSyncVolumeStepsPastASaturatedLeader(t *testing.T) {
	s, sp, a, b := groupServer(t, settings.Multiroom{SyncVolume: true})
	h := s.Handler()
	if err := sp.SetVolume(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.volume = 20
	b.mu.Unlock()
	if rec := radioDo(t, h, http.MethodPost, "/api/volume", `{"volume":110,"group":true}`); rec.Code != http.StatusOK {
		t.Fatalf("POST volume = %d %s", rec.Code, rec.Body)
	}
	s.group.WaitPeers()
	lead, _ := sp.Volume(context.Background())
	va, _, _ := a.snapshot()
	vb, _, _ := b.snapshot()
	if lead != 100 || va != 60 || vb != 30 {
		t.Fatalf("leader %d, followers %d and %d; want 100, 60 and 30", lead, va, vb)
	}
}

// A drag past an end and back keeps the balance: the leader at 30 and followers at 50
// and 95 go down 60 (the leader and the first follower stop at 0) and up 60 again, and
// all are back where they started instead of flattened against the bound.
func TestSyncVolumeDragPastAnEndKeepsTheBalance(t *testing.T) {
	s, sp, a, b := groupServer(t, settings.Multiroom{SyncVolume: true})
	h := s.Handler()
	for _, level := range []int{-30, 30} {
		body := `{"volume":` + strconv.Itoa(level) + `,"group":true}`
		if rec := radioDo(t, h, http.MethodPost, "/api/volume", body); rec.Code != http.StatusOK {
			t.Fatalf("POST volume %d = %d %s", level, rec.Code, rec.Body)
		}
		s.group.WaitPeers()
	}
	lead, _ := sp.Volume(context.Background())
	va, _, _ := a.snapshot()
	vb, _, _ := b.snapshot()
	if lead != 30 || va != 50 || vb != 95 {
		t.Fatalf("leader %d, followers %d and %d; want 30, 50 and 95", lead, va, vb)
	}
}

func TestSyncVolumeDoesNotPropagate(t *testing.T) {
	group := `{"volume":40,"group":true}`
	for _, tc := range []struct {
		name   string
		prefs  settings.Multiroom
		body   string
		header []string
		follow bool
	}{
		{name: "per-room slider (no group flag)", prefs: settings.Multiroom{SyncVolume: true}, body: `{"volume":40}`},
		{name: "group flag false", prefs: settings.Multiroom{SyncVolume: true}, body: `{"volume":40,"group":false}`},
		{name: "forwarded request", prefs: settings.Multiroom{SyncVolume: true}, body: group, header: []string{volumeSyncHeader, "1"}},
		{name: "sync off", prefs: settings.Multiroom{}, body: group},
		{name: "this speaker follows another", prefs: settings.Multiroom{SyncVolume: true}, body: group, follow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, sp, a, b := groupServer(t, tc.prefs)
			if tc.follow {
				if err := os.WriteFile(s.multiroomServerPath(), []byte("10.0.0.9\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			rec := radioDo(t, s.Handler(), http.MethodPost, "/api/volume", tc.body, tc.header...)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST volume = %d", rec.Code)
			}
			s.group.WaitPeers()
			if v, _ := sp.Volume(context.Background()); v != 40 {
				t.Fatalf("own volume = %d, want 40", v)
			}
			va, _, _ := a.snapshot()
			vb, _, _ := b.snapshot()
			if va != 50 || vb != 95 {
				t.Fatalf("followers moved to %d, %d", va, vb)
			}
		})
	}
}

func TestStandbyReleasesFollowersOnlyWhenGroupsAreNotKept(t *testing.T) {
	for _, path := range []string{"/api/stop", "/api/power"} {
		t.Run("dissolve "+path, func(t *testing.T) {
			s, sp, a, b := groupServer(t, settings.Multiroom{DissolveGroups: true})
			playStation(t, sp)
			if rec := radioDo(t, s.Handler(), http.MethodPost, path, `{"on":false}`); rec.Code != http.StatusOK {
				t.Fatalf("%s = %d %s", path, rec.Code, rec.Body)
			}
			s.group.WaitPeers()
			for name, f := range map[string]*fakeFollower{"A": a, "B": b} {
				if _, posts, following := f.snapshot(); following != "" || len(posts) != 1 || posts[0] != "/api/multiroom/follow" {
					t.Fatalf("follower %s: following %q after %v", name, following, posts)
				}
			}
		})
	}
	t.Run("sleep timer dissolves too", func(t *testing.T) {
		s, sp, a, _ := groupServer(t, settings.Multiroom{DissolveGroups: true})
		s.sleepUnit = 5 * time.Millisecond
		playStation(t, sp)
		radioDo(t, s.Handler(), http.MethodPost, "/api/sleep", `{"minutes":1}`)
		eventually(t, "follower released", func() bool { _, _, f := a.snapshot(); return f == "" })
		s.group.WaitPeers()
	})
	t.Run("keep", func(t *testing.T) {
		s, sp, a, b := groupServer(t, settings.Multiroom{})
		playStation(t, sp)
		radioDo(t, s.Handler(), http.MethodPost, "/api/stop", "")
		s.group.WaitPeers()
		for _, f := range []*fakeFollower{a, b} {
			if _, posts, following := f.snapshot(); following != "leader" || len(posts) != 0 {
				t.Fatalf("a kept group was touched: %q %v", following, posts)
			}
		}
	})
}

// A follower discovery has no address for is still released: snapserver knows where its
// client connected from. Without that, a leader's standby left it following.
func TestStandbyReleasesAFollowerDiscoveryMissed(t *testing.T) {
	s, sp, _ := radioServer(t)
	if err := s.settings.SetMultiroom(settings.Multiroom{DissolveGroups: true}); err != nil {
		t.Fatal(err)
	}
	f := &fakeFollower{t: t, following: "leader"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	defer func(p string) { downstreamPort = p }(downstreamPort)
	downstreamPort = srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	var c snapcast.ClientInfo
	c.ID, c.Connected, c.Host.IP = "soundtap-SN-A", true, "127.0.0.1"
	s.ReconfigureForTest(func(o *Options) {
		o.Multiroom.Backend, o.Multiroom.LocalID = &fakeMultiroom{statuses: []snapcast.Status{{Groups: []snapcast.Group{
			{ID: "g1", Clients: []snapcast.ClientInfo{c}}}}}}, "soundtap-SELF"
	})
	playStation(t, sp)
	if rec := radioDo(t, s.Handler(), http.MethodPost, "/api/power", `{"on":false}`); rec.Code != http.StatusOK {
		t.Fatalf("power off = %d %s", rec.Code, rec.Body)
	}
	s.group.WaitPeers()
	if _, posts, following := f.snapshot(); following != "" {
		t.Fatalf("follower still follows %q after %v", following, posts)
	}
}

// A drag of the group slider is a burst of overlapping requests. However they fall, each
// follower ends up moved by the sum of the leader's steps -- which is the leader's net
// change -- and never sees two of the leader's calls at once.
func TestSyncVolumeCoalescesConcurrentSteps(t *testing.T) {
	s, sp, a, b := groupServer(t, settings.Multiroom{SyncVolume: true})
	b.mu.Lock()
	b.volume = 20
	b.mu.Unlock()
	h := s.Handler()
	// Leader starts at 30; every requested level stays in 31..80, so neither follower
	// (50 and 20, each moved by level-30) is ever clamped part way through.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		level := 31 + (i*37)%50
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := `{"volume":` + strconv.Itoa(level) + `,"group":true}`
			if rec := radioDo(t, h, http.MethodPost, "/api/volume", body); rec.Code != http.StatusOK {
				t.Errorf("POST %s = %d %s", body, rec.Code, rec.Body)
			}
		}()
	}
	wg.Wait()
	s.group.WaitPeers()
	final, _ := sp.Volume(context.Background())
	sum := final - 30 // the steps telescope: each one is measured from the level before it
	va, _, _ := a.snapshot()
	vb, _, _ := b.snapshot()
	if va != min(max(50+sum, 0), 100) || vb != min(max(20+sum, 0), 100) {
		t.Fatalf("leader moved %+d to %d; followers at %d and %d, want %d and %d", sum, final, va, vb, 50+sum, 20+sum)
	}
	if a.overlap.Load() || b.overlap.Load() {
		t.Fatal("a follower had two of the leader's volume calls in flight at once")
	}
}

// Every way into standby, not only the web API's own off switches, cancels the sleep
// timer and lets the followers go when groups are not kept.
func TestBackendStandbyCancelsTimerAndReleasesFollowers(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		start func(*countingBackend)
		off   func(*testing.T, *Server, *countingBackend)
	}{
		{"POWER key", playStationNoT, func(t *testing.T, _ *Server, sp *countingBackend) { sp.ApplyKey("POWER") }},
		{"auto-off", playStationNoT, func(t *testing.T, _ *Server, sp *countingBackend) {
			if err := sp.EnterStandby(ctx); err != nil {
				t.Fatal(err)
			}
		}},
		{"POST /api/source STANDBY", playStationNoT, func(t *testing.T, s *Server, _ *countingBackend) {
			req := httptest.NewRequest(http.MethodPost, "/api/source", strings.NewReader(`{"source":"STANDBY"}`))
			req.Host, req.RemoteAddr = "127.0.0.1:8080", "127.0.0.1:40000"
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST /api/source = %d %s", rec.Code, rec.Body)
			}
		}},
		{"source session ends", func(sp *countingBackend) { _ = sp.SetSource(ctx, "SPOTIFY") },
			func(t *testing.T, _ *Server, sp *countingBackend) {
				if err := sp.ReleaseSource(ctx, "SPOTIFY"); err != nil {
					t.Fatal(err)
				}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, sp, a, b := groupServer(t, settings.Multiroom{DissolveGroups: true})
			tc.start(sp)
			h := s.Handler()
			radioDo(t, h, http.MethodPost, "/api/sleep", `{"minutes":30}`)
			if getSleepView(t, h).Minutes != 30 {
				t.Fatal("timer not set")
			}
			tc.off(t, s, sp)
			if !sp.IsStandby() {
				t.Fatal("speaker not in standby")
			}
			if v := getSleepView(t, h); v.Minutes != 0 {
				t.Fatalf("timer survived standby: %+v", v)
			}
			s.group.WaitPeers()
			for name, f := range map[string]*fakeFollower{"A": a, "B": b} {
				if _, _, following := f.snapshot(); following != "" {
					t.Fatalf("follower %s still follows %q", name, following)
				}
			}
		})
	}
	// Leaving standby is not entering it.
	t.Run("wake", func(t *testing.T) {
		s, sp, a, _ := groupServer(t, settings.Multiroom{DissolveGroups: true})
		playStation(t, sp)
		sp.ApplyKey("POWER")
		s.group.WaitPeers()
		_, posts, _ := a.snapshot()
		sp.ApplyKey("POWER") // wake
		s.group.WaitPeers()
		if _, after, _ := a.snapshot(); len(after) != len(posts) {
			t.Fatalf("waking called the followers: %v", after[len(posts):])
		}
	})
}

func playStationNoT(sp *countingBackend) {
	_ = sp.Select(context.Background(), "TUNEIN", "stationurl", "/v1/playback/station/s1", "Radio 1", "", "")
}

// gatedStandby lets a test decide, call by call, whether standby works.
type gatedStandby struct {
	*countingBackend
	calls   chan int
	results chan error
	n       atomic.Int32
}

func (g *gatedStandby) EnterStandby(ctx context.Context) error {
	g.calls <- int(g.n.Add(1))
	if err := <-g.results; err != nil {
		return err
	}
	return g.countingBackend.EnterStandby(ctx)
}

func gatedServer(t *testing.T) (*Server, *countingBackend, *gatedStandby) {
	t.Helper()
	var g *gatedStandby
	s, sp, _ := radioServerWith(t, func(sp *countingBackend) Backend {
		g = &gatedStandby{countingBackend: sp, calls: make(chan int), results: make(chan error)}
		return g
	})
	s.sleepUnit = time.Millisecond
	s.sleepRetry = time.Millisecond
	return s, sp, g
}

func recvCall(t *testing.T, g *gatedStandby, want int) {
	t.Helper()
	select {
	case n := <-g.calls:
		if n != want {
			t.Fatalf("standby attempt %d, want %d", n, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("standby attempt %d never came", want)
	}
}

// A standby that fails is tried again, and the timer shows as running until it works.
func TestSleepTimerRetriesAFailedStandby(t *testing.T) {
	s, sp, g := gatedServer(t)
	h := s.Handler()
	playStation(t, sp)
	radioDo(t, h, http.MethodPost, "/api/sleep", `{"minutes":5}`)
	busy := errors.New("backend busy")
	for attempt := 1; attempt <= 2; attempt++ {
		recvCall(t, g, attempt)
		if v := getSleepView(t, h); v.Minutes != 5 {
			t.Fatalf("attempt %d: timer not shown as running: %+v", attempt, v)
		}
		g.results <- busy
	}
	recvCall(t, g, 3)
	g.results <- nil
	eventually(t, "timer cleared", func() bool { return getSleepView(t, h).Minutes == 0 })
	if !sp.IsStandby() {
		t.Fatal("speaker not in standby after the retry worked")
	}
}

// After sleeptimer.Attempts failures the timer gives up and is gone; it does not try forever.
func TestSleepTimerGivesUpAfterBoundedRetries(t *testing.T) {
	s, sp, g := gatedServer(t)
	h := s.Handler()
	playStation(t, sp)
	radioDo(t, h, http.MethodPost, "/api/sleep", `{"minutes":5}`)
	for attempt := 1; attempt <= sleeptimer.Attempts; attempt++ {
		recvCall(t, g, attempt)
		g.results <- errors.New("backend down")
	}
	eventually(t, "timer cleared", func() bool { return getSleepView(t, h).Minutes == 0 })
	select {
	case n := <-g.calls:
		t.Fatalf("attempt %d after giving up", n)
	default:
	}
	if sp.IsStandby() {
		t.Fatal("standby happened although every attempt failed")
	}
}

// raceGate holds the sleep timer's standby at one point -- the recent-list snapshot
// (NowPlaying) or, with inStandby, the backend's EnterStandby -- until the test has
// cancelled or replaced the timer. A held EnterStandby then goes through whatever its
// context says, as a backend already past its own check would.
type raceGate struct {
	*countingBackend
	inStandby bool
	armed     atomic.Bool
	held      chan struct{}
	release   chan struct{}
}

func (g *raceGate) hold() {
	if g.armed.CompareAndSwap(true, false) {
		g.held <- struct{}{}
		<-g.release
	}
}

func (g *raceGate) NowPlaying(ctx context.Context) (*speaker.NowPlaying, error) {
	if !g.inStandby {
		g.hold()
	}
	return g.countingBackend.NowPlaying(ctx)
}

func (g *raceGate) EnterStandby(ctx context.Context) error {
	if !g.inStandby {
		return g.countingBackend.EnterStandby(ctx)
	}
	g.hold()
	return g.countingBackend.EnterStandby(context.Background())
}

// sleepRace runs the sleep timer into the gate, calls during while it is held, and
// returns once the timer's standby attempt has finished.
func sleepRace(t *testing.T, inStandby bool, during func(h http.Handler)) (*Server, *countingBackend) {
	t.Helper()
	var g *raceGate
	s, sp, _ := radioServerWith(t, func(sp *countingBackend) Backend {
		g = &raceGate{countingBackend: sp, inStandby: inStandby, held: make(chan struct{}), release: make(chan struct{})}
		return g
	})
	t.Cleanup(s.sleep.Cancel)
	playStation(t, sp)
	g.armed.Store(true)
	done := make(chan struct{})
	s.sleep.Set(1, time.Millisecond, time.Hour, func(ctx context.Context, attempt int) error {
		defer close(done)
		return s.sleepExpired(ctx, attempt)
	})
	select {
	case <-g.held:
	case <-time.After(3 * time.Second):
		t.Fatal("the timer never reached standby")
	}
	during(s.Handler())
	close(g.release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the timer's standby never finished")
	}
	return s, sp
}

// A sleep timer cancelled or replaced while its standby is recording the recent list
// does not turn the speaker off.
func TestSleepTimerCancelledDuringSnapshotStaysOn(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
	}{{`{"minutes":0}`, 0}, {`{"minutes":60}`, 60}} {
		t.Run(tc.body, func(t *testing.T) {
			s, sp := sleepRace(t, false, func(h http.Handler) {
				if rec := radioDo(t, h, http.MethodPost, "/api/sleep", tc.body); rec.Code != http.StatusOK {
					t.Fatalf("POST %s = %d %s", tc.body, rec.Code, rec.Body)
				}
			})
			if sp.IsStandby() {
				t.Fatal("the old timer still put the speaker in standby")
			}
			if v := getSleepView(t, s.Handler()); v.Minutes != tc.want {
				t.Fatalf("timer = %+v, want %d minutes", v, tc.want)
			}
		})
	}
}

// A timer set while the old one's standby was already under way is not cancelled by
// that standby: only the timer that ran out is.
func TestSleepTimerStandbyKeepsAReplacement(t *testing.T) {
	s, sp := sleepRace(t, true, func(h http.Handler) {
		if rec := radioDo(t, h, http.MethodPost, "/api/sleep", `{"minutes":60}`); rec.Code != http.StatusOK {
			t.Fatalf("POST = %d %s", rec.Code, rec.Body)
		}
	})
	if !sp.IsStandby() {
		t.Fatal("the held standby did not go through")
	}
	if v := getSleepView(t, s.Handler()); v.Minutes != 60 {
		t.Fatalf("timer = %+v, want the replacement's 60 minutes", v)
	}
}

// A clock change that skips the alarm minute fires it in the first minute after the
// jump; one that repeats it fires it once.
func TestAlarmAcrossClockChanges(t *testing.T) {
	s, sp := alarmServer(t, settings.Alarm{Enabled: true, Time: "02:30", Slot: 2}, "Europe/Amsterdam")
	ctx := context.Background()
	utc := func(mo time.Month, d, h, m, sec int) time.Time { return time.Date(2026, mo, d, h, m, sec, 0, time.UTC) }
	// 29 March 2026: 02:00 CET (01:00 UTC) becomes 03:00 CEST; 02:30 never happens.
	if s.checkAlarm(ctx, utc(time.March, 29, 0, 59, 30)) {
		t.Fatal("fired at 01:59 local, before the jump")
	}
	if !s.checkAlarm(ctx, utc(time.March, 29, 1, 0, 10)) {
		t.Fatal("did not fire at 03:00 local, the first minute after the skipped alarm")
	}
	if s.checkAlarm(ctx, utc(time.March, 29, 1, 0, 50)) || s.checkAlarm(ctx, utc(time.March, 29, 1, 1, 10)) {
		t.Fatal("fired twice for a skipped alarm")
	}
	// 25 October 2026: 03:00 CEST (01:00 UTC) becomes 02:00 CET; 02:30 happens twice.
	if !s.checkAlarm(ctx, utc(time.October, 25, 0, 30, 10)) {
		t.Fatal("did not fire at the first 02:30")
	}
	if s.checkAlarm(ctx, utc(time.October, 25, 1, 30, 10)) {
		t.Fatal("fired again at the repeated 02:30")
	}
	if got := sp.plays(); len(got) != 2 {
		t.Fatalf("plays = %v, want two", got)
	}
	// An ordinary day: the minute after the alarm is not "after a jump".
	if !alarmDue(time.Date(2026, 5, 1, 7, 0, 59, 0, time.UTC), "07:00") || alarmDue(time.Date(2026, 5, 1, 7, 1, 0, 0, time.UTC), "07:00") {
		t.Fatal("alarmDue wrong on a day without a clock change")
	}
}

// Concurrent PUTs of different fields all land: each is one atomic read-merge-write.
func TestConcurrentSettingsPutsDoNotLoseFields(t *testing.T) {
	s, _, _ := radioServer(t)
	h := s.Handler()
	puts := []string{
		`{"language":"nl"}`,
		`{"autoOff":30}`,
		`{"alarm":{"enabled":true}}`,
		`{"alarm":{"slot":4}}`,
		`{"multiroom":{"keepGroups":false}}`,
		`{"multiroom":{"syncVolume":true}}`,
		`{"mqtt":{"host":"broker"}}`,
		`{"mqtt":{"port":1884}}`,
	}
	var wg sync.WaitGroup
	for _, body := range puts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rec := radioDo(t, h, http.MethodPut, "/api/settings", body); rec.Code != http.StatusOK {
				t.Errorf("PUT %s = %d %s", body, rec.Code, rec.Body)
			}
		}()
	}
	wg.Wait()
	got := s.settings.Get()
	a := got.AlarmConfig()
	if got.Language != "nl" || got.Audio.AutoOff != 30 || !a.Enabled || a.Slot != 4 ||
		got.Multiroom.KeepGroups() || !got.Multiroom.SyncVolume || got.MQTT.Host != "broker" || got.MQTT.Port != 1884 {
		t.Fatalf("a concurrent PUT was lost: %+v", got)
	}
	big := `{"language":"` + strings.Repeat("x", settingsMaxBody) + `"}`
	if rec := radioDo(t, h, http.MethodPut, "/api/settings", big); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized PUT = %d, want 400", rec.Code)
	}
	big = `{"volume":40,"source":"` + strings.Repeat("x", volumeMaxBody) + `"}`
	if rec := radioDo(t, h, http.MethodPost, "/api/volume", big); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized volume POST = %d, want 400", rec.Code)
	}
}

// ---- Spotify pause / resume -----------------------------------------------------

func TestSpotifyPauseIsReportedByNow(t *testing.T) {
	s, sp, _ := radioServer(t)
	var mu sync.Mutex
	var commands []string
	var ignore atomic.Bool
	ctl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		commands = append(commands, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		// What librespot's hook does once the command took effect.
		if !ignore.Load() {
			go sp.SetPaused("SPOTIFY", r.URL.Path == "/pause")
		}
	}))
	t.Cleanup(ctl.Close)
	s.AttachForTest(func(h *Attached) {
		h.Spotify.Client, h.Spotify.Account = &spotify.Client{Ctl: ctl.URL, HTTP: ctl.Client()}, nil
	})
	if err := sp.SetSource(context.Background(), "SPOTIFY"); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	playStatus := func() string {
		t.Helper()
		var np struct{ PlayStatus string }
		_ = json.Unmarshal(radioDo(t, h, http.MethodGet, "/api/now", "").Body.Bytes(), &np)
		return np.PlayStatus
	}
	rec := radioDo(t, h, http.MethodPost, "/api/spotify/pause", "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("pause = %d %s", rec.Code, rec.Body)
	}
	if got := playStatus(); got != "PAUSE_STATE" {
		t.Fatalf("playStatus after pause = %q", got)
	}
	if rec := radioDo(t, h, http.MethodPost, "/api/spotify/resume", ""); rec.Code != http.StatusOK {
		t.Fatalf("resume = %d %s", rec.Code, rec.Body)
	}
	if got := playStatus(); got != "PLAY_STATE" {
		t.Fatalf("playStatus after resume = %q", got)
	}
	mu.Lock()
	if len(commands) != 2 || commands[0] != "POST /pause" || commands[1] != "POST /play" {
		t.Fatalf("librespot saw %v, want one pause and one play", commands)
	}
	mu.Unlock()

	// librespot accepts a command and drops it while Not Active.
	ignore.Store(true)
	defer func(d time.Duration) { spotifyAckWait = d }(spotifyAckWait)
	spotifyAckWait = 200 * time.Millisecond
	if rec := radioDo(t, h, http.MethodPost, "/api/spotify/pause", ""); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "spotify_command_ignored") {
		t.Fatalf("ignored pause = %d %s, want 409 spotify_command_ignored", rec.Code, rec.Body)
	}
	if got := playStatus(); got != "PLAY_STATE" {
		t.Fatalf("playStatus after an ignored pause = %q", got)
	}
}

// A backup restored with the alarm off cancels an alarm already counting down to its
// fallback, as turning it off in the settings does.
func TestRestoreDisabledAlarmDismissesPendingFallback(t *testing.T) {
	s, _, _ := radioServer(t)
	if err := s.settings.SetAlarm(settings.Alarm{Enabled: true, Time: "07:00", Slot: 1}); err != nil {
		t.Fatal(err)
	}
	run := s.newAlarmRun(context.Background())
	defer s.dismissAlarm()
	r := radioDo(t, s.Handler(), http.MethodPost, "/api/restore",
		`{"kind":"soundtap-backup","version":1,"settings":{"alarm":{"enabled":false,"time":"07:00","slot":1}},"presets":[]}`)
	if r.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", r.Code, r.Body)
	}
	if run.Err() == nil {
		t.Fatal("alarm restored as off, but its pending fallback is still live")
	}
}

// The alarm's raise lasts only as long as the alarm: standby puts the owner's level
// back, unless they changed the volume while it rang.
func TestAlarmGivesBackTheVolumeItRaised(t *testing.T) {
	for _, tc := range []struct {
		name          string
		turnedTo, end int
	}{
		{"untouched", 0, 3},
		{"turned up while ringing", 25, 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func(d time.Duration) { alarmFallbackAfter = d }(alarmFallbackAfter)
			alarmFallbackAfter = time.Hour
			s, sp := alarmServer(t, settings.Alarm{Enabled: true, Time: "07:00", Slot: 2}, "")
			ctx := context.Background()
			if err := sp.SetVolume(ctx, 3); err != nil {
				t.Fatal(err)
			}
			if !s.checkAlarm(ctx, time.Date(2026, 10, 4, 7, 0, 0, 0, time.Local)) {
				t.Fatal("alarm did not fire")
			}
			if tc.turnedTo != 0 {
				if err := sp.SetVolume(ctx, tc.turnedTo); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.enterStandby(ctx); err != nil {
				t.Fatal(err)
			}
			eventually(t, "volume after standby", func() bool {
				v, _ := sp.Volume(ctx)
				return v == tc.end
			})
		})
	}
}

// An idle timeout too long for time.Duration is refused, not stored as a silent "off".
func TestSettingsRefuseAnAutoOffBeyondAWeek(t *testing.T) {
	s, _, _ := radioServer(t)
	h := s.Handler()
	if rec := radioDo(t, h, http.MethodPut, "/api/settings", `{"autoOff":2147483647}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT autoOff=2147483647 = %d, want 400", rec.Code)
	}
	if rec := radioDo(t, h, http.MethodPut, "/api/settings", `{"autoOff":10080}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT autoOff=10080 = %d %s, want 200", rec.Code, rec.Body)
	}
	if got := settings.NormalizeAutoOff(1 << 30); got != settings.MaxAutoOff {
		t.Fatalf("NormalizeAutoOff(1<<30) = %d, want %d", got, settings.MaxAutoOff)
	}
}

func TestValidStationTakesAGuideIDOrAStreamURL(t *testing.T) {
	ctx := context.Background()
	for id, want := range map[string]bool{
		"s6712":                         true,
		"http://192.0.2.10:8000/live":   true,
		"https://192.0.2.10/live.mp3":   true,
		"http://127.0.0.1/api/reboot":   false,
		"http://169.254.169.254/latest": false,
		"file:///etc/passwd":            false,
		"http://192.0.2.10/" + strings.Repeat("a", maxStreamURL): false,
	} {
		if got := validStation(ctx, id); got != want {
			t.Errorf("validStation(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestValidOwnStream(t *testing.T) {
	for id, want := range map[string]bool{
		"http://example.com/x":       true,
		"https://example.com:8443/x": true,
		"http://example.com:99999/x": false,
		"http://example.com:0/x":     false,
		"http://example.com/ x":      false,
		"ftp://example.com/x":        false,
	} {
		if got := validOwnStream(id); got != want {
			t.Errorf("validOwnStream(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestFollowRefusesAMistypedKey(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/follow", strings.NewReader(`{"leader":"10.0.1.135"}`))
	rec := httptest.NewRecorder()
	s.postMultiroomFollow(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: a typo must not leave the group", rec.Code)
	}
}
