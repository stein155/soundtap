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
const (
	maxFDs     = 2
	lengthSize = 4
	fdSize     = 4 // an int in the SCM_RIGHTS payload
)

// The refusals of a message. A message with values reads them around its sentinel.
var (
	errOverLimit   = errors.New("bytes is over")
	errTooManyFDs  = errors.New("too many descriptors")
	errFDCount     = errors.New("descriptors; at most")
	errMoreThanOne = errors.New("more than one message")
)

// writeMessage sends v, with files as SCM_RIGHTS on the same write.
func writeMessage(conn *net.UnixConn, v any, files ...*os.File) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding the message: %w", err)
	}

	if len(body) > MaxMessage {
		return fmt.Errorf("message of %d %w %d", len(body), errOverLimit, MaxMessage)
	}

	msg := make([]byte, 0, lengthSize+len(body))
	msg = binary.BigEndian.AppendUint32(msg, uint32(len(body))) //nolint:gosec // at most MaxMessage, checked above
	msg = append(msg, body...)

	var oob []byte

	if len(files) > 0 {
		fds := make([]int, len(files))
		for i, file := range files {
			fds[i] = int(file.Fd())
		}

		oob = syscall.UnixRights(fds...)
	}

	written, _, err := conn.WriteMsgUnix(msg, oob, nil)
	if err != nil {
		return fmt.Errorf("sending the message: %w", err)
	}

	if written < len(msg) {
		_, err = conn.Write(msg[written:])
		if err != nil {
			return fmt.Errorf("sending the message: %w", err)
		}
	}

	return nil
}

// readMessage reads one message into v and returns the descriptors that came with it. A
// message over MaxMessage, more descriptors than maxFDs or truncated control data is
// refused, and every descriptor received is closed then.
func readMessage(conn *net.UnixConn, into any) ([]*os.File, error) {
	buf := make([]byte, lengthSize+MaxMessage)
	oob := make([]byte, syscall.CmsgSpace(fdSize*(maxFDs+1)))

	got, oobn, flags, _, err := conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, fmt.Errorf("receiving the message: %w", err)
	}

	files, err := parseRights(oob[:oobn])
	if err == nil {
		err = readBody(conn, buf, got, flags, len(files), into)
	}

	if err != nil {
		closeAll(files)

		return nil, err
	}

	return files, nil
}

// readBody checks what came with the first read of a message, got bytes of buf with
// control flags and fds descriptors, and reads the rest of the message into v.
func readBody(conn *net.UnixConn, buf []byte, got, flags, fds int, into any) error {
	if flags&syscall.MSG_CTRUNC != 0 {
		return errTooManyFDs
	}

	if fds > maxFDs {
		return fmt.Errorf("%d %w %d", fds, errFDCount, maxFDs)
	}

	for got < lengthSize {
		more, err := conn.Read(buf[got:lengthSize])
		if err != nil {
			return fmt.Errorf("receiving the message: %w", err)
		}

		got += more
	}

	size := int(binary.BigEndian.Uint32(buf))
	if size > MaxMessage {
		return fmt.Errorf("message of %d %w %d", size, errOverLimit, MaxMessage)
	}

	if got > lengthSize+size {
		return errMoreThanOne
	}

	_, err := io.ReadFull(conn, buf[got:lengthSize+size])
	if err != nil {
		return fmt.Errorf("receiving the message: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(buf[lengthSize : lengthSize+size]))
	dec.DisallowUnknownFields()

	err = dec.Decode(into)
	if err != nil {
		return fmt.Errorf("bad message: %w", err)
	}

	return nil
}

func parseRights(oob []byte) ([]*os.File, error) {
	if len(oob) == 0 {
		return nil, nil
	}

	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, fmt.Errorf("control data: %w", err)
	}

	var files []*os.File

	for _, msg := range msgs {
		fds, err := syscall.ParseUnixRights(&msg)
		if err != nil {
			closeAll(files)

			return nil, fmt.Errorf("control data: %w", err)
		}

		for _, fd := range fds {
			files = append(files, os.NewFile(uintptr(fd), "passed"))
		}
	}

	return files, nil
}

func closeAll(files []*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}
