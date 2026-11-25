// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package atomicjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// ErrUnreadable marks a state file that is there but could not be read (EIO, EACCES,
// EISDIR), or a corrupt one that could not be moved aside. The file may still hold
// the owner's data, so the store that opened it refuses every write: a save would
// replace it with defaults.
var ErrUnreadable = errors.New("state file unreadable; writes refused so it is not replaced")

// ErrCorrupt marks a state file that did not decode and was moved aside. The store
// starts from its defaults and may write.
var ErrCorrupt = errors.New("corrupt state file moved aside")

// Read is the one rule every on-box store opens its file by. Only a missing file
// means "start from defaults" (found false, err nil). Any other read error comes
// back wrapping ErrUnreadable, and the caller must then refuse writes.
func Read(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return data, true, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
}

// Quarantine moves a file that did not decode to path.corrupt, and the copy before
// that to path.corrupt.1: a file that keeps going bad must not fill /mnt/nv. cause
// is why it did not decode. The result wraps ErrCorrupt when the file was moved,
// and ErrUnreadable when it was not, since the next write would then destroy it.
func Quarantine(path string, cause error) error {
	backup := path + ".corrupt"

	_ = os.Rename(backup, backup+".1") // there may be no earlier copy

	err := os.Rename(path, backup)
	if err != nil {
		return fmt.Errorf("%w: corrupt (%w) and not moved aside: %w", ErrUnreadable, cause, err)
	}

	return fmt.Errorf("%w to %s: %w", ErrCorrupt, backup, cause)
}

// RemoveQuarantine deletes the corrupt copies Quarantine kept of path: a factory
// reset must not leave the previous owner's data behind in them.
func RemoveQuarantine(path string) error {
	backups, _ := filepath.Glob(path + ".corrupt*")

	var errs []error

	for _, b := range backups {
		err := os.Remove(b)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// Load reads the JSON file at path into value by Read's rule. A missing file leaves value
// as it is (the caller's defaults) and returns nil. A file that does not decode is
// quarantined and value keeps what it held before, so a half-decoded value never becomes
// the store. The error says which case applies: ErrUnreadable means the store must
// refuse writes, ErrCorrupt that it may write over its defaults.
func Load[T any](path string, value *T) error {
	data, found, err := Read(path)
	if err != nil || !found {
		return err
	}

	before := *value

	err = json.Unmarshal(data, value)
	if err != nil {
		*value = before

		return Quarantine(path, err)
	}

	return nil
}

// LogLoad reports what Read, Load or Quarantine returned for the store named what,
// once, when the store opens: an unreadable file at error level (writes are refused
// until it is fixed), a quarantined one as a warning, nothing for a good or missing file.
func LogLoad(log *slog.Logger, what, path string, err error) {
	switch {
	case err == nil:
	case errors.Is(err, ErrUnreadable):
		log.Error(what+" unreadable; read-only until the file is fixed", "path", path, "err", err)
	default:
		log.Warn(what+" corrupt; starting from defaults", "path", path, "err", err)
	}
}
