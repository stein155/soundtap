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
	"crypto/sha1" //nolint:gosec // G505: WPA's PSK is PBKDF2-HMAC-SHA1 by definition (IEEE 802.11i)
	"encoding/binary"
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
	// wpaCleanupTimeout bounds removing a block whose join failed.
	wpaCleanupTimeout = 5 * time.Second
)

// The shell's read size and poll interval, shared with the site survey.
const (
	serialChunk = 1024
	serialPoll  = 20 * time.Millisecond
)

// The supplicant exchanges' errors; each keeps the text it had before it was a sentinel.
var (
	errShellMultiline   = errors.New("BCO shell: command must be a single line")
	errShellNoReply     = errors.New("no reply")
	errNoWPAState       = errors.New("BCO wpa_cli STATUS: no wpa_state in reply")
	errDidNotAssociate  = errors.New("BCO did not associate")
	errNoNetworkID      = errors.New("no network id in")
	errSSIDLength       = errors.New("ssid must be 1..32 bytes")
	errSSIDCharacters   = errors.New("ssid must not contain quotes, backslashes or control characters")
	errPassphraseLength = errors.New("WPA passphrase must be 8..63 bytes")
	errPassphraseChars  = errors.New("passphrase must not contain quotes, backslashes or control characters")
)

// refusalError is the first line of a reply that turned a command down, as the error.
type refusalError string

func (r refusalError) Error() string { return string(r) }

// wpaCLI runs one wpa_cli command and returns the shell's reply with its echo removed.
// The caller must already hold the serial lock and own fd.
func wpaCLI(ctx context.Context, fd int, command string) (string, error) {
	return shellCommand(ctx, fd, wpaVerb+command)
}

// shellCommand runs one sdsShell line and returns the reply with its echo removed. The
// caller must already hold the serial lock and own serialFD.
func shellCommand(ctx context.Context, serialFD int, line string) (string, error) {
	if strings.ContainsAny(line, "\r\n") {
		return "", errShellMultiline
	}

	buf := make([]byte, serialChunk)
	drainSerial(serialFD, buf)

	_, err := syscall.Write(serialFD, []byte(line+"\r"))
	if err != nil {
		return "", fmt.Errorf("BCO serial write: %w", err)
	}

	var out strings.Builder

	deadline := time.Now().Add(wpaTimeout)

	var lastData time.Time

	for time.Now().Before(deadline) && out.Len() <= wpaMaxOutput {
		count, err := syscall.Read(serialFD, buf)
		switch {
		case count > 0:
			out.Write(buf[:count])

			lastData = time.Now()
		case err != nil && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK):
			return "", fmt.Errorf("BCO serial read: %w", err)
		default:
			// Nothing yet: wait below.
		}

		if !lastData.IsZero() && time.Since(lastData) >= wpaQuiet {
			break
		}

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("BCO shell %q: %w", line, ctx.Err())
		case <-time.After(serialPoll):
		}
	}

	if out.Len() == 0 {
		return "", fmt.Errorf("BCO shell %q: %w", line, errShellNoReply)
	}

	return cleanShellOutput(out.String(), line), nil
}

// drainSerial reads away anything the shell left behind from an earlier command.
func drainSerial(fd int, buf []byte) {
	for range 16 {
		count, err := syscall.Read(fd, buf)
		if err != nil || count == 0 {
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
	var status Status

	for line := range strings.SplitSeq(reply, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}

		switch strings.ToLower(strings.TrimSpace(key)) {
		case "id":
			status.ID = strings.TrimSpace(value)
		case "wpa_state":
			status.State = strings.TrimSpace(value)
		case "ssid":
			status.SSID = strings.TrimSpace(value)
		case "bssid":
			status.BSSID = strings.TrimSpace(value)
		case "freq":
			status.Freq = strings.TrimSpace(value)
		case "ip_address":
			status.IP = strings.TrimSpace(value)
		default:
			// The supplicant's other keys are not used.
		}
	}

	status.Linked = strings.EqualFold(status.State, "COMPLETED")

	return status
}

// ReadStatus asks the BCO supplicant for the live association state.
func ReadStatus(ctx context.Context, device string) (Status, error) {
	err := lockSerial(ctx)
	if err != nil {
		return Status{}, err
	}

	defer unlockSerial()

	serialFD, err := openSerial(ctx, device)
	if err != nil {
		return Status{}, err
	}
	defer syscall.Close(serialFD)

	reply, err := wpaCLI(ctx, serialFD, "STATUS")
	if err != nil {
		return Status{}, err
	}

	status := parseStatus(reply)
	if status.State == "" {
		return Status{}, errNoWPAState
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
	err := validSSID(ssid)
	if err != nil {
		return err
	}

	err = validPSK(psk)
	if err != nil {
		return err
	}

	err = lockSerial(ctx)
	if err != nil {
		return err
	}

	defer unlockSerial()

	serialFD, err := openSerial(ctx, device)
	if err != nil {
		return err
	}
	defer syscall.Close(serialFD)

	return joinNetwork(ctx, serialFD, ssid, psk)
}

// joinNetwork is Connect on the open UART: a new network block, selected, associated and
// saved, or removed again.
func joinNetwork(ctx context.Context, serialFD int, ssid, psk string) error {
	reply, err := wpaCLI(ctx, serialFD, "ADD_NETWORK")
	if err != nil {
		return err
	}

	block, err := networkID(reply)
	if err != nil {
		return err
	}

	// A failed attempt must not leave a half-configured entry behind; SAVE_CONFIG
	// would otherwise persist it on the next successful join.
	joined := false
	defer func() {
		if !joined {
			dropNetwork(ctx, serialFD, block)
		}
	}()

	err = configureNetwork(ctx, serialFD, block, ssid, psk)
	if err != nil {
		return err
	}

	_, err = wpaCLI(ctx, serialFD, fmt.Sprintf("SELECT_NETWORK %d", block))
	if err != nil {
		return err
	}

	last, err := awaitAssociation(ctx, serialFD, block)
	if err != nil {
		return err
	}

	if !last.Linked || last.ID != strconv.Itoa(block) {
		state := last.State
		if state == "" {
			state = "no status reply"
		}

		return fmt.Errorf("%w with %q (last state %s)", errDidNotAssociate, ssid, state)
	}

	joined = true

	return persistNetwork(ctx, serialFD, ssid, block)
}

// dropNetwork removes a block whose join failed. ctx may already be spent here, so the
// cleanup gets its own short budget. SELECT_NETWORK disabled every other block; enabling
// them again lets the old network come back. Both are best effort.
func dropNetwork(ctx context.Context, serialFD, block int) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), wpaCleanupTimeout)
	defer cancel()

	_, _ = wpaCLI(cctx, serialFD, fmt.Sprintf("REMOVE_NETWORK %d", block))
	_, _ = wpaCLI(cctx, serialFD, "ENABLE_NETWORK all")
}

// configureNetwork sets block's SSID and its key. The sdsShell strips double quotes from
// arguments, so a quoted literal reaches the supplicant bare and fails to parse ("failed
// to parse ssid", seen on 9423). Both fields take an unquoted hex form instead: the SSID's
// bytes, and the 256-bit PSK derived from the passphrase, which also keeps the passphrase
// off the serial line.
func configureNetwork(ctx context.Context, serialFD, block int, ssid, psk string) error {
	set := func(key, value string) error {
		out, err := wpaCLI(ctx, serialFD, fmt.Sprintf("SET_NETWORK %d %s %s", block, key, value))
		if err != nil {
			return err
		}

		if refused(out) {
			return fmt.Errorf("BCO wpa_cli SET_NETWORK %s: %w", key, refusalError(firstLine(out)))
		}

		return nil
	}

	err := set("ssid", hex.EncodeToString([]byte(ssid)))
	if err != nil {
		return err
	}

	if psk == "" {
		return set("key_mgmt", "NONE")
	}

	return set("psk", wpaPSK(psk, ssid))
}

// awaitAssociation polls STATUS until block is the associated one or wpaAssocTimeout
// passes, and returns the last status read. Same SSID is not enough: rejoining the network
// the speaker is already on would read COMPLETED from the old block before the new one is
// tried.
func awaitAssociation(ctx context.Context, serialFD, block int) (Status, error) {
	deadline := time.Now().Add(wpaAssocTimeout)

	var last Status

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("BCO association: %w", ctx.Err())
		case <-time.After(time.Second):
		}

		out, err := wpaCLI(ctx, serialFD, "STATUS")
		if err != nil {
			continue // a single missed reply is not a failed association
		}

		last = parseStatus(out)
		if last.Linked && last.ID == strconv.Itoa(block) {
			return last, nil
		}
	}

	return last, nil
}

// persistNetwork saves a join that worked. Each join adds a block; older ones for this
// SSID are dropped first so they don't pile up.
func persistNetwork(ctx context.Context, serialFD int, ssid string, block int) error {
	list, err := wpaCLI(ctx, serialFD, "LIST_NETWORKS")
	if err == nil {
		for _, old := range staleNetworkIDs(list, ssid, block) {
			_, _ = wpaCLI(ctx, serialFD, fmt.Sprintf("REMOVE_NETWORK %d", old))
		}
	}

	out, err := wpaCLI(ctx, serialFD, "SAVE_CONFIG")
	if err == nil && refused(out) {
		err = refusalError(firstLine(out))
	}

	if err != nil {
		return fmt.Errorf("associated with %q but could not persist it: %w", ssid, err)
	}

	return nil
}

// networkID reads the id ADD_NETWORK returns: the reply is the bare number.
func networkID(reply string) (int, error) {
	for line := range strings.SplitSeq(reply, "\n") {
		block, err := strconv.Atoi(strings.TrimSpace(line))
		if err == nil && block >= 0 {
			return block, nil
		}
	}

	return 0, fmt.Errorf("BCO wpa_cli ADD_NETWORK: %w %q", errNoNetworkID, firstLine(reply))
}

// The bounds of an SSID and a WPA passphrase (IEEE 802.11), and the first character that
// is not a control character.
const (
	maxSSIDLen       = 32
	minPassphraseLen = 8
	maxPassphraseLen = 63
	hexPSKLen        = 64
	firstPrintable   = 0x20
)

func validSSID(ssid string) error {
	if ssid == "" || len(ssid) > maxSSIDLen {
		return errSSIDLength
	}

	if strings.ContainsAny(ssid, "\"\\\r\n") || strings.IndexFunc(ssid, isControl) >= 0 {
		return errSSIDCharacters
	}

	return nil
}

// isControl is a byte below space: what the shell and the supplicant cannot take.
func isControl(r rune) bool { return r < firstPrintable }

// IsHexPSK says psk is a raw WPA key rather than a passphrase: 64 hex digits, which no
// passphrase can be since those stop at 63 bytes.
func IsHexPSK(psk string) bool {
	return len(psk) == hexPSKLen && strings.IndexFunc(psk, func(r rune) bool {
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

	if len(psk) < minPassphraseLen || len(psk) > maxPassphraseLen {
		return errPassphraseLength
	}

	if strings.ContainsAny(psk, "\"\\\r\n") || strings.IndexFunc(psk, isControl) >= 0 {
		return errPassphraseChars
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

		block, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err == nil && block != keep {
			ids = append(ids, block)
		}
	}

	return ids
}

// PBKDF2 for the WPA PSK (IEEE 802.11i H.4): 4096 rounds, 32 bytes of key.
const (
	pskRounds = 4096
	pskLen    = 32
)

// wpaPSK is the WPA pre-shared key for a passphrase: PBKDF2-HMAC-SHA1 with the SSID
// as salt, 4096 rounds, 32 bytes (IEEE 802.11i H.4), as 64 hex digits.
func wpaPSK(passphrase, ssid string) string {
	if IsHexPSK(passphrase) {
		return passphrase
	}

	var key []byte
	for block := uint32(1); len(key) < pskLen; block++ {
		mac := hmac.New(sha1.New, []byte(passphrase))
		mac.Write([]byte(ssid))
		mac.Write(binary.BigEndian.AppendUint32(nil, block))
		round := mac.Sum(nil)
		sum := append([]byte(nil), round...)

		for range pskRounds - 1 {
			mac.Reset()
			mac.Write(round)

			round = mac.Sum(round[:0])
			for i := range sum {
				sum[i] ^= round[i]
			}
		}

		key = append(key, sum...)
	}

	return hex.EncodeToString(key[:pskLen])
}

// errorLineMax bounds a reply quoted in an error message.
const errorLineMax = 120

// firstLine trims a reply to something safe to put in an error message.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > errorLineMax {
		return line[:errorLineMax]
	}

	return line
}

// refused reports a wpa_cli reply that turned the command down.
func refused(out string) bool { return strings.Contains(strings.ToUpper(out), "FAIL") }
