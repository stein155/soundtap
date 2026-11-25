// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"time"

	"github.com/stein155/soundtap/app/internal/snapcast"
)

// Backend is this speaker's snapserver.
type Backend interface {
	GetStatus(ctx context.Context) (snapcast.Status, error)
	SetVolume(ctx context.Context, id string, volume snapcast.Volume) error
	SetClients(ctx context.Context, groupID string, clients []string) error
	SetLatency(ctx context.Context, id string, ms int) error
}

// Client is one speaker in the /api/multiroom list.
type Client struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	IP      string `json:"ip"`
	Percent int    `json:"percent"`
	Muted   bool   `json:"muted"`
	GroupID string `json:"groupId"`
	// DeviceID is the same speaker as /api/devices calls it, or "" for a snapcast
	// client that is not a SoundTap speaker. Without it a controller holding a device
	// list has to know how the audio layer spells the same box to act on it.
	DeviceID string `json:"deviceId,omitempty"`
	Current  bool   `json:"current"`
	Member   bool   `json:"member"`
	// Diagnostics. A client that dropped out stays in the list as connected:false
	// rather than disappearing: a speaker that silently vanishes from the group looks
	// like a bug in the app, while "offline, last seen 2 minutes ago" is a fact the
	// user can act on. lastSeenAgo is seconds and only meaningful when offline.
	Connected   bool `json:"connected"`
	Latency     int  `json:"latency"`
	LastSeenAgo int  `json:"lastSeenAgo,omitempty"`
}

// Clients lists every client of status, with this speaker (localID) first when snapserver
// has not seen its client yet. names is what discovery calls each speaker, by device id;
// localName is this speaker's own name ("" when not set) and localVolume its volume.
func Clients(
	status snapcast.Status, localID string, names map[string]string, localName string, localVolume int,
) []Client {
	localGroup, _ := FindClient(status, localID)
	rows := make([]Client, 0)
	localSeen := false

	for _, group := range status.Groups {
		for i := range group.Clients {
			client := &group.Clients[i]
			localSeen = localSeen || client.ID == localID
			rows = append(rows, clientRow(client, group.ID, localID, localGroup, names, localName))
		}
	}
	// One speaker, one row. Changing a speaker's snapclient host id -- which is what
	// deriving it from the identity rather than a MAC did -- leaves the old id behind in
	// snapcast's own client list, disconnected and never coming back. Keeping
	// disconnected clients is deliberate ("offline, last seen 2 minutes ago" is a fact
	// somebody can act on), but a ghost of a speaker that is sitting right there,
	// connected, under its new name is not: it is one box claiming to be two. Measured on
	// the board after the upgrade, where soundtap-<mac> and soundtap-<serial> both appeared.
	rows = dropGhosts(dropForgotten(rows))

	if !localSeen && localID != "" {
		name := "SoundTap"
		if localName != "" {
			name = localName
		}

		rows = append([]Client{{
			ID: localID, DeviceID: DeviceIDFor(localID),
			Name: name, Host: "", IP: "", Percent: localVolume, Muted: false,
			GroupID: localGroup, Current: true, Member: true, Connected: true, Latency: 0, LastSeenAgo: 0,
		}}, rows...)
	}

	return rows
}

// clientRow is the row of one snapcast client in group groupID.
func clientRow(
	client *snapcast.ClientInfo, groupID, localID, localGroup string, names map[string]string, localName string,
) Client {
	// Discovery knows each speaker by the name its owner gave it; snapcast only knows
	// what snapclient sent at start, which on the ST20 is the hostname "(none)".
	name := names[DeviceIDFor(client.ID)]
	if name == "" {
		name = client.Config.Name
	}

	if client.ID == localID && localName != "" {
		name = localName
	}

	if name == "" && client.Host.Name != "(none)" {
		name = client.Host.Name
	}

	if name == "" {
		name = client.ID
	}
	// This speaker's own row is the speaker answering, whether its anchor runs or is
	// parked in standby (anchor.go): offline it would read as gone, and after a week of
	// standby dropForgotten would drop the speaker from its own list.
	connected := client.Connected || client.ID == localID
	ago := 0

	if !connected && client.LastSeen.Sec > 0 {
		if d := int(time.Now().Unix() - client.LastSeen.Sec); d > 0 {
			ago = d
		}
	}

	return Client{
		ID: client.ID, DeviceID: DeviceIDFor(client.ID),
		Name: name, Host: client.Host.Name, IP: client.Host.IP,
		Percent: client.Config.Volume.Percent, Muted: client.Config.Volume.Muted && client.ID != localID,
		GroupID: groupID, Current: client.ID == localID,
		Member:    localGroup != "" && groupID == localGroup,
		Connected: connected, Latency: client.Config.Latency, LastSeenAgo: ago,
	}
}

// dropGhosts removes a disconnected client when the same speaker is also present and
// connected. Identity is the device id, so this only ever collapses two spellings of one
// box; a client with no device id is not ours and is left exactly as it is.
func dropGhosts(rows []Client) []Client {
	live := make(map[string]bool, len(rows))

	liveHost := make(map[string]bool, len(rows))
	for i := range rows {
		row := &rows[i]
		if row.DeviceID != "" && row.Connected {
			live[row.DeviceID] = true
			// The old MAC-based id and the serial-based one are different device ids for
			// the same box, but the box kept its hostname (the WLA, 2026-10-01).
			if row.Host != "" && row.Host != "(none)" {
				liveHost[row.Host] = true
			}
		}
	}

	if len(live) == 0 {
		return rows
	}

	kept := rows[:0]
	for i := range rows {
		if !rows[i].Connected && rows[i].DeviceID != "" && (live[rows[i].DeviceID] || liveHost[rows[i].Host]) {
			continue
		}

		kept = append(kept, rows[i])
	}

	return kept
}

// forgetAfter is how long a speaker may stay offline before its row is dropped as gone.
const forgetAfter = 7 * 24 * time.Hour

// dropForgotten removes offline clients that are gone for good: ones offline longer than
// forgetAfter. snapserver keeps every client it ever saw in server.json, so without this
// the list grows with each replaced speaker.
func dropForgotten(rows []Client) []Client {
	kept := rows[:0]
	for i := range rows {
		if !rows[i].Connected && time.Duration(rows[i].LastSeenAgo)*time.Second > forgetAfter {
			continue
		}

		kept = append(kept, rows[i])
	}

	return kept
}
