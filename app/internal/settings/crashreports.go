// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings

import "slices"

// CrashReports is whether the web UI offers to report a crash it finds on the speaker,
// and the crashes the owner already reported or dismissed, which it does not offer again
// (ADR 0126). The speaker sends nothing: the owner opens and submits the issue.
type CrashReports struct {
	Enabled bool `json:"enabled"`
	// Handled are crash ids, oldest first, at most MaxHandledCrashes.
	Handled []string `json:"handled,omitempty"`
}

// MaxHandledCrashes bounds CrashReports.Handled: older ids go first. A crash id comes from
// the crash's own content, so one that fell off the list is only offered again if the same
// crash is still on the speaker.
const MaxHandledCrashes = 32

// IsHandled reports whether the owner already reported or dismissed crash id.
func (c *CrashReports) IsHandled(id string) bool { return slices.Contains(c.Handled, id) }

// Handle records crash id as reported or dismissed.
func (c *CrashReports) Handle(id string) {
	if c.IsHandled(id) {
		return
	}

	c.Handled = append(c.Handled, id)
	if n := len(c.Handled) - MaxHandledCrashes; n > 0 {
		c.Handled = append([]string(nil), c.Handled[n:]...)
	}
}
