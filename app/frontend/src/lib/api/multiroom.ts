// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { getJSON, send } from './http';
import type { MultiroomClient, SpeakerDevice } from '../types';

// The snapcast clients on a speaker's server; null when it runs none (or is unreachable).
export async function getMultiroom(speaker?: string): Promise<MultiroomClient[] | null> {
  try {
    return await getJSON<MultiroomClient[]>('/api/multiroom', speaker);
  } catch {
    return null;
  }
}

export async function getDevices(): Promise<SpeakerDevice[]> {
  const body = await getJSON<{ devices?: SpeakerDevice[] }>('/api/devices');
  return body.devices ?? [];
}

// Ask another speaker to play what this one is playing. This speaker places the call: a
// page may not talk to a second speaker, and the address the other one needs is this one's.
// `speaker` is the one that will lead: the invited one follows it.
export function inviteSpeaker(ip: string, speaker?: string) {
  return send('/api/multiroom/invite', 'POST', { ip }, speaker);
}

// Put a speaker on another one's stream, or with an empty host back on its own. This is
// how a speaker leaves a group: the leader cannot drop it, only the speaker can stop
// following.
export function followSpeaker(host: string, speaker?: string) {
  return send('/api/multiroom/follow', 'POST', { host }, speaker);
}

// What a speaker plays of a stereo source: both channels, one as half of a stereo pair, or
// both mixed to mono.
export type Channel = 'stereo' | 'left' | 'right' | 'mono';

export async function getChannel(speaker?: string): Promise<Channel | null> {
  try {
    return (await getJSON<{ channel: Channel }>('/api/channel', speaker)).channel;
  } catch {
    return null;
  }
}

export function setChannel(channel: Channel, speaker?: string) {
  return send('/api/channel', 'PUT', { channel }, speaker);
}
