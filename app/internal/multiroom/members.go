// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"errors"
	"os"
	"slices"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/snapcast"
)

// The steps of SetVolume and SetMember that can fail, so the caller can tell them apart.
var (
	ErrStatus       = errors.New("multiroom status failed")
	ErrNoLocal      = errors.New("current client not found")
	ErrNoClient     = errors.New("client not found")
	ErrRemoveLocal  = errors.New("current client cannot be removed")
	ErrSetVolume    = errors.New("set multiroom volume failed")
	ErrSetClients   = errors.New("set multiroom membership failed")
	ErrMemberVolume = errors.New("set multiroom membership volume failed")
	ErrPersist      = errors.New("persist multiroom state failed")
)

// readStatus is snapserver's status, failed as ErrStatus.
func readStatus(ctx context.Context, env Env) (snapcast.Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status, err := env.Snapcast.GetStatus(ctx)
	if err != nil {
		return snapcast.Status{}, &JoinError{ErrStatus, err}
	}
	return status, nil
}

// SetVolume sets client id's volume and returns the volume it set. id may name the
// client either way; see ClientIDFor.
func SetVolume(ctx context.Context, env Env, id string, volume snapcast.Volume) (snapcast.Volume, error) {
	status, err := readStatus(ctx, env)
	if err != nil {
		return volume, err
	}
	id, found := ClientIDFor(status, id)
	if !found {
		return volume, &JoinError{ErrNoClient, nil}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// This speaker's own client is the silent anchor: it stays muted (muteAnchor), and
	// its volume never reached a speaker anyway.
	if id == env.LocalID {
		volume.Muted = true
	}
	if err := env.Snapcast.SetVolume(ctx, id, volume); err != nil {
		return volume, &JoinError{ErrSetVolume, err}
	}
	return volume, nil
}

// SetMember puts client id in this speaker's group or, with member false, takes it out.
func (g *Group) SetMember(ctx context.Context, env Env, id string, member bool) error {
	// The read of the group and the write of the new member list are one step: two
	// overlapping calls would each build their list from the old group, and the later
	// one would drop the room the earlier one added.
	g.members.Lock()
	defer g.members.Unlock()
	status, groupID, err := localGroup(ctx, env)
	if err != nil {
		return err
	}
	// Resolved before the comparison below: a caller that named this speaker by its
	// device id must still be recognised as this speaker, or the guard against removing
	// the current client is bypassed by spelling it the other way.
	id, found := ClientIDFor(status, id)
	if !found {
		return &JoinError{ErrNoClient, nil}
	}
	if id == env.LocalID && !member {
		return &JoinError{ErrRemoveLocal, nil}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := env.Snapcast.SetClients(ctx, groupID, groupClients(status, groupID, id, member)); err != nil {
		return &JoinError{ErrSetClients, err}
	}
	volume, found := FindClientVolume(status, id)
	if found {
		volume.Muted = !member
		if err := env.Snapcast.SetVolume(ctx, id, volume); err != nil {
			return &JoinError{ErrMemberVolume, err}
		}
	}
	if env.Active != nil {
		active := member || othersConnected(status, groupID, id, env.LocalID)
		env.Active(active)
		if err := PersistActive(env.ActivePath, active); err != nil {
			return &JoinError{ErrPersist, err}
		}
	}
	return nil
}

// anchorWait bounds how long SetMember waits for snapserver to know this speaker's own
// client after starting the anchor: a snapclient start and connect take about a second on
// these boards. It stays well under a peer call's timeout, so a follower's join is
// answered rather than timed out. A variable for tests.
var anchorWait = 5 * time.Second

// anchorPoll is how often snapserver is asked while SetMember waits for the anchor.
const anchorPoll = 200 * time.Millisecond

// localGroup starts this speaker's anchor and returns snapserver's status with the group
// the anchor is in. The anchor is parked while the speaker leads no group (anchor.go), and
// snapserver knows the group only by it: a snapserver that never saw it, a fresh
// installation's, has no group to add a member to until it connects. One it saw before
// keeps its record, connected or not, and is found at once.
func localGroup(ctx context.Context, env Env) (snapcast.Status, string, error) {
	if env.WantAnchor != nil {
		env.WantAnchor()
	}
	deadline := time.Now().Add(anchorWait)
	for {
		status, err := readStatus(ctx, env)
		if err != nil {
			return status, "", err
		}
		if groupID, found := FindClient(status, env.LocalID); found {
			return status, groupID, nil
		}
		if env.WantAnchor == nil || !time.Now().Before(deadline) {
			return status, "", &JoinError{ErrNoLocal, nil}
		}
		select {
		case <-ctx.Done():
			return status, "", &JoinError{ErrNoLocal, ctx.Err()}
		case <-time.After(anchorPoll):
		}
	}
}

// groupClients is group groupID's member list with client id in it or, with member false,
// left out.
func groupClients(status snapcast.Status, groupID, id string, member bool) []string {
	var clients []string
	for _, group := range status.Groups {
		if group.ID != groupID {
			continue
		}
		for _, client := range group.Clients {
			if client.ID != id || member {
				clients = append(clients, client.ID)
			}
		}
	}
	if member && !slices.Contains(clients, id) {
		clients = append(clients, id)
	}
	return clients
}

// othersConnected reports whether group groupID still has a connected client other than
// id and this speaker's own.
func othersConnected(status snapcast.Status, groupID, id, localID string) bool {
	active := false
	for _, group := range status.Groups {
		if group.ID == groupID {
			for _, client := range group.Clients {
				active = active || client.Connected && client.ID != id && client.ID != localID
			}
		}
	}
	return active
}

// PersistActive keeps the multiroom marker at path in step with active; "" keeps none.
func PersistActive(path string, active bool) error {
	if path == "" {
		return nil
	}
	if !active {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return atomicjson.WriteFile(path, []byte("enabled\n"), 0o600)
}
