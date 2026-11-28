// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Remember puts several networks in front of the known list at once, in their order: the
// first install from Bose's software hands over every network stock saved.
func TestRememberSeveral(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "wifi.conf.known")
	rec := func(ssid string) Record { return NewRecord(ssid, "", "wachtwoord") }

	err := Remember(path, rec("Oud"), rec("Nieuw"), rec("Oud"))
	if err != nil {
		t.Fatal(err)
	}

	got := func() []Record {
		b, _ := os.ReadFile(path)

		return ParseKnown(b)
	}

	if want := []Record{rec("Oud"), rec("Nieuw")}; !slices.Equal(got(), want) {
		t.Fatalf("known = %+v, want %+v", got(), want)
	}

	// Lines already there follow, without the networks put in front, up to KnownMax.
	err = Remember(path, rec("A"), rec("Nieuw"), rec("B"), rec("C"))
	if err != nil {
		t.Fatal(err)
	}

	want := []Record{rec("A"), rec("Nieuw"), rec("B"), rec("C"), rec("Oud")}
	if !slices.Equal(got(), want) {
		t.Fatalf("known = %+v, want %+v", got(), want)
	}

	err = Remember(path, rec("D"))
	if err != nil {
		t.Fatal(err)
	}

	if want = append([]Record{rec("D")}, want[:KnownMax-1]...); !slices.Equal(got(), want) {
		t.Fatalf("known = %+v, want %+v", got(), want)
	}

	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != privateFileMode {
		t.Fatalf("mode: %v", err)
	}
}

// A save replaces the network's whole earlier profile: its line in the known list becomes the
// new one, in its place, so an open or WEP entry of the SSID never outlives a move to WEP or
// WPA2. The other networks stay as they were.
func TestSaveReplacesTheKnownProfileOfItsSSID(t *testing.T) {
	t.Parallel()

	conf := filepath.Join(t.TempDir(), "wifi.conf")
	cafe := NewRecord("cafe", "", "cafepass1")

	err := os.WriteFile(conf+".known", []byte(NewRecord("lab", "", "").Line()+"\n"+cafe.Line()+"\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	known := func() []Record {
		b, _ := os.ReadFile(conf + ".known")

		return ParseKnown(b)
	}

	for _, rec := range []Record{NewRecord("lab", SecurityWEP, "abcde"), NewRecord("lab", "", "labpass12")} {
		err = Save(conf, rec)
		if err != nil {
			t.Fatal(err)
		}

		b, _ := os.ReadFile(conf)
		if ParseConf(b) != rec {
			t.Fatalf("wifi.conf = %q", b)
		}

		if got, want := known(), []Record{rec, cafe}; !slices.Equal(got, want) {
			t.Fatalf("known = %+v, want %+v", got, want)
		}
	}

	// the empty profile setup mode saves leaves the list alone
	err = Save(conf, NewRecord("", "", ""))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := known(), []Record{NewRecord("lab", "", "labpass12"), cafe}; !slices.Equal(got, want) {
		t.Fatalf("known after setup mode = %+v, want %+v", got, want)
	}
}
