// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"os"
	"testing"
)

// The install tests upload unsigned builds, as a development speaker takes them;
// signature_test.go puts the real rule back.
var strictAllowUnsigned = allowUnsigned

func TestMain(m *testing.M) {
	allowUnsigned = func(string) bool { return true }
	os.Exit(m.Run())
}
