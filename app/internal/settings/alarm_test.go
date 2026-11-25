// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// A settings file from before the alarm and the group preferences existed must read as
// their defaults, not as their zero values: keepGroups on, alarm 07:00 on preset 1.
func TestOlderFileYieldsAlarmAndMultiroomDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	err := os.WriteFile(path, []byte(`{"name":"Keuken","language":"nl"}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got := Open(path).Get()
	if a := got.AlarmConfig(); a != DefaultAlarm {
		t.Fatalf("alarm = %+v, want %+v", a, DefaultAlarm)
	}

	if !got.Multiroom.KeepGroups() || got.Multiroom.SyncVolume {
		t.Fatalf("multiroom = %+v, want keepGroups on and syncVolume off", got.Multiroom)
	}
}

func TestSetAlarmValidatesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := Open(path)

	want := Alarm{Enabled: true, Time: "06:45", Slot: 3}

	err := s.SetAlarm(want)
	if err != nil {
		t.Fatal(err)
	}

	if got := Open(path).Get().AlarmConfig(); got != want {
		t.Fatalf("reopened alarm = %+v, want %+v", got, want)
	}

	for _, bad := range []Alarm{
		{Time: "24:00", Slot: 1},
		{Time: "07:60", Slot: 1},
		{Time: "7:00", Slot: 1},
		{Time: "07:00:00", Slot: 1},
		{Time: "ab:cd", Slot: 1},
		{Time: "", Slot: 1},
		{Time: "07:00", Slot: 0},
		{Time: "07:00", Slot: 7},
	} {
		err := s.SetAlarm(bad)
		if err == nil {
			t.Errorf("SetAlarm(%+v) succeeded", bad)
		}
	}

	if got := s.Get().AlarmConfig(); got != want {
		t.Fatalf("alarm after rejected writes = %+v", got)
	}
}

func TestParseAlarmTimeBounds(t *testing.T) {
	for in, want := range map[string][2]int{"00:00": {0, 0}, "23:59": {23, 59}, "07:05": {7, 5}} {
		h, m, err := ParseAlarmTime(in)
		if err != nil || h != want[0] || m != want[1] {
			t.Errorf("ParseAlarmTime(%q) = %d, %d, %v", in, h, m, err)
		}
	}
}

func TestSetMultiroomPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	s := Open(path)

	err := s.SetMultiroom(Multiroom{DissolveGroups: true, SyncVolume: true})
	if err != nil {
		t.Fatal(err)
	}

	got := Open(path).Get().Multiroom
	if got.KeepGroups() || !got.SyncVolume {
		t.Fatalf("reopened multiroom = %+v", got)
	}
}

// Update is one atomic read-modify-write: concurrent patches to different fields all
// land, a refused patch stores nothing, and a failed disk write leaves memory alone.
func TestUpdateIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := Open(path)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()

			_ = s.Update(func(st *Settings) error {
				st.Alarm.Slot++

				return nil
			})
		}()
		go func() {
			defer wg.Done()

			_ = s.Update(func(st *Settings) error {
				st.Audio.Volume++

				return nil
			})
		}()
	}

	wg.Wait()

	if got := s.Get(); got.Alarm.Slot != 20 || got.Audio.Volume != 20 {
		t.Fatalf("lost updates: slot %d, volume %d", got.Alarm.Slot, got.Audio.Volume)
	}

	if got := Open(path).Get(); got.Alarm.Slot != 20 || got.Audio.Volume != 20 {
		t.Fatalf("disk lost updates: %+v", got)
	}

	err := s.Update(func(st *Settings) error {
		st.Language = "nl"

		return errors.New("no")
	})
	if err == nil {
		t.Fatal("a refused patch reported success")
	}

	if s.Get().Language != "en" {
		t.Fatal("a refused patch changed memory")
	}

	// A path whose directory is a file: the write must fail and memory stay as it was.
	blocker := filepath.Join(t.TempDir(), "file")

	err = os.WriteFile(blocker, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	broken := Open(filepath.Join(blocker, "settings.json"))

	err = broken.Update(func(st *Settings) error {
		st.Language = "nl"

		return nil
	})
	if err == nil {
		t.Fatal("write into a file-as-directory succeeded")
	}

	if broken.Get().Language != "en" {
		t.Fatal("memory changed although the disk write failed")
	}
}
