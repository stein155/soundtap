// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// The two sources a follow moves between.
const (
	SourceMultiroom = "MULTIROOM"
	SourceStandby   = "STANDBY"
)

// Env is how a Group reaches the rest of the speaker, as it is wired at the moment of
// the call. A nil func is a part this speaker does not have.
type Env struct {
	Log *slog.Logger
	// Path is the follow file; "" means no settings directory.
	Path string
	// NowPlaying is nil without a speaker backend.
	NowPlaying        func(context.Context) (*speaker.NowPlaying, error)
	SelectSource      func(context.Context, string) error
	SourceChanging    func(string)
	RestartSnapclient func()
	LeaderLive        func(bool)
	LeaderNow         func(speaker.NowPlaying)
	// Nudge is nil without an event hub.
	Nudge    func()
	DeviceIP func(id string) string
	// The calls to other speakers.
	PeerLeader      func(ctx context.Context, host string) string
	PeerNow         func(ctx context.Context, host string) (speaker.NowPlaying, bool)
	LeaderReachable func(ctx context.Context, host string) bool
	// FollowerPeers is the API base URL of every speaker following this one.
	FollowerPeers func(context.Context) []string
	// StepPeerVolume sets one follower delta further than from (read first when not
	// known), clamped, and returns the unclamped level.
	StepPeerVolume func(ctx context.Context, base string, delta, from int, known bool) (int, error)
	// ReleasePeer tells one follower to stop following.
	ReleasePeer func(ctx context.Context, base string) error
	// JoinLeaderGroup asks the leader at host to take this speaker's snapclient into its
	// Snapcast group; nil without a snapclient.
	JoinLeaderGroup func(host string)
	// PeerTimeout bounds one call to a peer.
	PeerTimeout time.Duration
	// Snapcast is this speaker's snapserver, LocalID its own snapclient (members.go).
	Snapcast Backend
	LocalID  string
	// WantAnchor starts this speaker's own snapclient, the group anchor, and keeps it
	// running for AnchorHold (anchor.go); nil where it is not parked.
	WantAnchor func()
	// Active turns the multiroom tap on or off; nil when not wired. ActivePath is the
	// marker that keeps it on across a restart, "" for none.
	Active     func(bool)
	ActivePath string
	// Downstream asks the follower at ip how long its audio chain is, in ms (watch.go).
	Downstream func(ctx context.Context, ip string) (int, error)
}

// Group is this speaker's place in a multiroom group: whom it follows, and the one lock
// every change of that takes. Its zero value is ready to use.
type Group struct {
	// mu makes a change of whom this speaker follows, with its snapclient restart and
	// source switch, one step: a slow WatchLeader check acts only if the follow it
	// checked is still the current one.
	mu sync.Mutex
	// keep is the deadline (unix nanos) of the standby SyncLeaderPower is entering, 0
	// for none, see KeepFollow.
	keep atomic.Int64
	// leaderKey is the follow (host and id) CheckLeader counted its failures against.
	leaderKey string

	// volSync coalesces the follower volume steps (followers.go).
	volSync volumeSync
	// peerJobs tracks the best-effort calls to followers (followers.go), so a test can
	// wait for them instead of sleeping.
	peerJobs sync.WaitGroup
	// members makes SetMember's read-group/write-clients one step (members.go).
	members sync.Mutex
	// anchorWait replaces the package's anchorWait when set; a test shortens it.
	anchorWait time.Duration
}

// ReadFollow is whom this speaker follows: the leader's address and device id, "" when
// unknown. Both live in one file, address on the first line and id on the second, so one
// write changes them together: a power cut can never leave one leader's address with
// another's id.
func ReadFollow(path string) (string, string) {
	if path == "" {
		return "", ""
	}

	data, _ := os.ReadFile(path)
	host, deviceID, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")

	return strings.TrimSpace(host), strings.TrimSpace(deviceID)
}

// WriteFollow writes the leader's address and id, or with an empty host removes the
// file. snapclient is pointed at the first line only; the id is for CheckLeader. Only
// Group calls it outside tests, under its lock.
func WriteFollow(path, host, deviceID string) error {
	if host == "" {
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove follow: %w", err)
		}

		return nil
	}

	err := atomicjson.WriteFile(path, []byte(host+"\n"+deviceID+"\n"), stateFileMode)
	if err != nil {
		return fmt.Errorf("write follow: %w", err)
	}

	return nil
}

// stateFileMode is the mode of the follow file and the multiroom marker.
const stateFileMode = 0o600

// Bounds of the calls this package makes.
const (
	// localCallTimeout bounds one call to this speaker's own snapserver or source switch.
	localCallTimeout = 5 * time.Second
	// leaderNowTimeout bounds one ask of what the leader plays.
	leaderNowTimeout = 4 * time.Second
)

// The steps of Join that can fail, so the caller can tell them apart.
var (
	ErrWrite   = errors.New("follow speaker failed")
	ErrUndo    = errors.New("undo follow failed")
	ErrChained = errors.New("that speaker follows another; group with its leader")
	ErrSelect  = errors.New("select multiroom source failed")
)

// JoinError is a failed step of Join, SetVolume or SetMember: Step is one of the Err
// values above or in members.go, Err the cause, nil for a refusal such as ErrChained.
type JoinError struct {
	Step error
	Err  error
}

func (e *JoinError) Error() string {
	if e.Err == nil {
		return e.Step.Error()
	}

	return e.Step.Error() + ": " + e.Err.Error()
}

func (e *JoinError) Unwrap() []error { return []error{e.Step, e.Err} }

// Join makes this speaker follow host (deviceID is the leader's, "" if unknown), or
// with an empty host puts it back on its own, and returns whom it followed before. The
// caller has already checked that host may be followed.
//
//nolint:gocritic // exported; web passes Env by value
func (g *Group) Join(ctx context.Context, env Env, host, deviceID string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	prevHost, prevID := ReadFollow(env.Path)

	err := WriteFollow(env.Path, host, deviceID)
	if err != nil {
		return prevHost, &JoinError{ErrWrite, err}
	}
	// The peer check before Join and this write are not one step: two speakers grouping
	// with each other at once both read "follows nobody" and both commit. Asking again now
	// that ours is written means whichever commits last sees the other's follow and backs out.
	if host != "" && env.PeerLeader(ctx, host) != "" {
		err = WriteFollow(env.Path, prevHost, prevID)
		if err != nil {
			return prevHost, &JoinError{ErrUndo, err}
		}

		return prevHost, &JoinError{ErrChained, nil}
	}
	// The source switch comes first, as on a local start: the old source stops and the
	// follower's hold closes before the leader's stream can reach the bus.
	err = selectFollowSource(ctx, &env, host)
	// The supervisor restarts the wrapper, which reads the file again. Killing the client
	// is the whole handover; nothing here waits for it, because the speaker answering this
	// request is the one whose audio is about to change.
	if env.RestartSnapclient != nil {
		env.RestartSnapclient()
	}

	if err != nil {
		return prevHost, &JoinError{ErrSelect, err}
	}
	// snapserver remembers each client's group and mute: a speaker once taken out of the
	// leader's group comes back to it muted and plays silence. Every follow, an
	// invite's too, comes through here; the call to the leader is best effort.
	if host != "" && env.JoinLeaderGroup != nil {
		g.peerJobs.Add(1)
		safego.Go("multiroom.joinLeaderGroup", func() {
			defer g.peerJobs.Done()

			env.JoinLeaderGroup(host)
		})
	}

	return prevHost, nil
}

// selectFollowSource: following means this speaker plays what arrives on the bus, so it
// needs a source the arbiter will grant the card to; leaving means it stops.
func selectFollowSource(ctx context.Context, env *Env, host string) error {
	source := SourceMultiroom
	if host == "" {
		source = SourceStandby
	}
	// What setUserSource does before a switch: stop the previous source and drop its
	// queued audio, so it ends in the Mixer's underrun fade.
	if env.SourceChanging != nil {
		env.SourceChanging(source)
	}

	if env.SelectSource == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, localCallTimeout)
	defer cancel()

	return env.SelectSource(ctx, source)
}

// keepFollowFor is how long a standby marked by SyncLeaderPower may take to show up as
// a source change. A mark nobody consumed (the source changed under it) dies after this,
// instead of swallowing a later real leave.
const keepFollowFor = 2 * time.Second

// KeepFollow reports, once, whether the leave from MULTIROOM now happening is the standby
// the group asked for (SyncLeaderPower), so it must not end the follow. A leave to any
// other source, or a mark gone stale, is a real leave; the mark is used up either way.
//
//nolint:gocritic // exported; web passes Env by value
func (g *Group) KeepFollow(env Env) bool {
	until := g.keep.Swap(0)
	if until <= time.Now().UnixNano() || env.NowPlaying == nil {
		return false
	}

	np, err := env.NowPlaying(context.Background())

	return err == nil && np != nil && np.Source == SourceStandby
}

// LeaveFollow ends the follow of host and deviceID after the source left MULTIROOM. It writes
// under the lock like every other change of the follow, and only ends that one follow: a
// late leave never removes a follow written since, nor one the speaker went back to
// MULTIROOM with.
//
//nolint:gocritic // exported; web passes Env by value
func (g *Group) LeaveFollow(env Env, host, deviceID string) {
	if g.KeepFollow(env) || host == "" {
		return // the leader went to standby: so does this follower, still in the group
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if h, i := ReadFollow(env.Path); h != host || i != deviceID {
		return
	}

	if env.NowPlaying != nil {
		np, err := env.NowPlaying(context.Background())
		if err == nil && np != nil && np.Source == SourceMultiroom {
			return
		}
	}

	err := WriteFollow(env.Path, "", "")
	if err != nil {
		env.Log.Warn("multiroom: could not leave the group", "err", err)

		return
	}

	if env.RestartSnapclient != nil {
		env.RestartSnapclient()
	}
}

// SyncLeaderPower runs one WatchLeaderPower tick. Like CheckLeader, it asks the leader
// without the lock and acts under it only if the follow is still the one asked, so a late
// answer never overrules leaving the group or picking another leader.
//
//nolint:gocritic // exported; web passes Env by value
func (g *Group) SyncLeaderPower(ctx context.Context, env Env) {
	host, deviceID := ReadFollow(env.Path)
	if host == "" || env.SelectSource == nil || env.NowPlaying == nil {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, leaderNowTimeout)
	defer cancel()

	leader, ok := env.PeerNow(ctx, host)
	if env.LeaderLive != nil {
		env.LeaderLive(ok)
	}

	if !ok {
		return // leaving the group is CheckLeader's business
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if h, i := ReadFollow(env.Path); h != host || i != deviceID {
		return
	}

	np, err := env.NowPlaying(ctx)
	if err != nil || np == nil {
		return
	}

	if g.matchLeader(ctx, &env, np.Source, &leader, host) && env.Nudge != nil {
		env.Nudge()
	}
}

// LeaderGiveUp is how many failed checks in a row (one per WatchLeader tick) a follower
// waits before it stops following: at 30 s a tick, five minutes.
const LeaderGiveUp = 10

// CheckLeader runs one WatchLeader tick and returns the failure count for the next. The
// count belongs to the follow it was made against and starts over when the user picks
// another leader. Reachability is checked without the lock, since it can take seconds;
// anything it leads to is done under the lock and only if the follow is still the one
// that was checked, so a follow chosen meanwhile is never undone by a stale verdict.
//
//nolint:gocritic // exported; web passes Env by value
func (g *Group) CheckLeader(ctx context.Context, env Env, fails int) int {
	host, deviceID := ReadFollow(env.Path)
	if key := host + "\x00" + deviceID; key != g.leaderKey {
		g.leaderKey, fails = key, 0
	}

	if host == "" || env.LeaderReachable(ctx, host) {
		return 0
	}

	if deviceID != "" {
		if ip := env.DeviceIP(deviceID); ip != "" && ip != host && env.LeaderReachable(ctx, ip) {
			return g.followMovedLeader(&env, host, deviceID, ip, fails)
		}
	}

	if fails+1 < LeaderGiveUp {
		return fails + 1
	}

	g.leaveLostLeader(ctx, &env, host, deviceID)

	return 0
}

// matchLeader brings a follower on source in line with what the leader is doing, and says
// whether that changed anything.
func (g *Group) matchLeader(
	ctx context.Context, env *Env, source string, leader *speaker.NowPlaying, host string,
) bool {
	leaderOn := leader.Source != SourceStandby
	switch {
	case source == SourceMultiroom && !leaderOn:
		// Standby makes the arbiter drop the follow (a follower that picks another
		// source leaves the group); this standby is the group's, so it stays.
		g.keep.Store(time.Now().Add(keepFollowFor).UnixNano())

		err := env.SelectSource(ctx, SourceStandby)
		if err != nil {
			g.keep.Store(0)
			env.Log.Warn("multiroom: standby with the leader failed", "err", err)

			return false
		}

		env.Log.Info("multiroom: leader in standby; following it there", "leader", host)
		// Off MULTIROOM snapclient goes back to the silent anchor: left on the leader, it
		// kept the whole chain writing the leader's silence into the bus.
		restartSnapclient(env)

		return true
	case source == SourceMultiroom:
		if env.LeaderNow != nil {
			env.LeaderNow(*leader)
		}
	case source == SourceStandby && leaderOn:
		err := env.SelectSource(ctx, SourceMultiroom)
		if err != nil {
			env.Log.Warn("multiroom: joining the playing leader failed", "err", err)

			return false
		}

		env.Log.Info("multiroom: leader playing; joined", "leader", host)
		// After the source switch, as in Join: back on MULTIROOM, snapclient follows
		// the leader again.
		restartSnapclient(env)

		return true
	default:
		// Already in line with the leader.
	}

	return false
}

// followMovedLeader points the follow of host at the leader's new address newIP and returns the
// failure count for the next tick.
func (g *Group) followMovedLeader(env *Env, host, deviceID, newIP string, fails int) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	if h, i := ReadFollow(env.Path); h != host || i != deviceID {
		return 0
	}

	err := WriteFollow(env.Path, newIP, deviceID)
	if err != nil {
		env.Log.Warn("multiroom: could not follow the leader's new address", "err", err)

		return fails + 1
	}

	g.leaderKey = newIP + "\x00" + deviceID
	env.Log.Info("multiroom: leader moved", "from", host, "to", newIP)

	if env.RestartSnapclient != nil {
		env.RestartSnapclient()
	}

	return 0
}

// leaveLostLeader leaves the group of a leader that stopped answering and goes to standby.
func (g *Group) leaveLostLeader(ctx context.Context, env *Env, host, id string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if h, i := ReadFollow(env.Path); h != host || i != id {
		return
	}

	env.Log.Warn("multiroom: leader gone; leaving the group", "leader", host)

	err := WriteFollow(env.Path, "", "")
	if err != nil {
		env.Log.Warn("multiroom: could not leave the group", "err", err)

		return
	}

	if env.RestartSnapclient != nil {
		env.RestartSnapclient()
	}

	if env.SelectSource != nil {
		sctx, cancel := context.WithTimeout(ctx, localCallTimeout)
		defer cancel()

		err := env.SelectSource(sctx, SourceStandby)
		if err != nil {
			env.Log.Warn("multiroom: standby after leaving failed", "err", err)
		}
	}

	if env.Nudge != nil {
		env.Nudge()
	}
}

func restartSnapclient(env *Env) {
	if env.RestartSnapclient != nil {
		env.RestartSnapclient()
	}
}
