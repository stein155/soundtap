// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
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

	conf := &deviceConfirm{
		mu:     sync.Mutex{},
		wait:   nil,
		gen:    0,
		token:  "",
		window: time.Minute,
		show:   nil,
		prompt: "",
		log:    slog.New(slog.DiscardHandler),
	}
	conf.rotate()

	return conf
}

// useConfirmer makes c the one the button handlers confirm through.
func useConfirmer(t *testing.T, conf *deviceConfirm) {
	t.Helper()

	old := confirmer
	confirmer = conf

	t.Cleanup(func() { confirmer = old })
}

// panelHandler is a handler for the speaker's own keys, as the keypad and gpio-keys
// readers make it.
func panelHandler() (*buttonHandler, *fakeActor) {
	handler, actor := testHandler()
	handler.confirms = true

	return handler, actor
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
func wait(conf *deviceConfirm, what string) <-chan error {
	done := make(chan error, 1)
	go func() { done <- conf.confirm(context.Background(), what, "") }()

	synctest.Wait()

	return done
}

// answered reports whether done has an answer yet, and what it is.
func answered(done <-chan error) (bool, error) {
	select {
	case err := <-done:
		return true, err
	default:
		return false, nil
	}
}

// A key held for confirmHold confirms the waiting request, and does nothing else: no
// preset plays or stores although the hold passes holdToStore, and the release is
// swallowed.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath, confirmer
func TestConfirmByHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)
		useConfirmer(t, conf)

		handler, actor := panelHandler()
		done := wait(conf, "POST /api/factory-reset")

		if !confirmPending.Load() {
			t.Fatal("no pending confirmation while a request waits")
		}

		handler.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold - time.Millisecond)
		synctest.Wait()

		if ok, _ := answered(done); ok {
			t.Fatal("confirmed before the hold was long enough")
		}

		time.Sleep(time.Millisecond)
		synctest.Wait()

		ok, err := answered(done)
		if !ok || err != nil {
			t.Fatalf("after the hold: answered=%v err=%v, want confirmed", ok, err)
		}

		if confirmPending.Load() {
			t.Fatal("still pending after the hold")
		}

		time.Sleep(time.Second)
		handler.Handle(1, false, "PRESET_1")
		synctest.Wait()

		if keys, stored := actor.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys=%v stored=%v, want nothing from the confirming key", keys, stored)
		}
		// The key works as usual again afterwards.
		handler.Handle(1, true, "PRESET_1")
		handler.Handle(1, false, "PRESET_1")

		if actor.count("PRESET_1") != 1 {
			t.Fatal("the next press of the key did not play its preset")
		}
	})
}

// A press shorter than confirmHold confirms nothing, does nothing else either, and asks
// again on the panel.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath, confirmer
func TestShortPressDoesNotConfirm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)
		useConfirmer(t, conf)

		var shows panelShows

		conf.show = shows.show
		handler, actor := panelHandler()
		done := wait(conf, "POST /api/wifi")

		handler.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold / 2)
		handler.Handle(1, false, "PRESET_1")
		time.Sleep(confirmHold)
		synctest.Wait()

		if ok, _ := answered(done); ok {
			t.Fatal("a short press answered the request")
		}

		if !confirmPending.Load() {
			t.Fatal("the request stopped waiting after a short press")
		}

		if keys, stored := actor.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys=%v stored=%v, want nothing while a request waits", keys, stored)
		}

		if got := shows.all(); len(got) != 2 || got[0] != "Change Wi-Fi?" || got[1] != got[0] {
			t.Fatalf("panel = %q, want the prompt and then the prompt again", got)
		}

		err := <-done
		if !errors.Is(err, web.ErrNotConfirmed) {
			t.Fatalf("confirm = %v, want ErrNotConfirmed", err)
		}
	})
}

// A release shorter than confirmGrace during the hold is the panel's contact chattering,
// not letting go: the hold confirms confirmHold after the key first went down, not after
// the last bounce, and does not ask again on the way.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath, confirmer
func TestConfirmHoldRidesOutAShortRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)
		useConfirmer(t, conf)

		var shows panelShows

		conf.show = shows.show
		handler, actor := panelHandler()
		done := wait(conf, "POST /api/reboot")

		handler.Handle(1, true, "PRESET_1")
		time.Sleep(time.Second)
		handler.Handle(1, false, "PRESET_1")
		time.Sleep(confirmGrace - 10*time.Millisecond)
		handler.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold - time.Second - confirmGrace + 10*time.Millisecond - time.Millisecond)
		synctest.Wait()

		if ok, _ := answered(done); ok {
			t.Fatal("confirmed before the hold was long enough")
		}

		time.Sleep(time.Millisecond)
		synctest.Wait()

		ok, err := answered(done)
		if !ok || err != nil {
			t.Fatalf("confirmHold after the first press: answered=%v err=%v, want confirmed", ok, err)
		}

		handler.Handle(1, false, "PRESET_1")

		if got := shows.all(); len(got) != 2 || got[1] == got[0] {
			t.Fatalf("panel = %q, want the prompt once and then the confirmation", got)
		}

		if keys, stored := actor.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys=%v stored=%v, want nothing from the confirming key", keys, stored)
		}
	})
}

// A hold whose time runs out while its key is briefly up confirms only once the key is
// down again, and not before: the hold confirms while the key is held.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath, confirmer
func TestConfirmHoldDueDuringAShortReleaseConfirmsOnThePress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)
		useConfirmer(t, conf)

		handler, _ := panelHandler()
		done := wait(conf, "POST /api/reboot")

		handler.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold - 100*time.Millisecond)
		handler.Handle(1, false, "PRESET_1")
		time.Sleep(150 * time.Millisecond)
		synctest.Wait()

		if ok, _ := answered(done); ok {
			t.Fatal("confirmed while the key was up")
		}

		handler.Handle(1, true, "PRESET_1")
		synctest.Wait()

		ok, err := answered(done)
		if !ok || err != nil {
			t.Fatalf("after the press within the grace: answered=%v err=%v, want confirmed", ok, err)
		}

		handler.Handle(1, false, "PRESET_1")
	})
}

// Letting go just before confirmHold is letting go, though the grace reaches past it:
// nothing is confirmed, the panel asks again, and the key's next press is a new hold.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath, confirmer
func TestConfirmHoldLetGoJustBeforeItIsDueConfirmsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)
		useConfirmer(t, conf)

		var shows panelShows

		conf.show = shows.show
		handler, actor := panelHandler()
		done := wait(conf, "POST /api/reboot")

		handler.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold - 100*time.Millisecond)
		handler.Handle(1, false, "PRESET_1")
		time.Sleep(confirmGrace)
		synctest.Wait()

		if ok, _ := answered(done); ok {
			t.Fatal("a released key confirmed the request")
		}

		if got := shows.all(); len(got) != 2 || got[1] != got[0] {
			t.Fatalf("panel = %q, want the prompt and then the prompt again", got)
		}
		// A press after the grace starts over: a tap of it confirms nothing.
		handler.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold / 2)
		handler.Handle(1, false, "PRESET_1")
		time.Sleep(confirmHold)
		synctest.Wait()

		if ok, _ := answered(done); ok {
			t.Fatal("a new short press finished the old hold")
		}

		if keys, stored := actor.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys=%v stored=%v, want nothing while a request waits", keys, stored)
		}

		err := <-done
		if !errors.Is(err, web.ErrNotConfirmed) {
			t.Fatalf("confirm = %v, want ErrNotConfirmed", err)
		}
	})
}

// A bounce of the confirming key keeps the hold to the request that waited when it
// began: a request that replaced it during the bounce needs a hold of its own.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath, confirmer
func TestConfirmHoldAcrossABounceKeepsItsRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)
		useConfirmer(t, conf)

		handler, _ := panelHandler()
		first := wait(conf, "POST /api/reboot")

		handler.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold / 2)
		handler.Handle(1, false, "PRESET_1")

		second := wait(conf, "POST /api/factory-reset")

		time.Sleep(confirmGrace / 2)
		handler.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold)
		synctest.Wait()

		err := <-first
		if !errors.Is(err, web.ErrConfirmSuperseded) {
			t.Fatalf("first = %v, want ErrConfirmSuperseded", err)
		}

		if ok, _ := answered(second); ok {
			t.Fatal("a hold begun under the first request confirmed the second")
		}

		handler.Handle(1, false, "PRESET_1")
		time.Sleep(confirmGrace)
		handler.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold)
		synctest.Wait()

		ok, err := answered(second)
		if !ok || err != nil {
			t.Fatalf("second after its own hold: answered=%v err=%v", ok, err)
		}

		handler.Handle(1, false, "PRESET_1")
	})
}

// Volume keys never confirm, however long they are held, and keep working while a
// request waits.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath, confirmer
func TestVolumeDoesNotConfirm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)
		useConfirmer(t, conf)

		handler, actor := panelHandler()
		done := wait(conf, "POST /api/reboot")

		for _, key := range []string{"VOLUME_DOWN", "VOLUME_UP"} {
			handler.Handle(0, true, key)
			time.Sleep(2 * confirmHold)
			handler.Handle(0, false, key)
			synctest.Wait()

			if ok, _ := answered(done); ok {
				t.Fatalf("%s held confirmed the request", key)
			}

			if actor.count(key) < 2 {
				t.Fatalf("%s did not ramp while a request waited", key)
			}
		}

		err := <-done
		if !errors.Is(err, web.ErrNotConfirmed) {
			t.Fatalf("confirm = %v, want ErrNotConfirmed", err)
		}
	})
}

// The IR remote of the ST10 and ST20 never confirms: its keys act as they always do while
// a request waits.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath, confirmer
func TestRemoteKeysDoNotConfirm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)
		useConfirmer(t, conf)

		actor := &fakeActor{mu: sync.Mutex{}, keys: nil, stored: nil, storable: true, storeErr: nil}
		remote := newButtonHandler(actor, quietLog()) // as runIR builds it: confirms stays false
		done := wait(conf, "POST /api/factory-reset")

		remote.Handle(0x10, true, "POWER")
		time.Sleep(2 * confirmHold)
		remote.Handle(0x10, false, "POWER")
		synctest.Wait()

		if ok, _ := answered(done); ok {
			t.Fatal("a remote key confirmed the request")
		}

		if actor.count("POWER") != 1 {
			t.Fatal("the remote's key did not act as usual")
		}

		err := <-done
		if !errors.Is(err, web.ErrNotConfirmed) {
			t.Fatalf("the request ended with %v, want it to time out unconfirmed", err)
		}
	})
}

// On the SoundTouch 300 a key from the processor, whatever its producer, confirms when held:
// the bar's buttons and its remote's are not told apart yet. The rules of the panel hold, a
// short press confirms nothing and the confirming key does nothing else, and every hold logs
// the producer so the bar's value can be read off.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath, confirmer
func TestSoundbarKeysConfirm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)
		useConfirmer(t, conf)

		var (
			logged strings.Builder
			mu     sync.Mutex
		)

		actor := &fakeActor{mu: sync.Mutex{}, keys: nil, stored: nil, storable: true, storeErr: nil}
		handler := newSoundbarKeys(actor, slog.New(slog.NewTextHandler(writerFunc(func(b []byte) (int, error) {
			mu.Lock()
			defer mu.Unlock()

			return logged.Write(b)
		}), nil)))
		done := wait(conf, "POST /api/core-update")

		handler.Key(0x41, true, "PRESET_1", 7) // a short press
		time.Sleep(confirmHold / 2)
		handler.Key(0x41, false, "PRESET_1", 7)
		time.Sleep(confirmHold)
		synctest.Wait()

		if ok, _ := answered(done); ok {
			t.Fatal("a short press from the processor confirmed the request")
		}

		for _, producer := range []uint32{7, 9} {
			handler.Key(0x4c, true, "POWER", producer) // held
			time.Sleep(confirmHold)
			synctest.Wait()
			handler.Key(0x4c, false, "POWER", producer)

			ok, err := answered(done)
			if !ok || err != nil {
				t.Fatalf("producer %d: answered=%v err=%v, want the hold to confirm", producer, ok, err)
			}

			mu.Lock()
			line := logged.String()
			mu.Unlock()

			named := strings.Contains(line, "producer="+strconv.Itoa(int(producer)))
			if !strings.Contains(line, "button confirmed a request") || !named {
				t.Fatalf("producer %d: log %q does not name the producer of the confirming hold", producer, line)
			}

			if producer == 7 {
				done = wait(conf, "POST /api/factory-reset")
			}
		}

		if keys, stored := actor.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys=%v stored=%v, want nothing from the processor's keys while requests waited", keys, stored)
		}
	})
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

// A newer request replaces the waiting one, which gets ErrConfirmSuperseded; the hold
// then confirms the newer one. A host that repeats a request cannot keep the owner's out.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath, confirmer
func TestNewerRequestSupersedes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)
		useConfirmer(t, conf)

		var shows panelShows

		conf.show = shows.show
		handler, _ := panelHandler()
		first := wait(conf, "POST /api/factory-reset")
		second := wait(conf, "POST /api/wifi")

		firstAnswered, err := answered(first)
		if !firstAnswered || !errors.Is(err, web.ErrConfirmSuperseded) {
			t.Fatalf("first: answered=%v err=%v, want ErrConfirmSuperseded", firstAnswered, err)
		}

		if !confirmPending.Load() {
			t.Fatal("the newer request is not pending")
		}

		handler.Handle(1, true, "PRESET_2")
		time.Sleep(confirmHold)
		synctest.Wait()

		secondAnswered, err := answered(second)
		if !secondAnswered || err != nil {
			t.Fatalf("second: answered=%v err=%v, want confirmed", secondAnswered, err)
		}

		handler.Handle(1, false, "PRESET_2")

		want := []string{"Reset?", "Change Wi-Fi?", "Confirmed"}
		if got := shows.all(); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("panel = %q, want %q", got, want)
		}
	})
}

// A hold confirms only the request that waited when the key went down: a request that
// replaced it during the hold needs a hold of its own.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath, confirmer
func TestHoldDoesNotConfirmARequestThatCameDuringIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)
		useConfirmer(t, conf)

		handler, actor := panelHandler()
		first := wait(conf, "POST /api/reboot")

		handler.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold / 2)

		second := wait(conf, "POST /api/factory-reset")

		time.Sleep(confirmHold)
		synctest.Wait()

		err := <-first
		if !errors.Is(err, web.ErrConfirmSuperseded) {
			t.Fatalf("first = %v, want ErrConfirmSuperseded", err)
		}

		if ok, _ := answered(second); ok {
			t.Fatal("a hold begun under the first request confirmed the second")
		}

		handler.Handle(1, false, "PRESET_1")

		if keys, stored := actor.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys=%v stored=%v, want the release swallowed", keys, stored)
		}

		handler.Handle(1, true, "PRESET_1")
		time.Sleep(confirmHold)
		synctest.Wait()

		ok, secondErr := answered(second)
		if !ok || secondErr != nil {
			t.Fatalf("second after its own hold: answered=%v err=%v", ok, secondErr)
		}

		handler.Handle(1, false, "PRESET_1")
	})
}

// While one key is held to confirm, a second one does nothing, its release included.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath, confirmer
func TestSecondKeyDuringAHoldDoesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)
		useConfirmer(t, conf)

		handler, actor := panelHandler()
		done := wait(conf, "POST /api/reboot")

		handler.Handle(1, true, "PRESET_1")
		handler.Handle(2, true, "PRESET_2")
		handler.Handle(2, false, "PRESET_2")
		time.Sleep(confirmHold)
		synctest.Wait()

		ok, err := answered(done)
		if !ok || err != nil {
			t.Fatalf("answered=%v err=%v, want the first key's hold to confirm", ok, err)
		}

		handler.Handle(1, false, "PRESET_1")

		if keys, stored := actor.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys=%v stored=%v, want nothing", keys, stored)
		}
	})
}

// A device that goes away mid-hold takes the hold with it.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath, confirmer
func TestResetCancelsAConfirmHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)
		useConfirmer(t, conf)

		handler, _ := panelHandler()
		done := wait(conf, "POST /api/reboot")

		handler.Handle(1, true, "PRESET_1")
		handler.Reset()
		time.Sleep(confirmHold)
		synctest.Wait()

		if ok, _ := answered(done); ok {
			t.Fatal("a hold whose device went away confirmed the request")
		}

		err := <-done
		if !errors.Is(err, web.ErrNotConfirmed) {
			t.Fatalf("the request ended with %v, want it to time out unconfirmed", err)
		}
	})
}

// Without a hold the request is refused after the window.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath
func TestConfirmTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)

		err := conf.confirm(context.Background(), "POST /api/factory-reset", "")
		if !errors.Is(err, web.ErrNotConfirmed) {
			t.Fatalf("confirm = %v, want ErrNotConfirmed", err)
		}

		if confirmPending.Load() {
			t.Fatal("still pending after the window")
		}
	})
}

// The token from the root-only file confirms once and is then replaced.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath
func TestConfirmByToken(t *testing.T) {
	conf := testConfirmer(t)

	b, err := os.ReadFile(confirmTokenPath)
	if err != nil {
		t.Fatal(err)
	}

	token := strings.TrimSpace(string(b))

	err = conf.confirm(context.Background(), "POST /api/firmware", token)
	if err != nil {
		t.Fatalf("confirm with the token = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = conf.confirm(ctx, "POST /api/firmware", token)
	if err == nil {
		t.Fatal("a used token confirmed again")
	}
}

// The token confirms its own request at once and leaves a waiting one waiting.
//
//nolint:paralleltest // swaps package variables other tests read: confirmTokenPath
func TestTokenLeavesAWaitingRequestAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conf := testConfirmer(t)

		done := wait(conf, "POST /api/reboot")

		err := conf.confirm(context.Background(), "POST /api/firmware", conf.token)
		if err != nil {
			t.Fatalf("token confirm while another waits = %v", err)
		}

		if ok, _ := answered(done); ok || !confirmPending.Load() {
			t.Fatal("the token answered the waiting request")
		}

		err = <-done
		if !errors.Is(err, web.ErrNotConfirmed) {
			t.Fatalf("the request ended with %v, want it to time out unconfirmed", err)
		}
	})
}

// The panel names what a hold would confirm.
func TestConfirmPrompt(t *testing.T) {
	t.Parallel()

	for what, want := range map[string]string{
		"POST /api/factory-reset":   "Reset?",
		"POST /api/restore":         "Restore?",
		"POST /api/reboot":          "Restart?",
		"POST /api/wifi":            "Change Wi-Fi?",
		"POST /api/setup":           "Setup mode?",
		"POST /api/bluetooth/clear": "Unpair?",
		"POST /api/firmware":        "Install?",
		"POST /api/core-update":     "Install?",
		"POST /api/soundtapd":       "Install?",
		"DELETE /api/soundtapd":     "Install?",
		"POST /api/update":          "Install?",
		"GET /api/debug":            "Share logs?",
		"GET /api/debug/pprof/heap": "Share logs?",
		"POST /api/something-new":   "Continue?",
	} {
		if got := confirmPrompt(what); got != want {
			t.Errorf("%s: %q, want %q", what, got, want)
		}

		for _, lang := range panel.Untranslated(want) {
			t.Errorf("%s has no words for %q", lang, want)
		}
	}
}
