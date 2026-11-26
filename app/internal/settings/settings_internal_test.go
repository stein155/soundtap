// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings

import (
	"encoding/json"
	"testing"
)

func TestMigrationsRunInOrder(t *testing.T) {
	t.Parallel()

	// A pretend version 2 that renames a field: a version 1 file gets it, a version 2
	// file does not.
	migs := append(migrations(), func(raw map[string]json.RawMessage) error {
		raw["name"] = raw["oldName"]

		return nil
	})

	got, err := migrateWith([]byte(`{"schemaVersion":1,"oldName":"Zolder"}`), migs)
	if err != nil || got.Name != "Zolder" || got.SchemaVersion != 2 {
		t.Fatalf("v1 -> v2: %+v, %v", got, err)
	}

	got, err = migrateWith([]byte(`{"schemaVersion":2,"name":"Keuken","oldName":"Zolder"}`), migs)
	if err != nil || got.Name != "Keuken" {
		t.Fatalf("v2 read as is: %+v, %v", got, err)
	}
}
