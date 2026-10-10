// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Client for soundtapd's JSON API at /api/*.
//
// Every call takes an optional `speaker`: left out, it goes to the speaker the page is
// pointed at; '' is the speaker that served the page; any other id goes through that
// speaker's /api/devices/{id}/ proxy (see base.ts).
//
// The error contract: reads resolve to null when the speaker could not answer (or to a
// documented empty value where "nothing" and "unknown" mean the same to the UI), so a
// page can tell "unreachable" from "empty"; writes reject with an ApiError carrying the
// HTTP status and the speaker's own error text.

export * from './playback';
export * from './library';
export * from './multiroom';
export * from './tunein';
export * from './settings';
export * from './firmware';
export * from './upnp';
export { deadline } from './http';
