// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stein155/soundtap/app/internal/settings"
)

// A settings file from before the alarm and the group preferences existed must read as
// their defaults, not as their zero values: keepGroups on, alarm 07:00 on preset 1.
func TestOlderFileYieldsAlarmAndMultiroomDefaults(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")

	err := os.WriteFile(path, []byte(`{"name":"Keuken","language":"nl"}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got := settings.Open(path).Get()
	if a := got.AlarmConfig(); a != settings.DefaultAlarm {
		t.Fatalf("alarm = %+v, want %+v", a, settings.DefaultAlarm)
	}

	if !got.Multiroom.KeepGroups() || got.Multiroom.SyncVolume {
		t.Fatalf("multiroom = %+v, want keepGroups on and syncVolume off", got.Multiroom)
	}
}

// alarmAt is a disabled alarm at hhmm on preset slot.
func alarmAt(hhmm string, slot int) settings.Alarm {
	return settings.Alarm{Enabled: false, Time: hhmm, Slot: slot, Timezone: ""}
}

func TestSetAlarmValidatesAndPersists(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")
	store := settings.Open(path)

	want := settings.Alarm{Enabled: true, Time: "06:45", Slot: 3, Timezone: ""}

	err := store.SetAlarm(want)
	if err != nil {
		t.Fatal(err)
	}

	if got := settings.Open(path).Get().AlarmConfig(); got != want {
		t.Fatalf("reopened alarm = %+v, want %+v", got, want)
	}

	for _, bad := range []settings.Alarm{
		alarmAt("24:00", 1),
		alarmAt("07:60", 1),
		alarmAt("7:00", 1),
		alarmAt("07:00:00", 1),
		alarmAt("ab:cd", 1),
		alarmAt("", 1),
		alarmAt("07:00", 0),
		alarmAt("07:00", 7),
	} {
		err := store.SetAlarm(bad)
		if err == nil {
			t.Errorf("SetAlarm(%+v) succeeded", bad)
		}
	}

	if got := store.Get().AlarmConfig(); got != want {
		t.Fatalf("alarm after rejected writes = %+v", got)
	}
}

func TestParseAlarmTimeBounds(t *testing.T) {
	t.Parallel()

	for in, want := range map[string][2]int{"00:00": {0, 0}, "23:59": {23, 59}, "07:05": {7, 5}} {
		h, m, err := settings.ParseAlarmTime(in)
		if err != nil || h != want[0] || m != want[1] {
			t.Errorf("ParseAlarmTime(%q) = %d, %d, %v", in, h, m, err)
		}
	}
}

func TestSetMultiroomPersists(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")

	store := settings.Open(path)

	err := store.SetMultiroom(settings.Multiroom{DissolveGroups: true, SyncVolume: true})
	if err != nil {
		t.Fatal(err)
	}

	got := settings.Open(path).Get().Multiroom
	if got.KeepGroups() || !got.SyncVolume {
		t.Fatalf("reopened multiroom = %+v", got)
	}
}

// errRefused is a patch that refuses to apply.
var errRefused = errors.New("no")

// Update is one atomic read-modify-write: concurrent patches to different fields all
// land, a refused patch stores nothing, and a failed disk write leaves memory alone.
func TestUpdateIsAtomic(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")
	store := settings.Open(path)

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_ = store.Update(func(next *settings.Settings) error {
				next.Alarm.Slot++

				return nil
			})
		})
		wg.Go(func() {
			_ = store.Update(func(next *settings.Settings) error {
				next.Audio.Volume++

				return nil
			})
		})
	}

	wg.Wait()

	if got := store.Get(); got.Alarm.Slot != 20 || got.Audio.Volume != 20 {
		t.Fatalf("lost updates: slot %d, volume %d", got.Alarm.Slot, got.Audio.Volume)
	}

	if got := settings.Open(path).Get(); got.Alarm.Slot != 20 || got.Audio.Volume != 20 {
		t.Fatalf("disk lost updates: %+v", got)
	}

	err := store.Update(func(next *settings.Settings) error {
		next.Language = "nl"

		return errRefused
	})
	if err == nil {
		t.Fatal("a refused patch reported success")
	}

	if store.Get().Language != "en" {
		t.Fatal("a refused patch changed memory")
	}

	// A path whose directory is a file: the write must fail and memory stay as it was.
	blocker := filepath.Join(t.TempDir(), "file")

	err = os.WriteFile(blocker, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	broken := settings.Open(filepath.Join(blocker, "settings.json"))

	err = broken.Update(func(next *settings.Settings) error {
		next.Language = "nl"

		return nil
	})
	if err == nil {
		t.Fatal("write into a file-as-directory succeeded")
	}

	if broken.Get().Language != "en" {
		t.Fatal("memory changed although the disk write failed")
	}
}
