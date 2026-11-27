// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// cmControl is ConnectionManager: one fixed connection that sinks what ffmpeg can play.
// Controllers read the sink list to choose the format they stream.
func cmControl(w http.ResponseWriter, req *http.Request) {
	switch soapAction(req.Header.Get("SoapAction")) {
	case "GetProtocolInfo":
		soapOK(w, "GetProtocolInfo", connMgrURN, "<Source></Source><Sink>"+sinkProtocols+"</Sink>")
	case "GetCurrentConnectionIDs":
		soapOK(w, "GetCurrentConnectionIDs", connMgrURN, "<ConnectionIDs>0</ConnectionIDs>")
	case "GetCurrentConnectionInfo":
		soapOK(w, "GetCurrentConnectionInfo", connMgrURN,
			"<RcsID>0</RcsID><AVTransportID>0</AVTransportID><ProtocolInfo></ProtocolInfo>"+
				"<PeerConnectionManager></PeerConnectionManager><PeerConnectionID>-1</PeerConnectionID>"+
				"<Direction>Input</Direction><Status>OK</Status>")
	default:
		soapFail(w, faultInvalidAction)
	}
}

// RenderingControl's actions, and the speaker's volume range they work in.
const (
	actGetVolume = "GetVolume"
	actGetMute   = "GetMute"
	actSetVolume = "SetVolume"
	actSetMute   = "SetMute"
	maxVolume    = 100
)

// rcControl is RenderingControl on the Master channel: the speaker's own volume and
// mute. Volume is 0..100 like the speaker's; mute is a flag beside it, so GetVolume
// reports the level an unmute returns to, as a controller's slider expects.
func (r *Renderer) rcControl(w http.ResponseWriter, req *http.Request) { //nolint:cyclop // moved unchanged
	action := soapAction(req.Header.Get("SoapAction"))

	body, ok := readBody(w, req)
	if !ok {
		return
	}

	ctx := req.Context()

	if ch := firstGroup(reChannel, body); ch != "" && ch != "Master" {
		soapFail(w, faultInvalidArgs)

		return
	}

	switch action {
	case actGetVolume, actGetMute:
		volume, err := r.Backend.Volume(ctx)
		if err != nil {
			soapFail(w, faultActionFailed)

			return
		}

		if action == actGetVolume {
			soapOK(w, actGetVolume, renderURN, fmt.Sprintf("<CurrentVolume>%d</CurrentVolume>", volume))

			return
		}

		soapOK(w, actGetMute, renderURN, "<CurrentMute>"+muteValue(r.Backend)+"</CurrentMute>")
	case actSetVolume:
		volume, err := strconv.Atoi(firstGroup(reVolume, body))
		if err != nil || volume < 0 || volume > maxVolume {
			soapFail(w, faultInvalidArgs)

			return
		}

		err = r.Backend.SetVolume(ctx, volume)
		if err != nil {
			soapFail(w, faultActionFailed)

			return
		}

		soapOK(w, actSetVolume, renderURN, "")
	case actSetMute:
		var mute bool

		switch strings.ToLower(firstGroup(reMute, body)) {
		case "1", "true", "yes":
			mute = true
		case "0", "false", "no":
		default:
			soapFail(w, faultInvalidArgs)

			return
		}

		err := r.Backend.SetMute(ctx, mute)
		if err != nil {
			soapFail(w, faultActionFailed)

			return
		}

		soapOK(w, actSetMute, renderURN, "")
	default:
		soapFail(w, faultInvalidAction)
	}
}

// muteValue is the backend's mute as RenderingControl's boolean Mute variable.
func muteValue(b Backend) string {
	if b.Muted() {
		return "1"
	}

	return "0"
}
