// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/upnp"
	"github.com/stein155/soundtap/app/internal/web/apierror"
)

// notifyAudioMax caps an uploaded notification clip. Clips are short chimes that
// SoundTap holds in RAM on the memory-constrained speaker, so this is deliberately
// tight — a few seconds of MP3/AAC is well under a megabyte; 4 MiB covers a generous
// clip while bounding what a buggy or hostile caller can make us allocate.
const notifyAudioMax = 4 << 20

// notifyClipTTL is how long an uploaded clip stays fetchable after upload. The
// notification player fetches it once, right after the upload; a couple of minutes
// covers a slow fetch or a retry, after which the clip is swept.
const notifyClipTTL = 2 * time.Minute

// maxAudioClips bounds how many uploaded clips we hold at once (oldest evicted first),
// a second backstop on memory beyond the per-clip size cap and the TTL sweep.
const maxAudioClips = 8

// audioClip is one uploaded notification clip, served back to the player on loopback.
type audioClip struct {
	data        []byte
	contentType string
	created     time.Time
	expires     time.Time
}

// speakerNotifyBody is the JSON form of /api/speaker/notify: play a URL the player
// fetches itself (an already-hosted clip, or a Google TTS URL — no hosting needed).
type speakerNotifyBody struct {
	URL    string `json:"url"`
	Volume int    `json:"volume"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
	Track  string `json:"track"`
	// An appKey or app_key that clients written for Bose's SoundTouch API still send is
	// accepted and ignored: the decoder skips fields it does not know.
}

// speakerNotify plays an audio notification over what is playing.
// LAN controllers may trigger it; the normal host and CSRF guard still applies.
//
// Two shapes, by Content-Type:
//   - application/json {url,...}: play a URL the player fetches directly. Use this
//     for remote clips and for Google TTS (pass the translate_tts URL straight through).
//   - anything else: the body IS the audio file. SoundTap caches it and serves it back
//     to the player on loopback, so a local caller can provide a chime without hosting it.
//     Metadata comes from the query string (?volume=&artist=&album=&track=); an
//     appKey or app_key parameter is accepted and ignored.
func (s *Server) speakerNotify(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		s.speakerNotifyURL(w, r)
		return
	}
	s.speakerNotifyUpload(w, r)
}

// notifyJSONMax bounds the JSON-URL path of /api/speaker/notify. The guard sets
// the body limit to notifyAudioMax (4 MiB) so the upload path has room; for the
// JSON path (a URL + short metadata) a few kilobytes is more than enough.
const notifyJSONMax = 4 << 10

func (s *Server) speakerNotifyURL(w http.ResponseWriter, r *http.Request) {
	var body speakerNotifyBody
	if err := json.NewDecoder(io.LimitReader(r.Body, notifyJSONMax)).Decode(&body); err != nil {
		s.badRequest(w, "bad body", err)
		return
	}
	if body.URL == "" {
		apiError(w, http.StatusBadRequest, "url required")
		return
	}
	if !upnp.MediaURL(r.Context(), body.URL) {
		apiError(w, http.StatusBadRequest, "url must be http(s) and not loopback or link-local")
		return
	}
	s.playNotification(w, r, speaker.Notification{
		URL: body.URL, Volume: body.Volume,
		Artist: body.Artist, Album: body.Album, Track: body.Track,
	})
}

func (s *Server) speakerNotifyUpload(w http.ResponseWriter, r *http.Request) {
	// Each upload is read whole into memory, and the route is open to the LAN: uploads
	// side by side could take the speaker's RAM. A doorbell sends one at a time.
	if !s.clipUpload.TryLock() {
		apiErrorCode(w, http.StatusServiceUnavailable, apierror.Busy, "another clip is uploading")
		return
	}
	data, err := io.ReadAll(r.Body)
	s.clipUpload.Unlock()
	if err != nil {
		// The guard caps the body at notifyAudioMax; a MaxBytesError means too large.
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			apiError(w, http.StatusRequestEntityTooLarge, "audio too large")
			return
		}
		s.badRequest(w, "read audio", err)
		return
	}
	if len(data) == 0 {
		apiError(w, http.StatusBadRequest, "empty audio body")
		return
	}
	if s.notifyBase == "" {
		// Nothing told us where the player can fetch a clip back from.
		apiError(w, http.StatusServiceUnavailable, "notifications unavailable")
		return
	}
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		ct = "audio/mpeg"
	}
	name := s.storeClip(data, ct)

	q := r.URL.Query()
	volume, _ := strconv.Atoi(q.Get("volume"))
	n := speaker.Notification{
		URL:    s.audioURL(name),
		Volume: volume,
		Artist: q.Get("artist"),
		Album:  q.Get("album"),
		Track:  q.Get("track"),
	}
	if !s.playNotification(w, r, n) {
		// The player never got a URL it could fetch, so drop the clip now instead
		// of holding the bytes for the full TTL.
		s.dropClip(name)
	}
}

// playNotification runs the /speaker call and writes the HTTP response. It reports
// whether the request succeeded so callers can clean up on failure.
func (s *Server) playNotification(w http.ResponseWriter, r *http.Request, n speaker.Notification) bool {
	n.Volume = s.notificationVolume(n.Volume)
	// Notifications interrupt playback and can wake the speaker, so allow more room
	// than the default 4s speaker timeout.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.backend.PlayNotification(ctx, n); err != nil {
		s.fail(w, "notification failed", err)
		return false
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return true
}

// notificationVolumeMax is the ceiling for this model. The Wireless Link drives its own
// amplifier and is quiet at 70, so it gets the full range; the others clip above it.
func (s *Server) notificationVolumeMax() int {
	if model, err := s.fw.Model(); err == nil && model == "wla" {
		return 100
	}
	return 70
}

func (s *Server) notificationVolume(volume int) int {
	if volume == 0 {
		return 50
	}
	max := s.notificationVolumeMax()
	if volume < 10 {
		return 10
	}
	if volume > max {
		return max
	}
	return volume
}

// serveAudio hands an uploaded clip back to the player. Loopback-only: the only
// client is soundtapd's own notification player fetching over 127.0.0.1.
func (s *Server) serveAudio(w http.ResponseWriter, r *http.Request) {
	if !s.loopbackOnly(w, r) {
		return
	}
	name := r.PathValue("name")
	s.audioMu.Lock()
	clip := s.builtinClips[name] // permanent clips first
	if clip == nil {
		clip = s.audioClips[name]
		if clip != nil && time.Now().After(clip.expires) {
			clip = nil
		}
	}
	s.audioMu.Unlock()
	if clip == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", clip.contentType)
	// ServeContent handles range requests, which the player's fetcher may use.
	http.ServeContent(w, r, name, clip.created, bytes.NewReader(clip.data))
}

func (s *Server) loopbackOnly(w http.ResponseWriter, r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return true
		}
	}
	apiError(w, http.StatusForbidden, "loopback only")
	return false
}

// storeClip caches an uploaded clip and returns the URL name (id + extension) the
// player will fetch it under. Sweeps expired clips and bounds the total held.
func (s *Server) storeClip(data []byte, contentType string) string {
	now := time.Now()
	name := clipID() + extForContentType(contentType)
	s.audioMu.Lock()
	defer s.audioMu.Unlock()
	if s.audioClips == nil {
		s.audioClips = make(map[string]*audioClip)
	}
	for k, c := range s.audioClips {
		if now.After(c.expires) {
			delete(s.audioClips, k)
		}
	}
	for len(s.audioClips) >= maxAudioClips {
		oldest := ""
		for k, c := range s.audioClips {
			if oldest == "" || c.created.Before(s.audioClips[oldest].created) {
				oldest = k
			}
		}
		delete(s.audioClips, oldest)
	}
	s.audioClips[name] = &audioClip{data: data, contentType: contentType, created: now, expires: now.Add(notifyClipTTL)}
	return name
}

func (s *Server) dropClip(name string) {
	s.audioMu.Lock()
	delete(s.audioClips, name)
	s.audioMu.Unlock()
}

// SetBuiltinClip registers a permanent notification clip served at
// /api/speaker/audio/{name}. Unlike uploaded clips, builtin clips have no TTL
// and are never evicted, so they remain fetchable for the lifetime of the process.
// Typical use: the preset-store confirmation tone generated at startup.
func (s *Server) SetBuiltinClip(name string, data []byte, contentType string) {
	s.audioMu.Lock()
	defer s.audioMu.Unlock()
	if s.builtinClips == nil {
		s.builtinClips = make(map[string]*audioClip)
	}
	s.builtinClips[name] = &audioClip{data: data, contentType: contentType, created: time.Now()}
}

// audioURL builds the URL the player fetches an uploaded clip from: SoundTap's own
// loopback address, as SpeakerOptions.NotifyBaseURL gave it.
func (s *Server) audioURL(name string) string {
	return s.notifyBase + "/api/speaker/audio/" + name
}

func clipID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// extForContentType maps the common audio types to a file extension, so the fetched
// URL carries one for the player to go by.
func extForContentType(ct string) string {
	switch {
	case strings.Contains(ct, "mpeg"), strings.Contains(ct, "mp3"):
		return ".mp3"
	case strings.Contains(ct, "aac"), strings.Contains(ct, "mp4"):
		return ".aac"
	case strings.Contains(ct, "wav"):
		return ".wav"
	case strings.Contains(ct, "flac"):
		return ".flac"
	case strings.Contains(ct, "ogg"), strings.Contains(ct, "vorbis"):
		return ".ogg"
	default:
		return ".mp3"
	}
}
