// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"context"
	"io"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// feed reads fixed-size blocks from the source bus into ch. A FIFO stays open across
// sources, so a transient EOF (all writers closed) is treated as a gap: feed keeps
// trying until ctx ends rather than closing ch.
//
// The bus pipe is kept small (BusPipeBytes) so what feed cannot drop on a flush stays
// bounded, and so the bus writers get real back-pressure instead of running ahead.
func (m *Mixer) feed(ctx context.Context, source io.Reader, blocks chan<- pcmBlock) {
	// A ring of buffers two wider than the channel, so a block still queued (or the
	// one Run is mixing) is never overwritten. Reusing them keeps the feeder out of
	// the allocator entirely. The channel's own capacity, not sourceBufferBlocks: a
	// long prebuffer (the ST20's 6 s) widens the channel, and a narrower ring then
	// rewrote blocks still waiting to be played.
	ring := make([][]byte, cap(blocks)+feedRingSpare)
	for i := range ring {
		ring[i] = make([]byte, blockBytes)
	}

	slot := 0
	seen := uint64(0)

	if f, ok := source.(*os.File); ok {
		m.bus.Store(f)
		defer m.bus.CompareAndSwap(f, nil)
	}

	for {
		if ctx.Err() != nil {
			return
		}

		seen = m.dropFlushed(source, seen)

		buf := ring[slot]
		read, err := io.ReadFull(source, buf)
		// A short read ends at EOF, possibly mid-frame. Run mixes whole stereo frames
		// only; a trailing partial frame is dropped rather than sliced at a negative
		// offset.
		read -= read % frameBytes

		if gen := m.flushGen.Load(); gen != seen {
			// The flush landed during this read: the block is (at least partly) old.
			continue
		}

		if read > 0 {
			slot = (slot + 1) % len(ring)
			// Tagged with the flush generation it was read under: a send blocked on a
			// full channel can complete right after Run drained it for a flush, and
			// Run must then still recognise the block as the old stream's.
			select {
			case blocks <- pcmBlock{buf[:read], seen}:
			case <-ctx.Done():
				return
			}
		}

		if err != nil && read == 0 {
			// No data right now (all bus writers closed -> EOF, or a transient read
			// error). Do NOT exit — the bus is long-lived and a later source will write
			// again; just wait a block-duration so we don't hot-spin, then retry. (In
			// production the bus is opened O_RDWR so this rarely fires, but the feeder's
			// survival must not depend on that.)
			select {
			case <-ctx.Done():
				return
			case <-time.After(blockFrames * time.Second / SampleRate):
			}
		}
	}
}

// dropFlushed acknowledges a flush newer than seen and returns the generation now seen.
//
// A flush also owns what sits in the bus FIFO at that moment: the writer that produced
// it (the killed ffmpeg of the previous preset, librespot before a skip) is gone, so it
// is the previous stream's tail. Left in place it became the first blocks after the
// flush and played as a blip of the old station right before the new one started.
func (m *Mixer) dropFlushed(source io.Reader, seen uint64) uint64 {
	if gen := m.flushGen.Load(); gen == seen {
		return seen
	}

	m.flushWakeMu.Lock()
	gen := m.flushGen.Load()

	if f, ok := source.(*os.File); ok {
		_ = f.SetReadDeadline(time.Time{})
	}

	discardQueued(source)
	m.flushAck.Store(gen)
	m.flushWakeMu.Unlock()

	return gen
}

// takeBlock takes the next queued block. On a dry queue it waits a beat rather than
// punching a hole in the stream at once: only a real stall (stalled) is an underrun.
// done is ctx ending.
func takeBlock(ctx context.Context, blocks <-chan pcmBlock, wait *time.Timer) (pcmBlock, bool, bool) {
	var block pcmBlock

	select {
	case block = <-blocks:
		return block, false, false
	default:
	}

	wait.Reset(sourceWait)

	select {
	case block = <-blocks:
		if !wait.Stop() {
			<-wait.C
		}

		return block, false, false
	case <-wait.C:
		return block, true, false
	case <-ctx.Done():
		if !wait.Stop() {
			<-wait.C
		}

		return block, false, true
	}
}

// pcmBlock is one bus read, tagged with the flush generation it was read under.
type pcmBlock struct {
	pcm []byte
	gen uint64
}

// discardQueued drops the bytes waiting in a pipe right now without blocking for more.
// Anything that is not a pipe (tests use io.Pipe) has nothing kernel-side to drop.
// fionread is FIONREAD/TIOCINQ on Linux; not every GOARCH's syscall package names it.
const fionread = 0x541B

// feedRingSpare is how many buffers feed's ring holds beyond the channel's capacity: the
// block Run is mixing and the one feed is reading into.
const feedRingSpare = 2

func discardQueued(r io.Reader) {
	file, ok := r.(*os.File)
	if !ok {
		return
	}

	raw, err := file.SyscallConn()
	if err != nil {
		return
	}

	var queued int32 // FIONREAD writes a C int

	_ = raw.Control(func(fd uintptr) {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, fionread, uintptr(unsafe.Pointer(&queued)))
		if errno != 0 {
			queued = 0
		}
	})
	// Whole frames only: a writer caught mid-write must not leave the next read
	// starting halfway through a sample.
	if queued -= queued % frameBytes; queued > 0 {
		_, _ = io.CopyN(io.Discard, file, int64(queued))
	}
}
