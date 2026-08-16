// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"fmt"
	"strings"
)

// A service description (SCPD) lists the actions a controller may call and their
// arguments. Controllers built on async_upnp_client, Music Assistant's DLNA provider
// among them, call only what the SCPD lists: with the empty stubs served before, Music
// Assistant failed every play with "Missing action AVT/SetAVTransportURI" and showed
// no pause or volume.

// scpdArg is one argument: "in" or "out", its name and the state variable it carries.
type scpdArg struct{ dir, name, state string }

type scpdAction struct {
	name string
	args []scpdArg
}

func in(name, state string) scpdArg  { return scpdArg{"in", name, state} }
func out(name, state string) scpdArg { return scpdArg{"out", name, state} }

var instanceID = in("InstanceID", "A_ARG_TYPE_InstanceID")

// sinkProtocols is what the speaker's ffmpeg can play (os/package/soundtap-ffmpeg),
// so a controller picks a format that works.
const sinkProtocols = "http-get:*:audio/flac:*,http-get:*:audio/x-flac:*,http-get:*:audio/mpeg:*," +
	"http-get:*:audio/mp3:*,http-get:*:audio/aac:*,http-get:*:audio/x-aac:*,http-get:*:audio/mp4:*," +
	"http-get:*:audio/x-m4a:*,http-get:*:audio/ogg:*,http-get:*:audio/wav:*,http-get:*:audio/x-wav:*,http-get:*:audio/x-ms-wma:*," +
	"http-get:*:application/x-mpegurl:*,http-get:*:application/vnd.apple.mpegurl:*,http-get:*:audio/vnd.apple.mpegurl:*"

var avTransportSCPD = scpd([]scpdAction{
	{"SetAVTransportURI", []scpdArg{instanceID, in("CurrentURI", "AVTransportURI"),
		in("CurrentURIMetaData", "AVTransportURIMetaData")}},
	{"SetNextAVTransportURI", []scpdArg{instanceID, in("NextURI", "NextAVTransportURI"),
		in("NextURIMetaData", "NextAVTransportURIMetaData")}},
	{"Play", []scpdArg{instanceID, in("Speed", "TransportPlaySpeed")}},
	{"Pause", []scpdArg{instanceID}},
	{"Stop", []scpdArg{instanceID}},
	{"Seek", []scpdArg{instanceID, in("Unit", "A_ARG_TYPE_SeekMode"), in("Target", "A_ARG_TYPE_SeekTarget")}},
	{"GetTransportInfo", []scpdArg{instanceID, out("CurrentTransportState", "TransportState"),
		out("CurrentTransportStatus", "TransportStatus"), out("CurrentSpeed", "TransportPlaySpeed")}},
	{"GetPositionInfo", []scpdArg{instanceID, out("Track", "CurrentTrack"),
		out("TrackDuration", "CurrentTrackDuration"), out("TrackMetaData", "CurrentTrackMetaData"),
		out("TrackURI", "CurrentTrackURI"), out("RelTime", "RelativeTimePosition"),
		out("AbsTime", "AbsoluteTimePosition"), out("RelCount", "RelativeCounterPosition"),
		out("AbsCount", "AbsoluteCounterPosition")}},
	{"GetMediaInfo", []scpdArg{instanceID, out("NrTracks", "NumberOfTracks"),
		out("MediaDuration", "CurrentMediaDuration"), out("CurrentURI", "AVTransportURI"),
		out("CurrentURIMetaData", "AVTransportURIMetaData"), out("NextURI", "NextAVTransportURI"),
		out("NextURIMetaData", "NextAVTransportURIMetaData"), out("PlayMedium", "PlaybackStorageMedium"),
		out("RecordMedium", "RecordStorageMedium"), out("WriteStatus", "RecordMediumWriteStatus")}},
	{"GetCurrentTransportActions", []scpdArg{instanceID, out("Actions", "CurrentTransportActions")}},
	{"Next", []scpdArg{instanceID}},
	{"Previous", []scpdArg{instanceID}},
	{"SetPlayMode", []scpdArg{instanceID, in("NewPlayMode", "CurrentPlayMode")}},
	{"GetTransportSettings", []scpdArg{instanceID, out("PlayMode", "CurrentPlayMode"),
		out("RecQualityMode", "CurrentRecordQualityMode")}},
	{"GetDeviceCapabilities", []scpdArg{instanceID, out("PlayMedia", "PossiblePlaybackStorageMedia"),
		out("RecMedia", "PossibleRecordStorageMedia"), out("RecQualityModes", "PossibleRecordQualityModes")}},
}, `<stateVariable sendEvents="yes"><name>LastChange</name><dataType>string</dataType></stateVariable>`+
	`<stateVariable sendEvents="no"><name>TransportState</name><dataType>string</dataType>`+
	`<allowedValueList><allowedValue>STOPPED</allowedValue><allowedValue>PLAYING</allowedValue>`+
	`<allowedValue>PAUSED_PLAYBACK</allowedValue><allowedValue>TRANSITIONING</allowedValue><allowedValue>NO_MEDIA_PRESENT</allowedValue></allowedValueList></stateVariable>`+
	`<stateVariable sendEvents="no"><name>TransportStatus</name><dataType>string</dataType>`+
	`<allowedValueList><allowedValue>OK</allowedValue><allowedValue>ERROR_OCCURRED</allowedValue></allowedValueList></stateVariable>`+
	`<stateVariable sendEvents="no"><name>TransportPlaySpeed</name><dataType>string</dataType>`+
	`<allowedValueList><allowedValue>1</allowedValue></allowedValueList></stateVariable>`+
	stringVars("no", "AVTransportURI", "AVTransportURIMetaData", "NextAVTransportURI", "NextAVTransportURIMetaData",
		"CurrentTrackDuration", "CurrentMediaDuration", "CurrentTrackMetaData", "CurrentTrackURI",
		"RelativeTimePosition", "AbsoluteTimePosition", "PlaybackStorageMedium", "RecordStorageMedium",
		"RecordMediumWriteStatus", "CurrentTransportActions", "CurrentRecordQualityMode",
		"PossiblePlaybackStorageMedia", "PossibleRecordStorageMedia", "PossibleRecordQualityModes")+
	`<stateVariable sendEvents="no"><name>CurrentPlayMode</name><dataType>string</dataType>`+
	`<allowedValueList><allowedValue>NORMAL</allowedValue></allowedValueList></stateVariable>`+
	`<stateVariable sendEvents="no"><name>CurrentTrack</name><dataType>ui4</dataType></stateVariable>`+
	`<stateVariable sendEvents="no"><name>NumberOfTracks</name><dataType>ui4</dataType></stateVariable>`+
	`<stateVariable sendEvents="no"><name>RelativeCounterPosition</name><dataType>i4</dataType></stateVariable>`+
	`<stateVariable sendEvents="no"><name>AbsoluteCounterPosition</name><dataType>i4</dataType></stateVariable>`+
	`<stateVariable sendEvents="no"><name>A_ARG_TYPE_SeekMode</name><dataType>string</dataType>`+
	`<allowedValueList><allowedValue>REL_TIME</allowedValue><allowedValue>ABS_TIME</allowedValue></allowedValueList></stateVariable>`+
	`<stateVariable sendEvents="no"><name>A_ARG_TYPE_SeekTarget</name><dataType>string</dataType></stateVariable>`+
	`<stateVariable sendEvents="no"><name>A_ARG_TYPE_InstanceID</name><dataType>ui4</dataType></stateVariable>`)

var channel = in("Channel", "A_ARG_TYPE_Channel")

var renderingControlSCPD = scpd([]scpdAction{
	{"GetVolume", []scpdArg{instanceID, channel, out("CurrentVolume", "Volume")}},
	{"SetVolume", []scpdArg{instanceID, channel, in("DesiredVolume", "Volume")}},
	{"GetMute", []scpdArg{instanceID, channel, out("CurrentMute", "Mute")}},
	{"SetMute", []scpdArg{instanceID, channel, in("DesiredMute", "Mute")}},
}, `<stateVariable sendEvents="yes"><name>LastChange</name><dataType>string</dataType></stateVariable>`+
	`<stateVariable sendEvents="no"><name>Volume</name><dataType>ui2</dataType>`+
	`<allowedValueRange><minimum>0</minimum><maximum>100</maximum><step>1</step></allowedValueRange></stateVariable>`+
	`<stateVariable sendEvents="no"><name>Mute</name><dataType>boolean</dataType></stateVariable>`+
	`<stateVariable sendEvents="no"><name>A_ARG_TYPE_Channel</name><dataType>string</dataType>`+
	`<allowedValueList><allowedValue>Master</allowedValue></allowedValueList></stateVariable>`+
	`<stateVariable sendEvents="no"><name>A_ARG_TYPE_InstanceID</name><dataType>ui4</dataType></stateVariable>`)

var connectionManagerSCPD = scpd([]scpdAction{
	{"GetProtocolInfo", []scpdArg{out("Source", "SourceProtocolInfo"), out("Sink", "SinkProtocolInfo")}},
	{"GetCurrentConnectionIDs", []scpdArg{out("ConnectionIDs", "CurrentConnectionIDs")}},
	{"GetCurrentConnectionInfo", []scpdArg{in("ConnectionID", "A_ARG_TYPE_ConnectionID"),
		out("RcsID", "A_ARG_TYPE_RcsID"), out("AVTransportID", "A_ARG_TYPE_AVTransportID"),
		out("ProtocolInfo", "A_ARG_TYPE_ProtocolInfo"), out("PeerConnectionManager", "A_ARG_TYPE_ConnectionManager"),
		out("PeerConnectionID", "A_ARG_TYPE_ConnectionID"), out("Direction", "A_ARG_TYPE_Direction"),
		out("Status", "A_ARG_TYPE_ConnectionStatus")}},
}, stringVars("yes", "SourceProtocolInfo", "SinkProtocolInfo", "CurrentConnectionIDs")+
	stringVars("no", "A_ARG_TYPE_ProtocolInfo", "A_ARG_TYPE_ConnectionManager")+
	`<stateVariable sendEvents="no"><name>A_ARG_TYPE_ConnectionID</name><dataType>i4</dataType></stateVariable>`+
	`<stateVariable sendEvents="no"><name>A_ARG_TYPE_RcsID</name><dataType>i4</dataType></stateVariable>`+
	`<stateVariable sendEvents="no"><name>A_ARG_TYPE_AVTransportID</name><dataType>i4</dataType></stateVariable>`+
	`<stateVariable sendEvents="no"><name>A_ARG_TYPE_Direction</name><dataType>string</dataType>`+
	`<allowedValueList><allowedValue>Input</allowedValue><allowedValue>Output</allowedValue></allowedValueList></stateVariable>`+
	`<stateVariable sendEvents="no"><name>A_ARG_TYPE_ConnectionStatus</name><dataType>string</dataType>`+
	`<allowedValueList><allowedValue>OK</allowedValue><allowedValue>Unknown</allowedValue></allowedValueList></stateVariable>`)

// stringVars declares string state variables; sendEvents is "yes" or "no".
func stringVars(sendEvents string, names ...string) string {
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, `<stateVariable sendEvents="%s"><name>%s</name><dataType>string</dataType></stateVariable>`, sendEvents, n)
	}
	return b.String()
}

func scpd(actions []scpdAction, stateVars string) string {
	var b strings.Builder
	b.WriteString(`<scpd xmlns="urn:schemas-upnp-org:service-1-0"><specVersion><major>1</major><minor>0</minor></specVersion><actionList>`)
	for _, a := range actions {
		fmt.Fprintf(&b, `<action><name>%s</name><argumentList>`, a.name)
		for _, arg := range a.args {
			fmt.Fprintf(&b, `<argument><name>%s</name><direction>%s</direction><relatedStateVariable>%s</relatedStateVariable></argument>`,
				arg.name, arg.dir, arg.state)
		}
		b.WriteString(`</argumentList></action>`)
	}
	b.WriteString(`</actionList><serviceStateTable>` + stateVars + `</serviceStateTable></scpd>`)
	return b.String()
}
