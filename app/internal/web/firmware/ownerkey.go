// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/release"
	"github.com/stein155/soundtap/app/internal/web/apierror"
)

// OwnerKeyPath is the owner key's route (ADR 0048, "The owner's own key"): GET says
// whether one is set, PUT sets one and DELETE removes it. PUT and DELETE wait for the
// hold on the speaker (ADR 0055); internal/web's needsPress names them.
const OwnerKeyPath = "/api/owner-key"

// ErrNoEnvironment means this speaker has no U-Boot environment soundtapd can reach: off
// a speaker, or a partition table without NAND.U-Boot-Env. It holds no owner key then.
var ErrNoEnvironment = errors.New("this speaker has no U-Boot environment to keep a key in")

// OwnerKey is what GET /api/owner-key answers.
type OwnerKey struct {
	// Available: the speaker has an environment the key can be kept in.
	Available bool `json:"available"`
	// Active: an owner key is set, and statements signed with it are trusted.
	Active bool `json:"active"`
	// Key is the public key as it was set, Fingerprint the first 8 bytes of its SHA-256.
	Key         string `json:"key,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// envInstaller is the environment's reader and writer on this speaker: the install's plan
// and commands, which tests replace with fake NAND.
func (s *Handler) envInstaller() (*flash.Installer, flash.Reader, error) {
	target, err := s.livePlan()
	if err != nil || target.EnvMTD < 0 {
		return nil, nil, ErrNoEnvironment
	}
	run, read := s.flashIO()
	return &flash.Installer{Target: target, Run: run}, read, nil
}

// ownerKey is the owner key the environment holds, "" when there is none or it cannot be
// read. A value that is not a key is ignored, as U-Boot ignores it, and said in the log.
func (s *Handler) ownerKey() string {
	inst, read, err := s.envInstaller()
	if err != nil {
		return ""
	}
	v, err := inst.EnvValue(read, release.OwnerKeyEnv)
	if err != nil {
		s.logger().Warn("owner key: U-Boot environment unreadable; trusting the release keys only", "err", err)
		return ""
	}
	if v != "" && release.Fingerprint(v) == "" {
		s.logger().Warn("owner key: the environment holds a value that is not a key; ignored")
		return ""
	}
	return v
}

// trustedKeys are the keys a statement on this speaker may be signed with: the release
// keys and, when one is set, the owner key.
func (s *Handler) trustedKeys() []string {
	return release.TrustedKeys(releaseKeys, s.ownerKey())
}

func (s *Handler) ownerKeyState() OwnerKey {
	if _, _, err := s.envInstaller(); err != nil {
		return OwnerKey{}
	}
	k := OwnerKey{Available: true, Key: s.ownerKey()}
	if k.Key != "" {
		k.Active, k.Fingerprint = true, release.Fingerprint(k.Key)
	}
	return k
}

func (s *Handler) getOwnerKey(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.ownerKeyState())
}

// putOwnerKey sets the owner key, after the hold on the speaker. Body: {"key": "<base64>"}.
func (s *Handler) putOwnerKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		apiError(w, http.StatusBadRequest, "bad body")
		return
	}
	key, err := release.ParseOwnerKey(body.Key)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeOwnerKey(w, key)
}

// deleteOwnerKey removes the owner key, after the hold on the speaker.
func (s *Handler) deleteOwnerKey(w http.ResponseWriter, _ *http.Request) {
	s.writeOwnerKey(w, "")
}

// writeOwnerKey puts key ("" removes it) into both environment copies under the
// operations lock: an install writes the same environment.
func (s *Handler) writeOwnerKey(w http.ResponseWriter, key string) {
	if !s.firmware.mu.TryLock() {
		apiErrorCode(w, http.StatusConflict, apierror.Busy, "another firmware operation is in progress")
		return
	}
	defer s.firmware.mu.Unlock()
	inst, read, err := s.envInstaller()
	if err != nil {
		apiError(w, http.StatusNotImplemented, err.Error())
		return
	}
	if err := inst.SetEnvEverywhere(read, release.OwnerKeyEnv, key); err != nil {
		s.fail(w, "writing the owner key to the U-Boot environment failed", err)
		return
	}
	if key == "" {
		s.logger().Warn("owner key removed")
	} else {
		s.logger().Warn("owner key set", "fingerprint", release.Fingerprint(key))
	}
	writeJSON(w, http.StatusOK, s.ownerKeyState())
}

// ClearOwnerKey removes the owner key from both environment copies: the factory reset's
// part (ADR 0048). The caller holds the operations lock. ErrNoEnvironment when there is
// no environment to hold one.
func (s *Handler) ClearOwnerKey() error {
	inst, read, err := s.envInstaller()
	if err != nil {
		return err
	}
	return inst.SetEnvEverywhere(read, release.OwnerKeyEnv, "")
}
