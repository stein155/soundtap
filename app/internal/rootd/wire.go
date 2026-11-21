// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package rootd

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
)

// A message on the socket is a 4-byte big-endian length and that much JSON. Descriptors
// travel with the request's first bytes, at most maxFDs of them.
const maxFDs = 2

// writeMessage sends v, with files as SCM_RIGHTS on the same write.
func writeMessage(c *net.UnixConn, v any, files ...*os.File) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(body) > MaxMessage {
		return fmt.Errorf("message of %d bytes is over %d", len(body), MaxMessage)
	}
	msg := binary.BigEndian.AppendUint32(make([]byte, 0, 4+len(body)), uint32(len(body))) //nolint:gosec // at most MaxMessage, checked above
	msg = append(msg, body...)
	var oob []byte
	if len(files) > 0 {
		fds := make([]int, len(files))
		for i, f := range files {
			fds[i] = int(f.Fd())
		}
		oob = syscall.UnixRights(fds...)
	}
	n, _, err := c.WriteMsgUnix(msg, oob, nil)
	if err != nil {
		return err
	}
	if n < len(msg) {
		_, err = c.Write(msg[n:])
	}
	return err
}

// readMessage reads one message into v and returns the descriptors that came with it. A
// message over MaxMessage, more descriptors than maxFDs or truncated control data is
// refused, and every descriptor received is closed then.
func readMessage(c *net.UnixConn, v any) (files []*os.File, err error) {
	buf := make([]byte, 4+MaxMessage)
	oob := make([]byte, syscall.CmsgSpace(4*(maxFDs+1)))
	n, oobn, flags, _, err := c.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, err
	}
	files, ferr := parseRights(oob[:oobn])
	defer func() {
		if err != nil {
			closeAll(files)
			files = nil
		}
	}()
	if ferr != nil {
		return files, ferr
	}
	if flags&syscall.MSG_CTRUNC != 0 {
		return files, errors.New("too many descriptors")
	}
	if len(files) > maxFDs {
		return files, fmt.Errorf("%d descriptors; at most %d", len(files), maxFDs)
	}
	for n < 4 {
		m, err := c.Read(buf[n:4])
		if err != nil {
			return files, err
		}
		n += m
	}
	size := int(binary.BigEndian.Uint32(buf))
	if size > MaxMessage {
		return files, fmt.Errorf("message of %d bytes is over %d", size, MaxMessage)
	}
	if n > 4+size {
		return files, errors.New("more than one message")
	}
	if _, err := io.ReadFull(c, buf[n:4+size]); err != nil {
		return files, err
	}
	dec := json.NewDecoder(bytes.NewReader(buf[4 : 4+size]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return files, fmt.Errorf("bad message: %w", err)
	}
	return files, nil
}

func parseRights(oob []byte) ([]*os.File, error) {
	if len(oob) == 0 {
		return nil, nil
	}
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	var files []*os.File
	for _, m := range msgs {
		fds, err := syscall.ParseUnixRights(&m)
		if err != nil {
			closeAll(files)
			return nil, err
		}
		for _, fd := range fds {
			files = append(files, os.NewFile(uintptr(fd), "passed"))
		}
	}
	return files, nil
}

func closeAll(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}
