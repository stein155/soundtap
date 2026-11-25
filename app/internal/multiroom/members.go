// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"errors"
	"fmt"
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
func readStatus(ctx context.Context, env *Env) (snapcast.Status, error) {
	ctx, cancel := context.WithTimeout(ctx, localCallTimeout)
	defer cancel()

	status, err := env.Snapcast.GetStatus(ctx)
	if err != nil {
		return snapcast.Status{}, &JoinError{ErrStatus, err}
	}

	return status, nil
}

// SetVolume sets client name's volume and returns the volume it set. name may name the
// client either way; see ClientIDFor.
//
//nolint:gocritic // exported; web passes Env by value
func SetVolume(
	ctx context.Context, env Env, name string, volume snapcast.Volume,
) (snapcast.Volume, error) {
	status, err := readStatus(ctx, &env)
	if err != nil {
		return volume, err
	}

	clientID, found := ClientIDFor(status, name)
	if !found {
		return volume, &JoinError{ErrNoClient, nil}
	}

	ctx, cancel := context.WithTimeout(ctx, localCallTimeout)
	defer cancel()
	// This speaker's own client is the silent anchor: it stays muted (muteAnchor), and
	// its volume never reached a speaker anyway.
	if clientID == env.LocalID {
		volume.Muted = true
	}

	err = env.Snapcast.SetVolume(ctx, clientID, volume)
	if err != nil {
		return volume, &JoinError{ErrSetVolume, err}
	}

	return volume, nil
}

// SetMember puts client name (either spelling, see ClientIDFor) in this speaker's group or,
// with member false, takes it out.
//
//nolint:gocritic,revive // exported; web passes Env by value, and member is the membership asked for
func (g *Group) SetMember(ctx context.Context, env Env, name string, member bool) error {
	// The read of the group and the write of the new member list are one step: two
	// overlapping calls would each build their list from the old group, and the later
	// one would drop the room the earlier one added.
	g.members.Lock()
	defer g.members.Unlock()

	status, groupID, err := g.localGroup(ctx, &env)
	if err != nil {
		return err
	}
	// Resolved before the comparison below: a caller that named this speaker by its
	// device id must still be recognised as this speaker, or the guard against removing
	// the current client is bypassed by spelling it the other way.
	clientID, found := ClientIDFor(status, name)
	if !found {
		return &JoinError{ErrNoClient, nil}
	}

	if clientID == env.LocalID && !member {
		return &JoinError{ErrRemoveLocal, nil}
	}

	ctx, cancel := context.WithTimeout(ctx, localCallTimeout)
	defer cancel()

	clients := groupWithout(status, groupID, clientID)
	if member {
		clients = groupWith(status, groupID, clientID)
	}

	err = env.Snapcast.SetClients(ctx, groupID, clients)
	if err != nil {
		return &JoinError{ErrSetClients, err}
	}

	volume, found := FindClientVolume(status, clientID)
	if found {
		volume.Muted = !member

		err = env.Snapcast.SetVolume(ctx, clientID, volume)
		if err != nil {
			return &JoinError{ErrMemberVolume, err}
		}
	}

	if env.Active != nil {
		active := member || othersConnected(status, groupID, clientID, env.LocalID)
		env.Active(active)

		err = PersistActive(env.ActivePath, active)
		if err != nil {
			return &JoinError{ErrPersist, err}
		}
	}

	return nil
}

// anchorWait bounds how long SetMember waits for snapserver to know this speaker's own
// client after starting the anchor: a snapclient start and connect take about a second on
// these boards. It stays well under a peer call's timeout, so a follower's join is
// answered rather than timed out. Group.anchorWait overrides it for a test.
const anchorWait = 5 * time.Second

// anchorPoll is how often snapserver is asked while SetMember waits for the anchor.
const anchorPoll = 200 * time.Millisecond

// localGroup starts this speaker's anchor and returns snapserver's status with the group
// the anchor is in. The anchor is parked while the speaker leads no group (anchor.go), and
// snapserver knows the group only by it: a snapserver that never saw it, a fresh
// installation's, has no group to add a member to until it connects. One it saw before
// keeps its record, connected or not, and is found at once.
func (g *Group) localGroup(ctx context.Context, env *Env) (snapcast.Status, string, error) {
	if env.WantAnchor != nil {
		env.WantAnchor()
	}

	wait := g.anchorWait
	if wait == 0 {
		wait = anchorWait
	}

	deadline := time.Now().Add(wait)

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

// groupMembers is group groupID's member list.
func groupMembers(status snapcast.Status, groupID string) []string {
	var clients []string

	for _, group := range status.Groups {
		if group.ID != groupID {
			continue
		}

		for _, client := range group.Clients {
			clients = append(clients, client.ID)
		}
	}

	return clients
}

// groupWith is group groupID's member list with client id in it.
func groupWith(status snapcast.Status, groupID, id string) []string {
	clients := groupMembers(status, groupID)
	if !slices.Contains(clients, id) {
		clients = append(clients, id)
	}

	return clients
}

// groupWithout is group groupID's member list with client id left out; nil when that
// leaves nobody.
func groupWithout(status snapcast.Status, groupID, id string) []string {
	var clients []string

	for _, client := range groupMembers(status, groupID) {
		if client != id {
			clients = append(clients, client)
		}
	}

	return clients
}

// othersConnected reports whether group groupID still has a connected client other than
// id and this speaker's own.
func othersConnected(status snapcast.Status, groupID, clientID, localID string) bool {
	active := false

	for _, group := range status.Groups {
		if group.ID == groupID {
			for _, client := range group.Clients {
				active = active || client.Connected && client.ID != clientID && client.ID != localID
			}
		}
	}

	return active
}

// PersistActive keeps the multiroom marker at path in step with active; "" keeps none.
//
//nolint:revive // exported; active is the state to persist, not a mode
func PersistActive(path string, active bool) error {
	if path == "" {
		return nil
	}

	if !active {
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove multiroom marker: %w", err)
		}

		return nil
	}

	err := atomicjson.WriteFile(path, []byte("enabled\n"), stateFileMode)
	if err != nil {
		return fmt.Errorf("write multiroom marker: %w", err)
	}

	return nil
}
