// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stein155/soundtap/app/internal/panel"
	"github.com/stein155/soundtap/app/internal/web"
)

func testConfirmer(t *testing.T) *deviceConfirm {
	t.Helper()
	old := confirmTokenPath
	confirmTokenPath = filepath.Join(t.TempDir(), "confirm-token")
	t.Cleanup(func() { confirmTokenPath = old })
	c := &deviceConfirm{window: time.Minute, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	c.rotate()
	return c
}

// useConfirmer makes c the one the button handlers confirm through.
func useConfirmer(t *testing.T, c *deviceConfirm) {
	t.Helper()
	old := confirmer
	confirmer = c
	t.Cleanup(func() { confirmer = old })
}

// panelHandler is a handler for the speaker's own keys, as the keypad and gpio-keys
// readers make it.
func panelHandler() (*buttonHandler, *fakeActor) {
	h, a := testHandler()
	h.confirms = true
	return h, a
}

// panelShows records what the confirmation puts on the ST20 panel.
type panelShows struct {
	mu    sync.Mutex
	texts []string
}

func (p *panelShows) show(text string, _ time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.texts = append(p.texts, text)
}

func (p *panelShows) all() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.texts...)
}

// wait starts a request that waits for the hold and returns where its answer lands.
func wait(c *deviceConfirm, what string) <-chan error {
	done := make(chan error, 1)
	go func() { done <- c.confirm(context.Background(), what, "") }()
	synctest.Wait()
	return done
}

func answered(done <-chan error) (error, bool) {
	select {
	case err := <-done:
		return err, true
	default:
		return nil, false
	}
}

// A key held for confirmHold confirms the waiting request, and does nothing else: no
// preset plays or stores although the hold passes holdToStore, and the release is
// swallowed.
func TestConfirmByHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := testConfirmer(t)
		useConfirmer(t, c)
		h, a := panelHandler()
		done := wait(c, "POST /api/factory-reset")
		if !confirmPending.Load() {
			t.Fatal("no pending confirmation while a request waits")
		}
		h.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold - time.Millisecond)
		synctest.Wait()
		if _, ok := answered(done); ok {
			t.Fatal("confirmed before the hold was long enough")
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if err, ok := answered(done); !ok || err != nil {
			t.Fatalf("after the hold: answered=%v err=%v, want confirmed", ok, err)
		}
		if confirmPending.Load() {
			t.Fatal("still pending after the hold")
		}
		time.Sleep(time.Second)
		h.Handle(1, false, "PRESET_1")
		synctest.Wait()
		if keys, stored := a.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys=%v stored=%v, want nothing from the confirming key", keys, stored)
		}
		// The key works as usual again afterwards.
		h.Handle(1, true, "PRESET_1")
		h.Handle(1, false, "PRESET_1")
		if a.count("PRESET_1") != 1 {
			t.Fatal("the next press of the key did not play its preset")
		}
	})
}

// A press shorter than confirmHold confirms nothing, does nothing else either, and asks
// again on the panel.
func TestShortPressDoesNotConfirm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := testConfirmer(t)
		useConfirmer(t, c)
		var shows panelShows
		c.show = shows.show
		h, a := panelHandler()
		done := wait(c, "POST /api/wifi")
		h.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold / 2)
		h.Handle(1, false, "PRESET_1")
		time.Sleep(confirmHold)
		synctest.Wait()
		if _, ok := answered(done); ok {
			t.Fatal("a short press answered the request")
		}
		if !confirmPending.Load() {
			t.Fatal("the request stopped waiting after a short press")
		}
		if keys, stored := a.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys=%v stored=%v, want nothing while a request waits", keys, stored)
		}
		if got := shows.all(); len(got) != 2 || got[0] != "Hold a button for Wi-Fi" || got[1] != got[0] {
			t.Fatalf("panel = %q, want the prompt and then the prompt again", got)
		}
		if err := <-done; !errors.Is(err, web.ErrNotConfirmed) {
			t.Fatalf("confirm = %v, want ErrNotConfirmed", err)
		}
	})
}

// Volume keys never confirm, however long they are held, and keep working while a
// request waits.
func TestVolumeDoesNotConfirm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := testConfirmer(t)
		useConfirmer(t, c)
		h, a := panelHandler()
		done := wait(c, "POST /api/reboot")
		for _, key := range []string{"VOLUME_DOWN", "VOLUME_UP"} {
			h.Handle(0, true, key)
			time.Sleep(2 * confirmHold)
			h.Handle(0, false, key)
			synctest.Wait()
			if _, ok := answered(done); ok {
				t.Fatalf("%s held confirmed the request", key)
			}
			if a.count(key) < 2 {
				t.Fatalf("%s did not ramp while a request waited", key)
			}
		}
		if err := <-done; !errors.Is(err, web.ErrNotConfirmed) {
			t.Fatalf("confirm = %v, want ErrNotConfirmed", err)
		}
	})
}

// The IR remote of the ST10 and ST20 never confirms: its keys act as they always do while
// a request waits.
func TestRemoteKeysDoNotConfirm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := testConfirmer(t)
		useConfirmer(t, c)
		a := &fakeActor{storable: true}
		remote := newButtonHandler(a, quietLog()) // as runIR builds it: confirms stays false
		done := wait(c, "POST /api/factory-reset")
		remote.Handle(0x10, true, "POWER")
		time.Sleep(2 * confirmHold)
		remote.Handle(0x10, false, "POWER")
		synctest.Wait()
		if _, ok := answered(done); ok {
			t.Fatal("a remote key confirmed the request")
		}
		if a.count("POWER") != 1 {
			t.Fatal("the remote's key did not act as usual")
		}
		<-done
	})
}

// On the SoundTouch 300 a key from the processor, whatever its producer, confirms when held:
// the bar's buttons and its remote's are not told apart yet. The rules of the panel hold, a
// short press confirms nothing and the confirming key does nothing else, and every hold logs
// the producer so the bar's value can be read off.
func TestSoundbarKeysConfirm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := testConfirmer(t)
		useConfirmer(t, c)
		var logged strings.Builder
		var mu sync.Mutex
		a := &fakeActor{storable: true}
		h := newSoundbarKeys(a, slog.New(slog.NewTextHandler(writerFunc(func(b []byte) (int, error) {
			mu.Lock()
			defer mu.Unlock()
			return logged.Write(b)
		}), nil)))
		done := wait(c, "POST /api/core-update")

		h.Key(0x41, true, "PRESET_1", 7) // a short press
		time.Sleep(confirmHold / 2)
		h.Key(0x41, false, "PRESET_1", 7)
		time.Sleep(confirmHold)
		synctest.Wait()
		if _, ok := answered(done); ok {
			t.Fatal("a short press from the processor confirmed the request")
		}

		for _, producer := range []uint32{7, 9} {
			h.Key(0x4c, true, "POWER", producer) // held
			time.Sleep(confirmHold)
			synctest.Wait()
			h.Key(0x4c, false, "POWER", producer)
			if err, ok := answered(done); !ok || err != nil {
				t.Fatalf("producer %d: answered=%v err=%v, want the hold to confirm", producer, ok, err)
			}
			mu.Lock()
			line := logged.String()
			mu.Unlock()
			if !strings.Contains(line, "button confirmed a request") || !strings.Contains(line, "producer="+strconv.Itoa(int(producer))) {
				t.Fatalf("producer %d: log %q does not name the producer of the confirming hold", producer, line)
			}
			if producer == 7 {
				done = wait(c, "POST /api/factory-reset")
			}
		}
		if keys, stored := a.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys=%v stored=%v, want nothing from the processor's keys while requests waited", keys, stored)
		}
	})
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

// A newer request replaces the waiting one, which gets ErrConfirmSuperseded; the hold
// then confirms the newer one. A host that repeats a request cannot keep the owner's out.
func TestNewerRequestSupersedes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := testConfirmer(t)
		useConfirmer(t, c)
		var shows panelShows
		c.show = shows.show
		h, _ := panelHandler()
		first := wait(c, "POST /api/factory-reset")
		second := wait(c, "POST /api/wifi")
		if err, ok := answered(first); !ok || !errors.Is(err, web.ErrConfirmSuperseded) {
			t.Fatalf("first: answered=%v err=%v, want ErrConfirmSuperseded", ok, err)
		}
		if !confirmPending.Load() {
			t.Fatal("the newer request is not pending")
		}
		h.Handle(1, true, "PRESET_2")
		time.Sleep(confirmHold)
		synctest.Wait()
		if err, ok := answered(second); !ok || err != nil {
			t.Fatalf("second: answered=%v err=%v, want confirmed", ok, err)
		}
		h.Handle(1, false, "PRESET_2")
		want := []string{"Hold a button to reset", "Hold a button for Wi-Fi", "Confirmed"}
		if got := shows.all(); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("panel = %q, want %q", got, want)
		}
	})
}

// A hold confirms only the request that waited when the key went down: a request that
// replaced it during the hold needs a hold of its own.
func TestHoldDoesNotConfirmARequestThatCameDuringIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := testConfirmer(t)
		useConfirmer(t, c)
		h, a := panelHandler()
		first := wait(c, "POST /api/reboot")
		h.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold / 2)
		second := wait(c, "POST /api/factory-reset")
		time.Sleep(confirmHold)
		synctest.Wait()
		if err := <-first; !errors.Is(err, web.ErrConfirmSuperseded) {
			t.Fatalf("first = %v, want ErrConfirmSuperseded", err)
		}
		if _, ok := answered(second); ok {
			t.Fatal("a hold begun under the first request confirmed the second")
		}
		h.Handle(1, false, "PRESET_1")
		if keys, stored := a.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys=%v stored=%v, want the release swallowed", keys, stored)
		}
		h.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold)
		synctest.Wait()
		if err, ok := answered(second); !ok || err != nil {
			t.Fatalf("second after its own hold: answered=%v err=%v", ok, err)
		}
		h.Handle(1, false, "PRESET_1")
	})
}

// While one key is held to confirm, a second one does nothing, its release included.
func TestSecondKeyDuringAHoldDoesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := testConfirmer(t)
		useConfirmer(t, c)
		h, a := panelHandler()
		done := wait(c, "POST /api/reboot")
		h.Handle(1, true, "PRESET_1")
		h.Handle(2, true, "PRESET_2")
		h.Handle(2, false, "PRESET_2")
		time.Sleep(confirmHold)
		synctest.Wait()
		if err, ok := answered(done); !ok || err != nil {
			t.Fatalf("answered=%v err=%v, want the first key's hold to confirm", ok, err)
		}
		h.Handle(1, false, "PRESET_1")
		if keys, stored := a.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys=%v stored=%v, want nothing", keys, stored)
		}
	})
}

// A device that goes away mid-hold takes the hold with it.
func TestResetCancelsAConfirmHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := testConfirmer(t)
		useConfirmer(t, c)
		h, _ := panelHandler()
		done := wait(c, "POST /api/reboot")
		h.Handle(1, true, "PRESET_1")
		h.Reset()
		time.Sleep(confirmHold)
		synctest.Wait()
		if _, ok := answered(done); ok {
			t.Fatal("a hold whose device went away confirmed the request")
		}
		<-done
	})
}

// Without a hold the request is refused after the window.
func TestConfirmTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := testConfirmer(t)
		if err := c.confirm(context.Background(), "POST /api/factory-reset", ""); !errors.Is(err, web.ErrNotConfirmed) {
			t.Fatalf("confirm = %v, want ErrNotConfirmed", err)
		}
		if confirmPending.Load() {
			t.Fatal("still pending after the window")
		}
	})
}

// The token from the root-only file confirms once and is then replaced.
func TestConfirmByToken(t *testing.T) {
	c := testConfirmer(t)
	b, err := os.ReadFile(confirmTokenPath)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(b))
	if err := c.confirm(context.Background(), "POST /api/firmware", token); err != nil {
		t.Fatalf("confirm with the token = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.confirm(ctx, "POST /api/firmware", token); err == nil {
		t.Fatal("a used token confirmed again")
	}
}

// The token confirms its own request at once and leaves a waiting one waiting.
func TestTokenLeavesAWaitingRequestAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := testConfirmer(t)
		done := wait(c, "POST /api/reboot")
		if err := c.confirm(context.Background(), "POST /api/firmware", c.token); err != nil {
			t.Fatalf("token confirm while another waits = %v", err)
		}
		if _, ok := answered(done); ok || !confirmPending.Load() {
			t.Fatal("the token answered the waiting request")
		}
		<-done
	})
}

// The panel names what a hold would confirm.
func TestConfirmPrompt(t *testing.T) {
	for what, want := range map[string]string{
		"POST /api/factory-reset":   "Hold a button to reset",
		"POST /api/restore":         "Hold a button to restore",
		"POST /api/reboot":          "Hold a button to restart",
		"POST /api/wifi":            "Hold a button for Wi-Fi",
		"POST /api/setup":           "Hold a button for setup",
		"POST /api/bluetooth/clear": "Hold a button to unpair",
		"POST /api/firmware":        "Hold a button to install",
		"POST /api/core-update":     "Hold a button to install",
		"POST /api/soundtapd":       "Hold a button to install",
		"DELETE /api/soundtapd":     "Hold a button to install",
		"POST /api/update":          "Hold a button to install",
		"PUT /api/owner-key":        "Hold a button for keys",
		"DELETE /api/owner-key":     "Hold a button for keys",
		"GET /api/debug":            "Hold a button for logs",
		"GET /api/debug/pprof/heap": "Hold a button for logs",
		"POST /api/something-new":   "Hold a button",
	} {
		if got := confirmPrompt(what); got != want {
			t.Errorf("%s: %q, want %q", what, got, want)
		}
		for _, lang := range panel.Untranslated(want) {
			t.Errorf("%s has no words for %q", lang, want)
		}
	}
}
