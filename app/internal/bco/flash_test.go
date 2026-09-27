// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Menus as the bench ST20 printed them (2026-08-07 log, 2026-09-23 probe).
const level1Menu = "\r\nBootloader Level 1  Menu (Jun 21 2011, 13:05:15):\r\n" +
	"================================================\r\n\r\n" +
	" 1) Download Bootloader Level 2\r\n 2) Show Meta Info\r\n 3) Reset\r\n" +
	" 4) Start Bootloader Level 2\r\n 5) Start Application\r\n 6) Erase Flash\r\n\r\n"

const level2Menu = "\r\nBootloader Level 2 Menu  (Jun 11 2013, 12:37:15)\r\n" +
	"================================================\r\n" +
	" 1) Download Image and Config (CnE)\r\n 2) Download Image Only\r\n 3) Download resource pages\r\n" +
	" 4) Start Image\r\n 5) Print Image Info\r\n 6) Mark Image as invalid\r\n" +
	" 7) Config (CnE) to factory default\r\n 8) Program MAC address 0\r\n 9) Program GUID\r\n" +
	" a) Program TVID\r\n b) Set Start Mode\r\n c) Exit to Shell\r\n" +
	" d) Download Image, Config and Resources (TFTP)\r\n e) Download Image Only (TFTP)\r\n" +
	" f) Download Bootloader (TFTP)\r\n g) Download Resources (TFTP)\r\n" +
	" h) Download Image and Resources (TFTP)\r\n i) Erase Application\r\n\r\n"

const flb = "\r\nFLB running @ 24000000Hz; Flash Drivers:  MX29GL640E detected.\r\nStarting Bootloader Level 1\r\n"

// fakeBCO plays the BCO's side of a flash.
type fakeBCO struct {
	mu       sync.Mutex
	out      bytes.Buffer
	on       bool
	state    string // off, level1, menu1, level2, upload, done
	dead     bool   // the flashed app refuses to start
	own      bool   // the flashed app is our own: it prints "netcard up", not the stock shell
	sent     []byte // every key the flasher typed outside Ctrl-A
	uploaded []byte
	xbuf     []byte // XMODEM packet being received
	xdata    []byte
	tftpFail bool // the TFTP upload fails, as on 2026-10-02
}

// xmodem takes one byte of an XMODEM-CRC transfer.
func (b *fakeBCO) xmodem(k byte) {
	if len(b.xbuf) == 0 && k == xEOT {
		// Level 2 programs the flash after the EOT; a power cut before it says so loses
		// the image (2026-10-14: "Application: invalid image").
		b.out.WriteByte(xACK)
		b.state = "programming"
		return
	}
	b.xbuf = append(b.xbuf, k)
	if len(b.xbuf) < 133 {
		return
	}
	p := b.xbuf
	b.xbuf = nil
	if p[0] != xSOH || p[1] != ^p[2] || p[1] != byte(len(b.xdata)/128+1) ||
		crc16XMODEM(p[3:131]) != uint16(p[131])<<8|uint16(p[132]) {
		b.out.WriteByte(xNAK)
		return
	}
	b.xdata = append(b.xdata, p[3:131]...)
	b.out.WriteByte(xACK)
}

func (b *fakeBCO) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == "programming" && b.out.Len() == 0 {
		b.out.WriteString("BCD decryption error = Data not encrypted\r\ndownload passed\r\n" + level2Menu)
		b.uploaded = b.xdata[:StockAppSize]
		b.state = "done"
	}
	return b.out.Read(p)
}

func (b *fakeBCO) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, k := range p {
		if b.state == "xmodem" {
			b.xmodem(k)
			continue
		}
		if k != 1 {
			b.sent = append(b.sent, k)
		}
		switch {
		case k == 1 && (b.state == "level1" || b.state == "menu1"):
			b.state = "menu1"
			b.out.WriteString(level1Menu)
		case k == '4' && b.state == "menu1":
			b.state = "level2"
			b.out.WriteString("Starting Bootloader Level 2\r\nDEVICE ID IS DM8XX : 0x70\r\n" +
				"string descriptor index = 4 string = APP=9423.47.9423.rev2859,<20141020>; BSL=47.9423\r\n" +
				"Application: CRC ok! Id=00000074\r\n" + level2Menu)
		case k == 'h' && b.state == "level2":
			b.state = "upload"
		case k == '2' && b.state == "level2":
			b.state = "xmodem"
			b.out.WriteString("2\r\nstart download with xmodem...\r\nCC")
		}
	}
	return len(p), nil
}

func (b *fakeBCO) power(on bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if on == b.on {
		return nil
	}
	b.on = on
	if !on {
		b.state = "off"
		return nil
	}
	b.out.WriteString(flb)
	if b.uploaded == nil {
		b.state = "level1"
		return nil
	}
	b.state = "app"
	b.out.WriteString("Starting Application\r\n")
	if b.dead {
		b.out.WriteString(flb + "Forced start of Bootloader Level 2\r\nStarting Bootloader Level 2\r\n" +
			"string descriptor index = 4 string = APP=9418.47.9418,<20141020>\r\n")
		return nil
	}
	if b.own {
		b.out.WriteString("wild id rf0 00000123\r\nnetcard wifi 00000000\r\nnetcard up\r\n")
		return nil
	}
	b.out.WriteString("DEVICE ID IS DM8XX : 0x70\r\nstring descriptor index = 4 string = " +
		"APP=9416.47.9416.rev3372(2014-10-06 11:10:49),<20141020>; BSL=47.9423.rev3372\r\n" +
		"sdsShell ready for commands. Try \"help\" for help.\r\nsds://>")
	return nil
}

func (b *fakeBCO) upload(ctx context.Context, image []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != "upload" {
		return errors.New("upload before the TFTP menu entry was chosen")
	}
	if b.tftpFail {
		return errors.New("TFTP: Wrong BCD version")
	}
	b.uploaded = image
	b.out.WriteString("TFTP: file transfer end. error code(0)\r\nFirmwareUpdate::processResourceData() Success\r\n" +
		"download with TFTP was successful\r\n" + level2Menu)
	return nil
}

func shellImage(t *testing.T) []byte {
	t.Helper()
	old := appImages
	img := bytes.Repeat([]byte{0x5a}, StockAppSize)
	appImages = map[string]string{sha256hex(img): "test image"}
	t.Cleanup(func() { appImages = old })
	return img
}

func TestFlashAppOwnAppBoots(t *testing.T) {
	img := shellImage(t)
	b := &fakeBCO{own: true}
	f := &Flasher{Serial: b, Power: b.power, Upload: b.upload, Scale: 0.01}
	if ver, err := f.FlashApp(context.Background(), img); err != nil || ver != "own app" {
		t.Fatalf("ver=%q err=%v", ver, err)
	}
}

func TestFlashAppFollowsStockSequence(t *testing.T) {
	img := shellImage(t)
	b := &fakeBCO{}
	f := &Flasher{Serial: b, Power: b.power, Upload: b.upload, Scale: 0.01}
	ver, err := f.FlashApp(context.Background(), img)
	if err != nil {
		t.Fatal(err)
	}
	if ver != "APP=9416.47.9416.rev3372(2014-10-06 11:10:49)" {
		t.Fatalf("version %q", ver)
	}
	if string(b.sent) != "4h" {
		t.Fatalf("typed %q, want exactly \"4h\"", b.sent)
	}
	if !bytes.Equal(b.uploaded, img) || !b.on {
		t.Fatalf("uploaded %d bytes, BCO on=%v", len(b.uploaded), b.on)
	}
}

func TestFlashAppReportsDeadApp(t *testing.T) {
	img := shellImage(t)
	b := &fakeBCO{dead: true}
	f := &Flasher{Serial: b, Power: b.power, Upload: b.upload, Scale: 0.01}
	if _, err := f.FlashApp(context.Background(), img); !errors.Is(err, ErrAppDead) {
		t.Fatalf("err = %v, want ErrAppDead", err)
	}
	if !b.on {
		t.Fatal("BCO left powered off")
	}
}

func TestFlashAppTFTPFailureIsErrUpload(t *testing.T) {
	img := shellImage(t)
	b := &fakeBCO{tftpFail: true}
	f := &Flasher{Serial: b, Power: b.power, Upload: b.upload, Scale: 0.01}
	if _, err := f.FlashApp(context.Background(), img); !errors.Is(err, ErrUpload) {
		t.Fatalf("err = %v, want ErrUpload", err)
	}
}

func TestXmodemAppWritesTheImage(t *testing.T) {
	img := shellImage(t)
	for i := range img {
		img[i] = byte(i * 7)
	}
	appImages = map[string]string{sha256hex(img): "test image"}
	b := &fakeBCO{}
	f := &Flasher{Serial: b, Power: b.power, Scale: 0.01}
	ver, err := f.XmodemApp(context.Background(), img)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.uploaded, img) || !b.on || ver == "" {
		t.Fatalf("uploaded %d bytes (equal %v), BCO on=%v, version %q",
			len(b.uploaded), bytes.Equal(b.uploaded, img), b.on, ver)
	}
	if string(b.sent) != "42" {
		t.Fatalf("typed %q before the transfer, want \"42\"", b.sent)
	}
}

func TestFlashAppRefusesUnknownImage(t *testing.T) {
	b := &fakeBCO{}
	f := &Flasher{Serial: b, Power: b.power, Upload: b.upload, Scale: 0.01}
	if _, err := f.FlashApp(context.Background(), make([]byte, StockAppSize)); err == nil {
		t.Fatal("accepted an image outside appImages")
	}
	if b.on || len(b.sent) != 0 {
		t.Fatal("touched the BCO for a refused image")
	}
}

func TestParseMenuKeyByExactLabel(t *testing.T) {
	for _, tc := range []struct{ menu, label, key string }{
		{level1Menu, "Start Bootloader Level 2", "4"},
		{level2Menu, "Download Image and Resources (TFTP)", "h"},
	} {
		got, err := parseMenuKey(tc.menu, tc.label)
		if err != nil || got != tc.key {
			t.Fatalf("%q: got %q, %v; want %q", tc.label, got, err, tc.key)
		}
	}
	if _, err := parseMenuKey(level1Menu, "Bootloader Level 2"); err == nil {
		t.Fatal("a partial label matched")
	}
}

func TestTFTPPutRetransmitsAndEndsWithEmptyBlock(t *testing.T) {
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	data := bytes.Repeat([]byte("0123456789abcdef"), 64) // 1024 bytes: two full blocks
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 600)
		n, client, err := srv.ReadFromUDP(buf)
		if err != nil || !strings.HasPrefix(string(buf[2:n]), "smsc_app.bcd\x00octet\x00") {
			got <- nil
			return
		}
		tid, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		defer tid.Close()
		ack := func(b uint16) { tid.WriteToUDP([]byte{0, 4, byte(b >> 8), byte(b)}, client) }
		ack(0)
		var file []byte
		dropped := false
		for {
			tid.SetReadDeadline(time.Now().Add(10 * time.Second))
			n, _, err := tid.ReadFromUDP(buf)
			if err != nil {
				got <- nil
				return
			}
			block := binary.BigEndian.Uint16(buf[2:])
			if block == 2 && !dropped { // lose block 2 once
				dropped = true
				continue
			}
			if int(block) == len(file)/512+1 {
				file = append(file, buf[4:n]...)
			}
			ack(block)
			if n-4 < 512 {
				got <- file
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := tftpPut(ctx, srv.LocalAddr().String(), "smsc_app.bcd", data); err != nil {
		t.Fatal(err)
	}
	if file := <-got; !bytes.Equal(file, data) {
		t.Fatalf("server got %d bytes, want %d", len(file), len(data))
	}
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// A flash that fails inside bootloader level 2 must not leave the BCO there: the menu
// has no network, so the ST20 stayed off the LAN until someone pulled the plug.
func TestFlashAppFailureCyclesBCOBackToItsApp(t *testing.T) {
	img := shellImage(t)
	b := &fakeBCO{}
	f := &Flasher{Serial: b, Power: b.power, Scale: 0.01,
		Upload: func(context.Context, []byte) error { return errors.New("no level-2 link") }}
	if _, err := f.FlashApp(context.Background(), img); err == nil {
		t.Fatal("upload failure was not reported")
	}
	if !b.on || b.state != "level1" {
		t.Fatalf("BCO on=%v state=%q, want powered and back through level 1", b.on, b.state)
	}
}

func TestAllowImageNeedsExactHash(t *testing.T) {
	old := appImages
	appImages = map[string]string{}
	t.Cleanup(func() { appImages = old })
	img := make([]byte, StockAppSize)
	img[0] = 1
	if AllowImage(img, strings.Repeat("0", 64)) == nil {
		t.Fatal("accepted a wrong hash")
	}
	if _, err := CheckAppImage(img); err == nil {
		t.Fatal("image allowed without approval")
	}
	if err := AllowImage(img, sha256hex(img)); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckAppImage(img); err != nil {
		t.Fatal(err)
	}
}

// Cancelling an upload stops it: no further firmware blocks go out, a pending ACK wait
// wakes at once, and the caller sees the cancellation.
func TestTFTPPutStopsWhenCancelled(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ackData bool // the server keeps acknowledging; otherwise block 1 is never ACKed
	}{{"between blocks", true}, {"waiting for an ACK", false}} {
		t.Run(tc.name, func(t *testing.T) {
			srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			blocks := make(chan uint16, 16)
			go func() {
				buf := make([]byte, 600)
				_, client, err := srv.ReadFromUDP(buf)
				if err != nil {
					return
				}
				tid, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
				defer tid.Close()
				ack := func(b uint16) { tid.WriteToUDP([]byte{0, 4, byte(b >> 8), byte(b)}, client) }
				ack(0)
				for {
					tid.SetReadDeadline(time.Now().Add(time.Second))
					_, _, err := tid.ReadFromUDP(buf)
					if err != nil {
						close(blocks)
						return
					}
					b := binary.BigEndian.Uint16(buf[2:])
					blocks <- b
					if b == 1 {
						cancel()
						time.Sleep(50 * time.Millisecond)
					}
					if tc.ackData {
						ack(b)
					}
				}
			}()
			start := time.Now()
			err = tftpPut(ctx, srv.LocalAddr().String(), "smsc_app.bcd", make([]byte, 4096))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("tftpPut = %v, want context.Canceled", err)
			}
			if d := time.Since(start); d > time.Second {
				t.Fatalf("tftpPut took %v after cancel", d)
			}
			srv.Close()
			for b := range blocks {
				if b > 1 {
					t.Fatalf("block %d sent after the upload was cancelled", b)
				}
			}
		})
	}
}

// noisySerial prints without pause, the way a BCO stuck in a reboot loop does.
type noisySerial struct{ reads int }

func (s *noisySerial) Read(b []byte) (int, error) {
	s.reads++
	for i := range b {
		b[i] = 'x'
	}
	return len(b), nil
}

func (s *noisySerial) Write(b []byte) (int, error) { return len(b), nil }

// A console that never goes quiet must not hold expect past its deadline or its
// cancellation, nor grow its buffer without bound.
func TestExpectOnANoisyConsoleKeepsItsBounds(t *testing.T) {
	serial := &noisySerial{}
	c := &console{f: &Flasher{Serial: serial}}

	start := time.Now()
	if _, err := c.expect(context.Background(), 50*time.Millisecond, "never printed"); err == nil {
		t.Fatal("expect found a marker that was never printed")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("expect returned after %v; its timeout was 50ms", d)
	}
	if len(c.buf) > consoleKeep {
		t.Fatalf("console kept %d bytes, limit %d", len(c.buf), consoleKeep)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	serial.reads = 0
	if _, err := c.expect(ctx, time.Minute, "never printed"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expect on a cancelled context = %v, want context.Canceled", err)
	}
	if serial.reads != 0 {
		t.Fatalf("a cancelled expect still read the UART %d times", serial.reads)
	}
}

// The boot-time AutoInstall approves an image while HTTP handlers check uploads: the
// allowlist must survive both at once. Run under -race.
func TestAllowImageAndCheckAppImageRunConcurrently(t *testing.T) {
	old := appImages
	appImages = map[string]string{}
	t.Cleanup(func() { appImages = old })
	img := []byte("synthetic BCO image")
	want := sha256hex(img)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 1000 {
			if err := AllowImage(img, want); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 1000 {
			_, _ = CheckAppImage(img)
		}
	}()
	wg.Wait()
	if _, err := CheckAppImage(img); err != nil {
		t.Fatal(err)
	}
}
