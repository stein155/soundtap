# 0122. Multiroom group state lives in internal/multiroom, not in the web server

Status: accepted — 2026-11-13

## Context

The multiroom state machine (whom this speaker follows, the leader watchdog's failure count, the
standby mark, the peer calls to a leader and to followers, the follower volume sync) sat in
`app/internal/web` next to the HTTP handlers. It could only be tested through `httptest`, and
several races came from handlers and watchers sharing the Server's mutexes.

## Decision

`app/internal/multiroom` owns the group. Its `Group` type holds the follow (address and id, one
file), the lock that makes every change of it one step, the leader watchdog and power sync, the
follower volume sync and the release of followers. Pure helpers over a snapcast status (client
ids, listeners) live there too.

`web` keeps the routes, request parsing, status codes and JSON, and the HTTP calls to other
speakers. On each call it hands the Group a `multiroom.Env` built from its current wiring, so an
unset part still means "not wired". soundtapd keeps calling the Server. The orderings that
matter are part of the Group: `LeaveFollow` checks and writes under the follow lock, and joining
a group switches the source before it restarts snapclient.

Alternatives that lost: leaving it in `web` (the package stays three times the size of the next
one and the races stay hard to test); one package per concern, such as follow, volume sync and
latency (they share the follow and the peer client, so the shared state would only move to a
third place).

## Consequences

- The follow state can be unit tested without a Server or HTTP.
- Anything that changes the follow goes through `Group`; web code must not write the follow
  file or take the follow lock itself.

## Code

- `app/internal/multiroom/group.go`: `multiroom.Group`, `multiroom.Env`,
  `multiroom.Group.Join`, `multiroom.Group.LeaveFollow`, `multiroom.Group.CheckLeader` and
  `multiroom.Group.SyncLeaderPower`.
- `app/internal/multiroom/followers.go`: `multiroom.Group.SyncFollowerVolume` and
  `multiroom.Group.ReleaseFollowers`.
- `app/internal/multiroom/status.go`: the helpers over a snapcast status.
- `app/internal/web/multiroom_follow.go:groupEnv`: the Env the web server hands the Group.
