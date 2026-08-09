// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"testing"

	"github.com/stein155/soundtap/app/internal/ubootenv"
	"github.com/stein155/soundtap/app/internal/ubootenv/ubootenvtest"
)

const testOwnerKey = "soundtap_owner_key"

// copyValues is the owner key in each copy of the redundant environment, "" when absent
// and "bad" when the copy does not check out.
func (r *rawNAND) copyValues() [2]string {
	var out [2]string
	for i, raw := range [][]byte{
		r.nand["/dev/mtd5"][:ubootenv.Size],
		r.nand["/dev/mtd4"][ubootenv.RedundantOffset : ubootenv.RedundantOffset+ubootenv.Size],
	} {
		env, _, err := ubootenv.ParseCopy(raw)
		if err != nil {
			out[i] = "bad"
			continue
		}
		out[i], _ = env.Get(testOwnerKey)
	}
	return out
}

// The owner key goes into both copies, so a torn newer copy cannot lose it; it goes out of
// both, so a torn newer copy cannot bring it back. Nothing else in the environment moves.
func TestSetEnvEverywhereWritesBothCopies(t *testing.T) {
	r := newRawNAND()
	want := factoryEnv(t)
	if err := ubootenvtest.Image(r.nand, "/dev/mtd5", "/dev/mtd4", want); err != nil {
		t.Fatal(err)
	}
	in := r.installer(true)
	const key = "88omIN8FRazo9PIEnIHv3JUqYTQBzN+wnqpexHvX+MY="
	if err := in.SetEnvEverywhere(r.read, testOwnerKey, key); err != nil {
		t.Fatal(err)
	}
	if got := r.copyValues(); got != [2]string{key, key} {
		t.Fatalf("after setting: copies hold %q", got)
	}
	if v, err := in.EnvValue(r.read, testOwnerKey); err != nil || v != key {
		t.Fatalf("EnvValue = %q, %v", v, err)
	}
	env := r.env(t)
	env.Delete(testOwnerKey)
	if !env.Equal(want) {
		t.Errorf("other entries changed: %q", env.Text())
	}

	// Set again: both copies already hold it, so nothing is written.
	n := len(r.cmds)
	if err := in.SetEnvEverywhere(r.read, testOwnerKey, key); err != nil || len(r.cmds) != n {
		t.Fatalf("setting the same key again: %v, ran %q", err, r.cmds[n:])
	}

	if err := in.SetEnvEverywhere(r.read, testOwnerKey, ""); err != nil {
		t.Fatal(err)
	}
	if got := r.copyValues(); got != [2]string{"", ""} {
		t.Fatalf("after removing: copies hold %q", got)
	}
	if !r.env(t).Equal(want) {
		t.Errorf("removing left %q", r.env(t).Text())
	}
	// Removing what is not there writes nothing.
	n = len(r.cmds)
	if err := in.SetEnvEverywhere(r.read, testOwnerKey, ""); err != nil || len(r.cmds) != n {
		t.Fatalf("removing an absent key: %v, ran %q", err, r.cmds[n:])
	}
}

// A copy that lacks the key (an older write, or a torn copy) is brought in line even when
// the current copy already holds it.
func TestSetEnvEverywhereRepairsTheOtherCopy(t *testing.T) {
	r := newRawNAND()
	const key = "88omIN8FRazo9PIEnIHv3JUqYTQBzN+wnqpexHvX+MY="
	env := factoryEnv(t)
	if err := ubootenvtest.Image(r.nand, "/dev/mtd5", "/dev/mtd4", env); err != nil {
		t.Fatal(err)
	}
	// One plain fw_setenv, as any other write would make it: the key in one copy only.
	in := r.installer(true)
	if err := in.rewriteEnv(r.read, func(e *ubootenv.Env) (bool, error) {
		e.Set(testOwnerKey, key)
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := r.copyValues(); got[0] == got[1] {
		t.Fatalf("one fw_setenv wrote both copies (%q); the test needs one", got)
	}
	if err := in.SetEnvEverywhere(r.read, testOwnerKey, key); err != nil {
		t.Fatal(err)
	}
	if got := r.copyValues(); got != [2]string{key, key} {
		t.Fatalf("copies hold %q", got)
	}
}

// A power cut at any command leaves an environment that checks out, and in it the key is
// either the old value or the new one: never a third, never a boot from defaults.
func TestSetEnvEverywhereSurvivesACut(t *testing.T) {
	const key = "88omIN8FRazo9PIEnIHv3JUqYTQBzN+wnqpexHvX+MY="
	for _, value := range []string{key, ""} {
		for cut := 1; ; cut++ {
			r := newRawNAND()
			env := factoryEnv(t)
			if value == "" {
				env.Set(testOwnerKey, key)
			}
			if err := ubootenvtest.Image(r.nand, "/dev/mtd5", "/dev/mtd4", env); err != nil {
				t.Fatal(err)
			}
			r.cutAfter = cut
			err := r.installer(true).SetEnvEverywhere(r.read, testOwnerKey, value)
			got, _ := r.env(t).Get(testOwnerKey)
			if got != key && got != "" {
				t.Fatalf("value %q, cut after %d: boots with %q", value, cut, got)
			}
			if err == nil {
				if got != value || r.copyValues() != [2]string{value, value} {
					t.Fatalf("value %q: done after %d commands but copies hold %q", value, cut,
						r.copyValues())
				}
				break
			}
		}
	}
}

// Under a U-Boot that reads the single 8 KiB copy there is one copy to write.
func TestSetEnvEverywhereSingleCopy(t *testing.T) {
	r := newRawNAND()
	r.putLegacy(t, factoryEnv(t))
	in := r.installer(false)
	const key = "88omIN8FRazo9PIEnIHv3JUqYTQBzN+wnqpexHvX+MY="
	if err := in.SetEnvEverywhere(r.read, testOwnerKey, key); err != nil {
		t.Fatal(err)
	}
	legacy, err := ubootenv.ParseLegacy(r.nand["/dev/mtd5"])
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := legacy.Get(testOwnerKey); v != key {
		t.Fatalf("single copy holds %q", v)
	}
}
