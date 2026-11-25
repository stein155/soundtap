// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

// The BCO's own wpa_supplicant, reached through the sdsShell's `wpa` command.
//
// On the ST20's BCO app 9423 the shell's help lists "wpa - wpa_supplicant cli"; there is
// no `wpa_cli` verb (the shell answers that with its help screen). `wpa STATUS` was read
// on hardware 2026-09-23 and returns the usual key=value block (wpa_state=COMPLETED).
// The site survey is a different verb, `sys wlan ctrl scan` (see scan.go).
//
// The control parser's vocabulary, read out of the image: STATUS, SCAN_RESULTS,
// LIST_NETWORKS, ADD_NETWORK, SET_NETWORK, SELECT_NETWORK, ENABLE_NETWORK, GET_NETWORK,
// SAVE_CONFIG, AP_SCAN, BSSID and friends.
//
// Unproven on hardware: the join sequence in Connect. Every call here therefore
// reports what it actually saw rather than inventing a success.

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// wpaVerb is the shell command that reaches the supplicant's control parser.
const wpaVerb = "wpa "

// Bounds for a single wpa exchange. The shell prints no end marker for these
// replies, so a call ends on silence rather than on a token.
const (
	wpaTimeout   = 20 * time.Second
	wpaQuiet     = 400 * time.Millisecond
	wpaMaxOutput = 64 << 10
	// wpaAssocTimeout bounds the association poll: scan, auth, DHCP.
	wpaAssocTimeout = 45 * time.Second
)

// wpaCLI runs one wpa_cli command and returns the shell's reply with its echo removed.
// The caller must already hold the serial lock and own fd.
func wpaCLI(ctx context.Context, fd int, command string) (string, error) {
	return shellCommand(ctx, fd, wpaVerb+command)
}

// shellCommand runs one sdsShell line and returns the reply with its echo removed. The
// caller must already hold the serial lock and own fd.
func shellCommand(ctx context.Context, fd int, line string) (string, error) {
	if strings.ContainsAny(line, "\r\n") {
		return "", errors.New("BCO shell: command must be a single line")
	}

	buf := make([]byte, 1024)
	drainSerial(fd, buf)

	if _, err := syscall.Write(fd, []byte(line+"\r")); err != nil {
		return "", fmt.Errorf("BCO serial write: %w", err)
	}

	var out strings.Builder

	deadline := time.Now().Add(wpaTimeout)

	var lastData time.Time

	for time.Now().Before(deadline) && out.Len() <= wpaMaxOutput {
		n, err := syscall.Read(fd, buf)
		switch {
		case n > 0:
			out.Write(buf[:n])

			lastData = time.Now()
		case err != nil && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK):
			return "", fmt.Errorf("BCO serial read: %w", err)
		}

		if !lastData.IsZero() && time.Since(lastData) >= wpaQuiet {
			break
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}

	if out.Len() == 0 {
		return "", fmt.Errorf("BCO shell %q: no reply", line)
	}

	return cleanShellOutput(out.String(), line), nil
}

// drainSerial reads away anything the shell left behind from an earlier command.
func drainSerial(fd int, buf []byte) {
	for range 16 {
		if n, err := syscall.Read(fd, buf); err != nil || n == 0 {
			return
		}
	}
}

// cleanShellOutput drops the sdsShell's backspace redraws and its echo of the line sent.
// The shell repaints the whole input line after every byte, so any line containing \b is
// echo, never payload.
func cleanShellOutput(response, sent string) string {
	var kept []string

	for raw := range strings.SplitSeq(strings.ReplaceAll(response, "\r", "\n"), "\n") {
		if strings.ContainsRune(raw, '\b') {
			continue
		}

		line := strings.TrimSpace(raw)
		if line == "" || strings.Contains(line, sent) {
			continue
		}

		kept = append(kept, line)
	}

	return strings.Join(kept, "\n")
}

// Status is what the BCO's supplicant reports about the current association.
type Status struct {
	State  string // wpa_state, e.g. COMPLETED / SCANNING / DISCONNECTED
	ID     string // id of the network block in use
	SSID   string
	BSSID  string
	Freq   string
	IP     string
	Linked bool
}

func parseStatus(reply string) Status {
	var s Status

	for line := range strings.SplitSeq(reply, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}

		switch strings.ToLower(strings.TrimSpace(key)) {
		case "id":
			s.ID = strings.TrimSpace(value)
		case "wpa_state":
			s.State = strings.TrimSpace(value)
		case "ssid":
			s.SSID = strings.TrimSpace(value)
		case "bssid":
			s.BSSID = strings.TrimSpace(value)
		case "freq":
			s.Freq = strings.TrimSpace(value)
		case "ip_address":
			s.IP = strings.TrimSpace(value)
		}
	}

	s.Linked = strings.EqualFold(s.State, "COMPLETED")

	return s
}

// ReadStatus asks the BCO supplicant for the live association state.
func ReadStatus(ctx context.Context, device string) (Status, error) {
	if err := lockSerial(ctx); err != nil {
		return Status{}, err
	}

	defer unlockSerial()

	fd, err := openSerial(ctx, device)
	if err != nil {
		return Status{}, err
	}
	defer syscall.Close(fd)

	reply, err := wpaCLI(ctx, fd, "STATUS")
	if err != nil {
		return Status{}, err
	}

	status := parseStatus(reply)
	if status.State == "" {
		return Status{}, errors.New("BCO wpa_cli STATUS: no wpa_state in reply")
	}

	return status, nil
}

// Connect joins an access point through the BCO's supplicant and waits for the
// association to complete.
//
// This is the one call here that changes state, and it is genuinely disruptive:
// SELECT_NETWORK disables the other configured networks, so a wrong passphrase drops the
// speaker off Wi-Fi until someone connects again. It therefore verifies rather than
// assumes — it polls STATUS and reports the state it actually reached.
//
// SAVE_CONFIG is intentionally last: nothing is persisted until the association is known
// to work, so a failed attempt leaves the stored profile alone.
func Connect(ctx context.Context, device, ssid, psk string) error {
	// Validate before taking the port: a bad argument must never reach the radio.
	if err := validSSID(ssid); err != nil {
		return err
	}

	if err := validPSK(psk); err != nil {
		return err
	}

	if err := lockSerial(ctx); err != nil {
		return err
	}

	defer unlockSerial()

	fd, err := openSerial(ctx, device)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)

	reply, err := wpaCLI(ctx, fd, "ADD_NETWORK")
	if err != nil {
		return err
	}

	id, err := networkID(reply)
	if err != nil {
		return err
	}

	// A failed attempt must not leave a half-configured entry behind; SAVE_CONFIG
	// would otherwise persist it on the next successful join.
	joined := false
	// ctx may already be spent here, so the cleanup gets its own short budget. SELECT_NETWORK
	// disabled every other block; enabling them again lets the old network come back.
	defer func() {
		if !joined {
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()

			wpaCLI(cctx, fd, fmt.Sprintf("REMOVE_NETWORK %d", id))
			wpaCLI(cctx, fd, "ENABLE_NETWORK all")
		}
	}()

	set := func(key, value string) error {
		out, err := wpaCLI(ctx, fd, fmt.Sprintf("SET_NETWORK %d %s %s", id, key, value))
		if err != nil {
			return err
		}

		if refused(out) {
			return fmt.Errorf("BCO wpa_cli SET_NETWORK %s: %s", key, firstLine(out))
		}

		return nil
	}
	// The sdsShell strips double quotes from arguments, so a quoted literal reaches the
	// supplicant bare and fails to parse ("failed to parse ssid", seen on 9423). Both
	// fields take an unquoted hex form instead: the SSID's bytes, and the 256-bit PSK
	// derived from the passphrase, which also keeps the passphrase off the serial line.
	if err := set("ssid", hex.EncodeToString([]byte(ssid))); err != nil {
		return err
	}

	if psk == "" {
		err = set("key_mgmt", "NONE")
	} else {
		err = set("psk", wpaPSK(psk, ssid))
	}

	if err != nil {
		return err
	}

	if _, err := wpaCLI(ctx, fd, fmt.Sprintf("SELECT_NETWORK %d", id)); err != nil {
		return err
	}

	deadline := time.Now().Add(wpaAssocTimeout)

	var last Status

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}

		out, err := wpaCLI(ctx, fd, "STATUS")
		if err != nil {
			continue // a single missed reply is not a failed association
		}

		last = parseStatus(out)
		// Same SSID is not enough: rejoining the network the speaker is already on
		// would read COMPLETED from the old block before the new one is tried.
		if last.Linked && last.ID == strconv.Itoa(id) {
			joined = true
			// Each join adds a block; drop older ones for this SSID so they don't pile up.
			if list, err := wpaCLI(ctx, fd, "LIST_NETWORKS"); err == nil {
				for _, old := range staleNetworkIDs(list, ssid, id) {
					wpaCLI(ctx, fd, fmt.Sprintf("REMOVE_NETWORK %d", old))
				}
			}

			out, err := wpaCLI(ctx, fd, "SAVE_CONFIG")
			if err == nil && refused(out) {
				err = errors.New(firstLine(out))
			}

			if err != nil {
				return fmt.Errorf("associated with %q but could not persist it: %w", ssid, err)
			}

			return nil
		}
	}

	state := last.State
	if state == "" {
		state = "no status reply"
	}

	return fmt.Errorf("BCO did not associate with %q (last state %s)", ssid, state)
}

// networkID reads the id ADD_NETWORK returns: the reply is the bare number.
func networkID(reply string) (int, error) {
	for line := range strings.SplitSeq(reply, "\n") {
		if id, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && id >= 0 {
			return id, nil
		}
	}

	return 0, fmt.Errorf("BCO wpa_cli ADD_NETWORK: no network id in %q", firstLine(reply))
}

func validSSID(ssid string) error {
	if ssid == "" || len(ssid) > 32 {
		return errors.New("ssid must be 1..32 bytes")
	}

	if strings.ContainsAny(ssid, "\"\\\r\n") || strings.IndexFunc(ssid, func(r rune) bool { return r < 0x20 }) >= 0 {
		return errors.New("ssid must not contain quotes, backslashes or control characters")
	}

	return nil
}

// IsHexPSK says psk is a raw WPA key rather than a passphrase: 64 hex digits, which no
// passphrase can be since those stop at 63 bytes.
func IsHexPSK(psk string) bool {
	return len(psk) == 64 && strings.IndexFunc(psk, func(r rune) bool {
		return !strings.ContainsRune("0123456789abcdefABCDEF", r)
	}) < 0
}

func validPSK(psk string) error {
	if psk == "" {
		return nil // open network
	}

	if IsHexPSK(psk) {
		return nil
	}

	if len(psk) < 8 || len(psk) > 63 {
		return errors.New("WPA passphrase must be 8..63 bytes")
	}

	if strings.ContainsAny(psk, "\"\\\r\n") || strings.IndexFunc(psk, func(r rune) bool { return r < 0x20 }) >= 0 {
		return errors.New("passphrase must not contain quotes, backslashes or control characters")
	}

	return nil
}

// staleNetworkIDs picks the LIST_NETWORKS rows ("id\tssid\tbssid\tflags") for ssid
// other than keep.
func staleNetworkIDs(list, ssid string, keep int) []int {
	var ids []int

	for line := range strings.SplitSeq(list, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || fields[1] != ssid {
			continue
		}

		if id, err := strconv.Atoi(strings.TrimSpace(fields[0])); err == nil && id != keep {
			ids = append(ids, id)
		}
	}

	return ids
}

// wpaPSK is the WPA pre-shared key for a passphrase: PBKDF2-HMAC-SHA1 with the SSID
// as salt, 4096 rounds, 32 bytes (IEEE 802.11i H.4), as 64 hex digits.
func wpaPSK(passphrase, ssid string) string {
	if IsHexPSK(passphrase) {
		return passphrase
	}

	var key []byte
	for block := uint32(1); len(key) < 32; block++ {
		mac := hmac.New(sha1.New, []byte(passphrase))
		mac.Write([]byte(ssid))
		mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)

		for range 4095 {
			mac.Reset()
			mac.Write(u)

			u = mac.Sum(u[:0])
			for i := range t {
				t[i] ^= u[i]
			}
		}

		key = append(key, t...)
	}

	return hex.EncodeToString(key[:32])
}

// firstLine trims a reply to something safe to put in an error message.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 120 {
		return line[:120]
	}

	return line
}

// refused reports a wpa_cli reply that turned the command down.
func refused(out string) bool { return strings.Contains(strings.ToUpper(out), "FAIL") }
