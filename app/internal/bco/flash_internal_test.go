// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
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

// The fakes' failures.
var (
	errUploadTooEarly = errors.New("upload before the TFTP menu entry was chosen")
	errWrongBCD       = errors.New("TFTP: Wrong BCD version")
	errNoLevel2Link   = errors.New("no level-2 link")
)

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

// newFakeBCO is a BCO that is off, with nothing flashed.
func newFakeBCO() *fakeBCO {
	return &fakeBCO{
		mu: sync.Mutex{}, out: bytes.Buffer{}, on: false, state: "", dead: false, own: false,
		sent: nil, uploaded: nil, xbuf: nil, xdata: nil, tftpFail: false,
	}
}

func (b *fakeBCO) Read(buf []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == "programming" && b.out.Len() == 0 {
		b.out.WriteString("BCD decryption error = Data not encrypted\r\ndownload passed\r\n" + level2Menu)
		b.uploaded = b.xdata[:StockAppSize]
		b.state = "done"
	}

	count, err := b.out.Read(buf)
	if err != nil {
		return count, fmt.Errorf("fake BCO: %w", err)
	}

	return count, nil
}

func (b *fakeBCO) Write(keys []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, key := range keys {
		if b.state == "xmodem" {
			b.xmodem(key)

			continue
		}

		if key != 1 {
			b.sent = append(b.sent, key)
		}

		switch {
		case key == 1 && (b.state == "level1" || b.state == "menu1"):
			b.state = "menu1"
			b.out.WriteString(level1Menu)
		case key == '4' && b.state == "menu1":
			b.state = "level2"
			b.out.WriteString("Starting Bootloader Level 2\r\nDEVICE ID IS DM8XX : 0x70\r\n" +
				"string descriptor index = 4 string = APP=9423.47.9423.rev2859,<20141020>; BSL=47.9423\r\n" +
				"Application: CRC ok! Id=00000074\r\n" + level2Menu)
		case key == 'h' && b.state == "level2":
			b.state = "upload"
		case key == '2' && b.state == "level2":
			b.state = "xmodem"
			b.out.WriteString("2\r\nstart download with xmodem...\r\nCC")
		default:
			// Anything else is ignored, as the bootloader menus do.
		}
	}

	return len(keys), nil
}

// flasher is a Flasher on b, with every wait a hundredth of the real one.
func (b *fakeBCO) flasher() *Flasher {
	return &Flasher{Serial: b, Power: b.supply(), Upload: b.upload, Log: nil, Scale: 0.01}
}

// xmodem takes one byte of an XMODEM-CRC transfer.
func (b *fakeBCO) xmodem(key byte) {
	if len(b.xbuf) == 0 && key == xEOT {
		// Level 2 programs the flash after the EOT; a power cut before it says so loses
		// the image (2026-10-14: "Application: invalid image").
		b.out.WriteByte(xACK)
		b.state = "programming"

		return
	}

	b.xbuf = append(b.xbuf, key)
	if len(b.xbuf) < xmodemPacket {
		return
	}

	pkt := b.xbuf

	b.xbuf = nil
	if pkt[0] != xSOH || pkt[1] != ^pkt[2] || pkt[1] != byte(len(b.xdata)/128+1) ||
		crc16XMODEM(pkt[3:131]) != uint16(pkt[131])<<8|uint16(pkt[132]) {
		b.out.WriteByte(xNAK)

		return
	}

	b.xdata = append(b.xdata, pkt[3:131]...)
	b.out.WriteByte(xACK)
}

// supply is the fake's supply switch, as Flasher.Power.
func (b *fakeBCO) supply() func(on bool) error {
	return func(powered bool) error {
		b.mu.Lock()
		defer b.mu.Unlock()

		if powered == b.on {
			return nil
		}

		b.on = powered
		if !powered {
			b.state = "off"

			return nil
		}

		b.boot()

		return nil
	}
}

// boot prints what the BCO prints at power-on; the caller holds mu.
func (b *fakeBCO) boot() {
	b.out.WriteString(flb)

	if b.uploaded == nil {
		b.state = "level1"

		return
	}

	b.state = "app"
	b.out.WriteString("Starting Application\r\n")

	if b.dead {
		b.out.WriteString(flb + "Forced start of Bootloader Level 2\r\nStarting Bootloader Level 2\r\n" +
			"string descriptor index = 4 string = APP=9418.47.9418,<20141020>\r\n")

		return
	}

	if b.own {
		b.out.WriteString("wild id rf0 00000123\r\nnetcard wifi 00000000\r\nnetcard up\r\n")

		return
	}

	b.out.WriteString("DEVICE ID IS DM8XX : 0x70\r\nstring descriptor index = 4 string = " +
		"APP=9416.47.9416.rev3372(2014-10-06 11:10:49),<20141020>; BSL=47.9423.rev3372\r\n" +
		"sdsShell ready for commands. Try \"help\" for help.\r\nsds://>")
}

func (b *fakeBCO) upload(_ context.Context, image []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state != "upload" {
		return errUploadTooEarly
	}

	if b.tftpFail {
		return errWrongBCD
	}

	b.uploaded = image
	b.out.WriteString("TFTP: file transfer end. error code(0)\r\nFirmwareUpdate::processResourceData() Success\r\n" +
		"download with TFTP was successful\r\n" + level2Menu)

	return nil
}

// allowedImage is an image of the stock app's size, added to the process's allowlist.
func allowedImage(t *testing.T) []byte {
	t.Helper()

	img := bytes.Repeat([]byte{0x5a}, StockAppSize)

	err := AllowImage(img, sha256hex(img))
	if err != nil {
		t.Fatal(err)
	}

	return img
}

func TestFlashAppOwnAppBoots(t *testing.T) {
	t.Parallel()

	img := allowedImage(t)
	fake := newFakeBCO()
	fake.own = true

	ver, err := fake.flasher().FlashApp(context.Background(), img)
	if err != nil || ver != "own app" {
		t.Fatalf("ver=%q err=%v", ver, err)
	}
}

func TestFlashAppFollowsStockSequence(t *testing.T) {
	t.Parallel()

	img := allowedImage(t)
	fake := newFakeBCO()

	ver, err := fake.flasher().FlashApp(context.Background(), img)
	if err != nil {
		t.Fatal(err)
	}

	if ver != "APP=9416.47.9416.rev3372(2014-10-06 11:10:49)" {
		t.Fatalf("version %q", ver)
	}

	if string(fake.sent) != "4h" {
		t.Fatalf("typed %q, want exactly \"4h\"", fake.sent)
	}

	if !bytes.Equal(fake.uploaded, img) || !fake.on {
		t.Fatalf("uploaded %d bytes, BCO on=%v", len(fake.uploaded), fake.on)
	}
}

func TestFlashAppReportsDeadApp(t *testing.T) {
	t.Parallel()

	img := allowedImage(t)
	fake := newFakeBCO()
	fake.dead = true

	_, err := fake.flasher().FlashApp(context.Background(), img)
	if !errors.Is(err, ErrAppDead) {
		t.Fatalf("err = %v, want ErrAppDead", err)
	}

	if !fake.on {
		t.Fatal("BCO left powered off")
	}
}

func TestFlashAppTFTPFailureIsErrUpload(t *testing.T) {
	t.Parallel()

	img := allowedImage(t)
	fake := newFakeBCO()
	fake.tftpFail = true

	_, err := fake.flasher().FlashApp(context.Background(), img)
	if !errors.Is(err, ErrUpload) {
		t.Fatalf("err = %v, want ErrUpload", err)
	}
}

func TestXmodemAppWritesTheImage(t *testing.T) {
	t.Parallel()

	img := make([]byte, StockAppSize)
	for i := range img {
		img[i] = byte(i * 7)
	}

	err := AllowImage(img, sha256hex(img))
	if err != nil {
		t.Fatal(err)
	}

	fake := newFakeBCO()
	flasher := fake.flasher()
	flasher.Upload = nil

	ver, err := flasher.XmodemApp(context.Background(), img)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(fake.uploaded, img) || !fake.on || ver == "" {
		t.Fatalf("uploaded %d bytes (equal %v), BCO on=%v, version %q",
			len(fake.uploaded), bytes.Equal(fake.uploaded, img), fake.on, ver)
	}

	if string(fake.sent) != "42" {
		t.Fatalf("typed %q before the transfer, want \"42\"", fake.sent)
	}
}

func TestFlashAppRefusesUnknownImage(t *testing.T) {
	t.Parallel()

	fake := newFakeBCO()

	_, err := fake.flasher().FlashApp(context.Background(), make([]byte, StockAppSize))
	if err == nil {
		t.Fatal("accepted an image outside appImages")
	}

	if fake.on || len(fake.sent) != 0 {
		t.Fatal("touched the BCO for a refused image")
	}
}

func TestParseMenuKeyByExactLabel(t *testing.T) {
	t.Parallel()

	for _, menu := range []struct{ text, label, key string }{
		{level1Menu, "Start Bootloader Level 2", "4"},
		{level2Menu, "Download Image and Resources (TFTP)", "h"},
	} {
		got, err := parseMenuKey(menu.text, menu.label)
		if err != nil || got != menu.key {
			t.Fatalf("%q: got %q, %v; want %q", menu.label, got, err, menu.key)
		}
	}

	_, err := parseMenuKey(level1Menu, "Bootloader Level 2")
	if err == nil {
		t.Fatal("a partial label matched")
	}
}

// ackTo sends the ACK of block from conn to client; a lost ACK shows as the test's failure.
func ackTo(conn *net.UDPConn, client *net.UDPAddr, block uint16) {
	_, _ = conn.WriteToUDP([]byte{0, 4, byte(block >> 8), byte(block)}, client)
}

func TestTFTPPutRetransmitsAndEndsWithEmptyBlock(t *testing.T) {
	t.Parallel()

	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0, Zone: ""})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	data := bytes.Repeat([]byte("0123456789abcdef"), 64) // 1024 bytes: two full blocks

	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 600)

		count, client, rerr := srv.ReadFromUDP(buf)
		if rerr != nil || !strings.HasPrefix(string(buf[2:count]), "smsc_app.bcd\x00octet\x00") {
			got <- nil

			return
		}

		tid, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0, Zone: ""})
		defer tid.Close()

		ackTo(tid, client, 0)

		var file []byte

		dropped := false

		for {
			_ = tid.SetReadDeadline(time.Now().Add(10 * time.Second))

			count, _, rerr := tid.ReadFromUDP(buf)
			if rerr != nil {
				got <- nil

				return
			}

			block := binary.BigEndian.Uint16(buf[2:])
			if block == 2 && !dropped { // lose block 2 once
				dropped = true

				continue
			}

			if int(block) == len(file)/512+1 {
				file = append(file, buf[4:count]...)
			}

			ackTo(tid, client, block)

			if count-4 < 512 {
				got <- file

				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	err = tftpPut(ctx, srv.LocalAddr().String(), "smsc_app.bcd", data)
	if err != nil {
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
	t.Parallel()

	img := allowedImage(t)
	fake := newFakeBCO()
	flasher := fake.flasher()
	flasher.Upload = func(context.Context, []byte) error { return errNoLevel2Link }

	_, err := flasher.FlashApp(context.Background(), img)
	if err == nil {
		t.Fatal("upload failure was not reported")
	}

	if !fake.on || fake.state != "level1" {
		t.Fatalf("BCO on=%v state=%q, want powered and back through level 1", fake.on, fake.state)
	}
}

func TestAllowImageNeedsExactHash(t *testing.T) {
	t.Parallel()

	// An image no flash has approved yet: the allowlist lasts as long as the process,
	// over every -count run.
	img := make([]byte, StockAppSize)
	rand.Read(img[:32])

	if AllowImage(img, strings.Repeat("0", 64)) == nil {
		t.Fatal("accepted a wrong hash")
	}

	_, err := CheckAppImage(img)
	if err == nil {
		t.Fatal("image allowed without approval")
	}

	err = AllowImage(img, sha256hex(img))
	if err != nil {
		t.Fatal(err)
	}

	_, err = CheckAppImage(img)
	if err != nil {
		t.Fatal(err)
	}
}

// Cancelling an upload stops it: no further firmware blocks go out, a pending ACK wait
// wakes at once, and the caller sees the cancellation.
func TestTFTPPutStopsWhenCancelled(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		ackData bool // the server keeps acknowledging; otherwise block 1 is never ACKed
	}{{"between blocks", true}, {"waiting for an ACK", false}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0, Zone: ""})
			if err != nil {
				t.Fatal(err)
			}
			defer srv.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			blocks := make(chan uint16, 16)
			go func() {
				buf := make([]byte, 600)

				_, client, rerr := srv.ReadFromUDP(buf)
				if rerr != nil {
					return
				}

				tid, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0, Zone: ""})
				defer tid.Close()

				ackTo(tid, client, 0)

				for {
					_ = tid.SetReadDeadline(time.Now().Add(time.Second))

					_, _, rerr := tid.ReadFromUDP(buf)
					if rerr != nil {
						close(blocks)

						return
					}

					block := binary.BigEndian.Uint16(buf[2:])
					blocks <- block

					if block == 1 {
						cancel()
						time.Sleep(50 * time.Millisecond)
					}

					if test.ackData {
						ackTo(tid, client, block)
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

			for block := range blocks {
				if block > 1 {
					t.Fatalf("block %d sent after the upload was cancelled", block)
				}
			}
		})
	}
}

// noisySerial prints without pause, the way a BCO stuck in a reboot loop does.
type noisySerial struct{ reads int }

func (s *noisySerial) Read(buf []byte) (int, error) {
	s.reads++

	for i := range buf {
		buf[i] = 'x'
	}

	return len(buf), nil
}

func (*noisySerial) Write(keys []byte) (int, error) { return len(keys), nil }

// A console that never goes quiet must not hold expect past its deadline or its
// cancellation, nor grow its buffer without bound.
func TestExpectOnANoisyConsoleKeepsItsBounds(t *testing.T) {
	t.Parallel()

	serial := &noisySerial{reads: 0}
	cons := &console{f: &Flasher{Serial: serial, Power: nil, Upload: nil, Log: nil, Scale: 0}, buf: nil}

	start := time.Now()

	_, err := cons.expect(context.Background(), 50*time.Millisecond, "never printed")
	if err == nil {
		t.Fatal("expect found a marker that was never printed")
	}

	if d := time.Since(start); d > time.Second {
		t.Fatalf("expect returned after %v; its timeout was 50ms", d)
	}

	if len(cons.buf) > consoleKeep {
		t.Fatalf("console kept %d bytes, limit %d", len(cons.buf), consoleKeep)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	serial.reads = 0

	_, err = cons.expect(ctx, time.Minute, "never printed")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expect on a cancelled context = %v, want context.Canceled", err)
	}

	if serial.reads != 0 {
		t.Fatalf("a cancelled expect still read the UART %d times", serial.reads)
	}
}

// The boot-time AutoInstall approves an image while HTTP handlers check uploads: the
// allowlist must survive both at once. Run under -race.
func TestAllowImageAndCheckAppImageRunConcurrently(t *testing.T) {
	t.Parallel()

	img := []byte("synthetic BCO image")
	want := sha256hex(img)

	var wg sync.WaitGroup

	wg.Go(func() {
		for range 1000 {
			err := AllowImage(img, want)
			if err != nil {
				t.Error(err)

				return
			}
		}
	})
	wg.Go(func() {
		for range 1000 {
			_, _ = CheckAppImage(img)
		}
	})

	wg.Wait()

	_, err := CheckAppImage(img)
	if err != nil {
		t.Fatal(err)
	}
}
