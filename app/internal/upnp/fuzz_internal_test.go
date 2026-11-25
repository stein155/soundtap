// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

// What arrives from the LAN: SSDP datagrams, media servers' Browse answers (SOAP around
// DIDL-Lite) and the DIDL and times controllers send the renderer. `go test` runs the
// seeds; `go test -fuzz=FuzzX ./internal/upnp` searches further.

func FuzzParseAdvert(f *testing.F) {
	f.Add("HTTP/1.1 200 OK\r\nST: " + mediaServerURN + "\r\nUSN: uuid:1::" + mediaServerURN +
		"\r\nLOCATION: http://192.0.2.5:8200/desc.xml\r\nCACHE-CONTROL: max-age=1800\r\n\r\n")
	f.Add("HTTP/1.1 200 OK\r\nst: " + mediaServerURN + "\r\nusn: u\r\nlocation: l\r\n" +
		"cache-control: max-age = 99999999999999999999\r\n")
	f.Add("NOTIFY * HTTP/1.1\r\nNT: upnp:rootdevice\r\n\r\n")
	f.Add("M-SEARCH * HTTP/1.1\r\nST: ssdp:all\r\n")
	f.Fuzz(func(t *testing.T, msg string) {
		_ = searchTarget(msg)

		reply, ok := parseAdvert(msg)
		if !ok {
			return
		}

		if reply.usn == "" || reply.location == "" || reply.ttl <= 0 || reply.ttl > maxTTL {
			t.Fatalf("advert %+v from %q", reply, msg)
		}
	})
}

func FuzzParseBrowseResponse(f *testing.F) {
	didl := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" ` +
		`xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"><container id="1"><dc:title>Music</dc:title>` +
		`<upnp:class>object.container</upnp:class></container><item id="2"><dc:title>Song</dc:title>` +
		`<upnp:artist>Band</upnp:artist><upnp:albumArtURI>javascript:alert(1)</upnp:albumArtURI>` +
		`<res protocolInfo="http-get:*:audio/mpeg:*">http://192.0.2.5/song.mp3</res></item></DIDL-Lite>`
	f.Add(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:BrowseResponse>` +
		`<Result>` + xmlEscape(didl) + `</Result><TotalMatches>2</TotalMatches>` +
		`</u:BrowseResponse></s:Body></s:Envelope>`)
	f.Add(`<?xml version="1.0" encoding="iso-8859-1"?><Result><![CDATA[` + didl + `]]></Result>`)
	f.Add(`<Result>&lt;DIDL-Lite</Result>`)
	f.Add(`<Result>` + strings.Repeat("&lt;a&gt;", 50) + `</Result>`)
	f.Add("")
	f.Fuzz(func(t *testing.T, data string) {
		items, err := parseBrowseResponse([]byte(data))
		if err != nil {
			return
		}

		for _, item := range items {
			for _, field := range []string{item.ID, item.Title, item.Class, item.Artist, item.Album} {
				if len(field) > fieldMax {
					t.Fatalf("field of %d bytes passed the %d cap", len(field), fieldMax)
				}

				if utf8.ValidString(data) && !utf8.ValidString(field) {
					t.Fatalf("clip split a character: %q", field)
				}
			}

			if len(item.URL) > urlMax || len(item.Art) > urlMax {
				t.Fatalf("URL of %d or art of %d bytes passed the cap", len(item.URL), len(item.Art))
			}

			if item.Art != "" && safeArt(item.Art) != item.Art {
				t.Fatalf("cover %q is not a plain http(s) URL", item.Art)
			}
		}
	})
}

// FuzzParseDesc is a media server's device description: whatever it says, the Browse goes
// to the host that served it, over http(s), and nowhere else.
func FuzzParseDesc(f *testing.F) {
	svc := func(ctrl string) string {
		return `<root><URLBase>http://192.0.2.5:8200/</URLBase><device><friendlyName>NAS</friendlyName><serviceList>` +
			`<service><serviceType>` + cdServiceURN + `</serviceType><controlURL>` + ctrl + `</controlURL></service>` +
			`</serviceList></device></root>`
	}
	for _, ctrl := range []string{
		"/ctl/ContentDir", "http://127.0.0.1/admin", "//192.0.2.6/x", "http://192.0.2.5:9000/cd", "file:///etc/passwd",
	} {
		f.Add("http://192.0.2.5:8200/desc.xml", svc(ctrl))
	}

	f.Add("http://[fe80::1%25eth0]:80/d.xml", svc("x"))
	f.Fuzz(func(t *testing.T, location, body string) {
		ctrl, name, err := parseDesc(location, []byte(body))
		if err != nil {
			return
		}

		c, perr := url.Parse(ctrl)
		if perr != nil || c.Hostname() != hostOf(location) || (c.Scheme != "http" && c.Scheme != "https") {
			t.Fatalf("control URL %q for a description from %q", ctrl, location)
		}

		if len(ctrl) > maxControlURL || len(name) > maxNameLen {
			t.Fatalf("control URL of %d or name of %d bytes passed the cap", len(ctrl), len(name))
		}
	})
}

// FuzzDidlMeta is the metadata a controller sends with SetAVTransportURI: its cover becomes
// an <img> in the web UI, so it must be a plain http(s) URL or nothing.
func FuzzDidlMeta(f *testing.F) {
	f.Add(`<DIDL-Lite><item><dc:title>Rock &amp;amp; Roll</dc:title><dc:creator>A</dc:creator>` +
		`<upnp:albumArtURI>http://192.0.2.5/a.jpg</upnp:albumArtURI>` +
		`<res duration="0:03:21.500">x</res></item></DIDL-Lite>`)
	f.Add(`<dc:title><![CDATA[<b>x</b>]]></dc:title><upnp:albumArtURI>data:image/png;base64,AAAA</upnp:albumArtURI>`)
	f.Add(`<res duration="99999999999:00:00">`)
	f.Fuzz(func(t *testing.T, didl string) {
		meta := didlMeta(didl)
		if meta.Art != "" && safeArt(meta.Art) != meta.Art {
			t.Fatalf("cover %q is not a plain http(s) URL", meta.Art)
		}

		if meta.Duration < 0 {
			t.Fatalf("negative duration %v from %q", meta.Duration, didl)
		}

		_ = meta.didl()
	})
}

// FuzzParseHMS is a Seek target or a duration: never negative.
func FuzzParseHMS(f *testing.F) {
	for _, s := range []string{
		"0:03:21", "1:00:00.5", "00:00:59.999", "-1:00:00", "1:60:00", "2562047:47:16", "9223372036:00:00",
		"1:2:NaN", "1:2:1e2",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		if d, ok := parseHMS(s); ok && d < 0 {
			t.Fatalf("parseHMS(%q) = %v", s, d)
		}
	})
}
