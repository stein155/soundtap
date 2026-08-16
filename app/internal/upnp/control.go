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
	switch action := soapAction(req.Header.Get("SOAPACTION")); action {
	case "GetProtocolInfo":
		soapOK(w, action, connMgrURN, "<Source></Source><Sink>"+sinkProtocols+"</Sink>")
	case "GetCurrentConnectionIDs":
		soapOK(w, action, connMgrURN, "<ConnectionIDs>0</ConnectionIDs>")
	case "GetCurrentConnectionInfo":
		soapOK(w, action, connMgrURN, "<RcsID>0</RcsID><AVTransportID>0</AVTransportID><ProtocolInfo></ProtocolInfo>"+
			"<PeerConnectionManager></PeerConnectionManager><PeerConnectionID>-1</PeerConnectionID>"+
			"<Direction>Input</Direction><Status>OK</Status>")
	default:
		soapFail(w, 401, "Invalid Action")
	}
}

const actGetVolume = "GetVolume"

// rcControl is RenderingControl on the Master channel: the speaker's own volume and
// mute. Volume is 0..100 like the speaker's; mute is volume 0.
func (r *Renderer) rcControl(w http.ResponseWriter, req *http.Request) { //nolint:cyclop // moved unchanged
	action := soapAction(req.Header.Get("SOAPACTION"))
	body, ok := readBody(w, req)
	if !ok {
		return
	}
	ctx := req.Context()
	if ch := firstGroup(reChannel, body); ch != "" && ch != "Master" {
		soapFail(w, 402, "Invalid Args")
		return
	}
	switch action {
	case actGetVolume, "GetMute":
		v, err := r.Backend.Volume(ctx)
		if err != nil {
			soapFail(w, 501, "Action Failed")
			return
		}
		if action == actGetVolume {
			soapOK(w, action, renderURN, fmt.Sprintf("<CurrentVolume>%d</CurrentVolume>", v))
			return
		}
		muted := "0"
		if v == 0 {
			muted = "1"
		}
		soapOK(w, action, renderURN, "<CurrentMute>"+muted+"</CurrentMute>")
	case "SetVolume":
		v, err := strconv.Atoi(firstGroup(reVolume, body))
		if err != nil || v < 0 || v > 100 {
			soapFail(w, 402, "Invalid Args")
			return
		}
		if err := r.Backend.SetVolume(ctx, v); err != nil {
			soapFail(w, 501, "Action Failed")
			return
		}
		soapOK(w, action, renderURN, "")
	case "SetMute":
		var on bool
		switch strings.ToLower(firstGroup(reMute, body)) {
		case "1", "true", "yes":
			on = true
		case "0", "false", "no":
		default:
			soapFail(w, 402, "Invalid Args")
			return
		}
		if err := r.Backend.SetMute(ctx, on); err != nil {
			soapFail(w, 501, "Action Failed")
			return
		}
		soapOK(w, action, renderURN, "")
	default:
		soapFail(w, 401, "Invalid Action")
	}
}
