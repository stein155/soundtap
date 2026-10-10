// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The files the redactor learns the speaker's secrets from, besides the ones in its home
// and on /mnt/nv. Logical paths, read through sysPath.
const (
	// wpa_supplicant's configuration. The Wi-Fi manager hands it the networks over the
	// control socket and writes none here, but a supplicant started by hand may.
	wpaConfFile = "/tmp/wpa_supplicant.conf"
	// The one-shot press token (ADR 0055); soundtapd's confirmTokenPath.
	confirmTokenFile = "/run/soundtap/confirm-token" //nolint:gosec // a file path, not a credential
)

// sysRoot is where the absolute system paths the support bundle reads (/tmp, /run, /mnt/nv)
// start: "/" on a speaker, a fake tree in the redaction test.
var sysRoot = "/"

// sysPath maps a logical absolute path onto sysRoot.
func sysPath(p string) string { return filepath.Join(sysRoot, p) }

// secretLine matches a log line that carries a credential. A daemon or a hand-written
// configuration can put one in the log, and a support bundle is pasted into public issues, so the value is replaced rather than the line dropped —
// the user still sees that a password was set, just not which.
//
// The match runs to end of line rather than to the next space: "Authorization: Bearer
// <token>" would otherwise scrub only the word "Bearer" and ship the token.
//
// No \b in front: "_" is a word character, so sae_password= slipped through. A quote
// may close the key, as in JSON's "password":"x". wifi.conf's PSK_B64= and PSK_HEX= and
// librespot's auth_data carry the key encoded.
var secretLine = regexp.MustCompile(`(?i)(psk|password|passphrase|passwd|secret|token|authorization|api[_-]?key|auth[_-]?data)(_b64|_hex)?\b"?\s*[:=]\s*.*`)

// urlUserinfo matches the user:password@ part of a URL. The player logs whole stream
// URLs, and custom streams may carry credentials there. The user goes too: some
// services put the token in it.
var urlUserinfo = regexp.MustCompile(`(://)[^/\s@]+@`)

// ssidField matches a network name given as a key, as wpa_supplicant's STATUS ("ssid=...") and the
// bundle's own network section ("ssid       : ...") write it, and wpa_supplicant's
// "SSID 'name'". bssid is a MAC address, not a name, and \b keeps it out.
var (
	ssidField  = regexp.MustCompile(`(?i)(\bssid(?:_b64)?"?\s*[:=]\s*)(.*)`)
	ssidQuoted = regexp.MustCompile(`(?i)(\bssid\s+)('[^'\n]*'|"[^"\n]*")`)
)

// Addresses in text: dotted IPv4, and IPv6 in the global unicast block (2000::/3) so a
// "std::bad" or a clock reading is never taken for one. An IPv6 candidate starts where
// no hex digit or colon comes before it, so the tail of fd12:3456::1 is not one.
var (
	ipv4Text = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	ipv6Text = regexp.MustCompile(`(?i)(^|[^0-9a-f:])([23][0-9a-f]{0,3}:[0-9a-f:]*:[0-9a-f]*)`)
)

// keptRanges are addresses that say nothing about where the owner lives: shared address
// space (carrier NAT) and the documentation ranges, which the ST20's USB link uses
// (203.0.113.0/30).
var keptRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}

var globalIPv6 = netip.MustParsePrefix("2000::/3")

// redactSecrets rewrites credential assignments and URL userinfo to "<redacted>". It is
// the pattern half of redactor.scrub, which every part of the bundle goes through.
func redactSecrets(s string) string {
	s = urlUserinfo.ReplaceAllString(s, "${1}<redacted>@")
	return secretLine.ReplaceAllStringFunc(s, func(m string) string {
		i := strings.IndexAny(m, ":=")
		return m[:i+1] + " <redacted>"
	})
}

// secretLiteral is one value this speaker holds that must not leave it, and what the
// bundle says in its place.
type secretLiteral struct {
	value, label string
	// word replaces the value only where it stands alone, not inside a longer word. Names
	// (network names, account names) get it, so a network called "home" leaves "homeDir"
	// alone; keys and tokens do not, since a leaked key costs more than a mangled line.
	word bool
}

// minSecretLen is the shortest value replaced literally. A one- or two-character network
// name would otherwise take that letter out of the whole bundle and identify nobody.
// Keys are longer by rule: a WPA passphrase has at least eight characters.
const minSecretLen = 3

// redactor is the one filter between the speaker and a support bundle: the plain-text
// /api/debug and every file of the archive go through scrub, and nothing else writes
// to either. It knows the speaker's own secrets by value (read from the files that hold
// them when the bundle is asked for) and replaces them wherever they turn up, then
// applies the patterns that catch credentials it was never told about.
type redactor struct {
	literals []secretLiteral // longest first, so a key that contains a name goes whole
	match    *regexp.Regexp  // the literals as one alternation, or nil
}

// newRedactor collects the secrets the bundle must not carry: the Wi-Fi networks and keys
// (wifi.conf, its known list, the live wpa_supplicant.conf and the network the speaker
// is on), the MQTT broker password, the Spotify credentials librespot keeps, the press
// token and a household token older firmware left behind.
func (s *Server) newRedactor(ctx context.Context) *redactor {
	c := &secretCollector{seen: map[string]bool{}}
	nv := filepath.Dir(s.homeDir)
	for _, name := range []string{"wifi.conf", "wifi.conf.good"} {
		c.wifiConf(filepath.Join(nv, name))
	}
	c.wifiKnown(filepath.Join(nv, "wifi.conf.known"))
	c.wpaConf(sysPath(wpaConfFile))
	if s.backend != nil {
		if net, err := s.backend.NetworkInfo(ctx); err == nil && net != nil {
			c.ssid(net.SSID)
		}
	}
	if s.settings != nil {
		c.add(s.settings.Get().MQTT.Password, "<mqtt-password>", false)
	}
	for _, sub := range []string{"system", "cache"} {
		c.spotify(filepath.Join(nv, "spotify", sub, "credentials.json"))
	}
	c.file(sysPath(confirmTokenFile), "<confirm-token>")
	c.file(filepath.Join(s.homeDir, "api-token"), "<household-token>")
	return newRedactorFrom(c.out)
}

// newRedactorFrom builds the matcher over the collected values.
func newRedactorFrom(literals []secretLiteral) *redactor {
	sort.SliceStable(literals, func(i, j int) bool { return len(literals[i].value) > len(literals[j].value) })
	r := &redactor{literals: literals}
	if len(literals) > 0 {
		alts := make([]string, len(literals))
		for i, l := range literals {
			alts[i] = regexp.QuoteMeta(l.value)
		}
		r.match = regexp.MustCompile(strings.Join(alts, "|"))
	}
	return r
}

// scrub is the redaction every byte of a support bundle passes through: the known
// secrets by value, in one pass so a placeholder is never matched again; then credential
// assignments and URL userinfo; then network names given as a key; then public addresses.
// Private, loopback and link-local addresses stay: they say how the speaker sees its
// network, and not where the owner lives.
func (r *redactor) scrub(s string) string {
	if r.match != nil {
		s = r.replaceLiterals(s)
	}
	s = redactSecrets(s)
	s = ssidField.ReplaceAllStringFunc(s, func(m string) string {
		sub := ssidField.FindStringSubmatch(m)
		v := strings.TrimSpace(sub[2])
		if v == "" || isPlaceholder(v) {
			return m
		}
		return sub[1] + "<redacted>"
	})
	s = ssidQuoted.ReplaceAllString(s, "${1}'<redacted>'")
	s = ipv4Text.ReplaceAllStringFunc(s, publicIPText)
	return ipv6Text.ReplaceAllStringFunc(s, func(m string) string {
		sub := ipv6Text.FindStringSubmatch(m)
		return sub[1] + publicIPText(sub[2])
	})
}

func (r *redactor) replaceLiterals(s string) string {
	var b strings.Builder
	last := 0
	for _, loc := range r.match.FindAllStringIndex(s, -1) {
		v := s[loc[0]:loc[1]]
		lit := r.lookup(v)
		if lit.word && !standsAlone(s, loc[0], loc[1]) {
			continue
		}
		b.WriteString(s[last:loc[0]])
		b.WriteString(lit.label)
		last = loc[1]
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

func (r *redactor) lookup(v string) secretLiteral {
	for _, l := range r.literals {
		if l.value == v {
			return l
		}
	}
	return secretLiteral{value: v, label: "<redacted>"}
}

// standsAlone reports whether s[i:j] is not part of a longer word.
func standsAlone(s string, i, j int) bool {
	if i > 0 {
		if r, _ := utf8.DecodeLastRuneInString(s[:i]); isWordRune(r) {
			return false
		}
	}
	if j < len(s) {
		if r, _ := utf8.DecodeRuneInString(s[j:]); isWordRune(r) {
			return false
		}
	}
	return true
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func isPlaceholder(v string) bool {
	return strings.HasPrefix(v, "<") && strings.HasSuffix(v, ">")
}

// publicIPText replaces a public address with "<public-ip>" and leaves anything else.
func publicIPText(m string) string {
	a, err := netip.ParseAddr(m)
	if err != nil {
		return m
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || (a.Is6() && !globalIPv6.Contains(a)) {
		return m
	}
	for _, p := range keptRanges {
		if p.Contains(a) {
			return m
		}
	}
	return "<public-ip>"
}

// secretCollector gathers the literal secrets, each once, with the encodings they are
// stored in: wifi.conf keeps names and keys in base64, wpa_supplicant writes a name it
// cannot quote in hex.
type secretCollector struct {
	out   []secretLiteral
	seen  map[string]bool
	ssids int
}

func (c *secretCollector) add(v, label string, word bool) {
	for _, form := range []string{v, base64.StdEncoding.EncodeToString([]byte(v)), hex.EncodeToString([]byte(v))} {
		if len(form) < minSecretLen || c.seen[form] {
			continue
		}
		c.seen[form] = true
		c.out = append(c.out, secretLiteral{value: form, label: label, word: word})
	}
}

// ssid adds a network name under a numbered label, so the bundle still tells two
// networks apart without naming either.
func (c *secretCollector) ssid(name string) {
	if len(name) < minSecretLen || c.seen[name] {
		return
	}
	c.ssids++
	c.add(name, fmt.Sprintf("<wifi-ssid-%d>", c.ssids), true)
}

func (c *secretCollector) psk(key string) { c.add(key, "<wifi-key>", false) }

// file adds a whole small file (a token) as one secret.
func (c *secretCollector) file(path, label string) {
	if b, err := os.ReadFile(path); err == nil {
		c.add(strings.TrimSpace(string(b)), label, false)
	}
}

func decodeB64(v string) string {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
	if err != nil {
		return ""
	}
	return string(b)
}

// wifiConf reads wifi.conf's SSID_B64=, PSK_B64= and PSK_HEX= lines.
func (c *secretCollector) wifiConf(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		key, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || v == "" {
			continue
		}
		switch key {
		case "SSID_B64":
			c.ssid(decodeB64(v))
			c.add(v, "<wifi-ssid>", false)
		case "PSK_B64":
			c.psk(decodeB64(v))
			c.psk(v)
		case "PSK_HEX":
			c.psk(v)
		}
	}
}

// wifiKnown reads wifi.conf.known: "SSID_B64 PSK_B64|- PSK_HEX|-" per line.
func (c *secretCollector) wifiKnown(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		for i, v := range f {
			if v == "-" {
				continue
			}
			switch i {
			case 0:
				c.ssid(decodeB64(v))
				c.add(v, "<wifi-ssid>", false)
			case 1:
				c.psk(decodeB64(v))
				c.psk(v)
			case 2:
				c.psk(v)
			}
		}
	}
}

// wpaConf reads the network blocks of a wpa_supplicant configuration: ssid="name" or ssid=hex,
// psk="passphrase" or psk=hex, and the SAE and EAP secrets.
func (c *secretCollector) wpaConf(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		key, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || v == "" {
			continue
		}
		quoted := false
		if u, err := strconv.Unquote(v); err == nil {
			v, quoted = u, true
		}
		switch key {
		case "ssid":
			if !quoted {
				if raw, err := hex.DecodeString(v); err == nil {
					c.ssid(string(raw))
				}
			}
			c.ssid(v)
		case "psk", "sae_password", "password", "identity", "wep_key0":
			c.psk(v)
		}
	}
}

// spotify reads librespot's stored credentials: the account name and the reusable
// authentication blob.
func (c *secretCollector) spotify(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var cred struct {
		Username string `json:"username"`
		AuthData string `json:"auth_data"`
	}
	if json.Unmarshal(b, &cred) != nil {
		return
	}
	c.add(cred.Username, "<spotify-user>", true)
	c.add(cred.AuthData, "<spotify-credentials>", false)
}
