// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/settings"
)

// The backup keeps its own list of settings fields, so a field added to the settings
// reaches the backup only if someone adds it here too (two once did not).
// These lists are the fields left out on purpose; anything else must be in the backup.
func settingsNotBackedUp() map[string]string {
	return map[string]string{
		"schemaVersion": "describes the file on this speaker, not a choice of the user",
	}
}

func mqttNotBackedUp() map[string]string {
	return map[string]string{
		"password": "a secret does not belong in a downloaded file",
	}
}

func jsonNames(t reflect.Type) map[string]bool {
	names := map[string]bool{}

	for field := range t.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		names[name] = true
	}

	return names
}

func TestBackupCoversEverySetting(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		stored, backedUp reflect.Type
		skipped          map[string]string
	}{
		{reflect.TypeFor[settings.Settings](), reflect.TypeFor[backupSettings](), settingsNotBackedUp()},
		{reflect.TypeFor[settings.MQTT](), reflect.TypeFor[backupMQTT](), mqttNotBackedUp()},
	} {
		inBackup := jsonNames(testCase.backedUp)
		for name := range jsonNames(testCase.stored) {
			_, skipped := testCase.skipped[name]
			if inBackup[name] == skipped {
				t.Errorf("%s field %q: in backup %v, on the not-backed-up "+
					"list %v; add it to %s or to the list with a reason",
					testCase.stored.Name(), name, inBackup[name], skipped, testCase.backedUp.Name())
			}
		}
	}
}
