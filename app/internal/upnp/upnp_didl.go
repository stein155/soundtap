// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"net/url"
	"regexp"
	"strings"
)

// didlMeta reads an (unescaped) DIDL-Lite doc. Its values are escaped once more: inside
// the DIDL "Rock &amp; Roll" is still XML. The cover ends up as an <img> in the
// web UI, so only a plain http(s) URL is kept.
func didlMeta(didl string) Meta {
	field := func(re *regexp.Regexp) string { return strings.TrimSpace(unescapeXML(firstGroup(re, didl))) }
	m := Meta{Title: field(reTitle), Artist: field(reArtist), Album: field(reAlbum), Art: field(reArt)}
	if m.Artist == "" {
		m.Artist = field(reCreator)
	}
	m.Duration, _ = parseHMS(firstGroup(reDuration, didl))
	m.Art = safeArt(m.Art)
	return m
}

// schemeHTTPS is one of the two schemes a media or cover URL may have.
const schemeHTTPS = "https"

// didl is the DIDL-Lite a controller reads back, built from what it sent about the track.
// Empty when nothing is known about it.
func (m Meta) didl() string {
	if m == (Meta{}) {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">` +
		`<item id="0" parentID="-1" restricted="1">`)
	for _, f := range []struct{ tag, val string }{
		{"dc:title", m.Title}, {"upnp:artist", m.Artist}, {"upnp:album", m.Album}, {"upnp:albumArtURI", m.Art},
	} {
		if f.val != "" {
			b.WriteString("<" + f.tag + ">" + xmlEscape(f.val) + "</" + f.tag + ">")
		}
	}
	b.WriteString(`<upnp:class>object.item.audioItem.musicTrack</upnp:class></item></DIDL-Lite>`)
	return b.String()
}

// safeArt keeps a cover URL only when it is a plain http(s) URL.
func safeArt(art string) string {
	if u, err := url.Parse(art); err != nil || (u.Scheme != "http" && u.Scheme != schemeHTTPS) || len(art) > 2048 {
		return ""
	}
	return art
}
