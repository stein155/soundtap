// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// soapAction extracts the action name from a SOAPACTION header value like
// "urn:schemas-upnp-org:service:AVTransport:1#Play" -> "Play".
func soapAction(header string) string {
	header = strings.Trim(strings.TrimSpace(header), `"`)
	if i := strings.LastIndex(header, "#"); i >= 0 {
		return header[i+1:]
	}

	return header
}

// searchTarget pulls the ST header out of an M-SEARCH datagram.
func searchTarget(msg string) string {
	for line := range strings.SplitSeq(msg, "\r\n") {
		if len(line) >= 3 && strings.EqualFold(line[:3], "ST:") {
			return strings.TrimSpace(line[3:])
		}
	}

	return ""
}

// soapBodyTimeout bounds how long a controller may take to send its SOAP body. The
// 64 KiB limit bounds its size only: a peer that stopped sending held the connection
// and its handler for good. A real request arrives in milliseconds.
var soapBodyTimeout = 10 * time.Second //nolint:gochecknoglobals // a variable so the stalled-body test can shorten it

// readBody returns the whole SOAP body, or answers 400 and reports false when the
// read failed or the body passed 64 KiB: no action may run from a partial body.
func readBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(soapBodyTimeout))

	const limit = 64 << 10

	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || len(body) > limit {
		http.Error(w, "bad request body", http.StatusBadRequest)

		return "", false
	}

	return string(body), true
}

func firstGroup(re *regexp.Regexp, text string) string {
	if m := re.FindStringSubmatch(text); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}

	return ""
}

// unescapeXML reads element text: entities, or a CDATA section, which some controllers
// wrap the URI and the DIDL-Lite metadata in.
func unescapeXML(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<![CDATA[") && strings.HasSuffix(text, "]]>") {
		return text[len("<![CDATA[") : len(text)-len("]]>")]
	}

	return html.UnescapeString(text)
}

// xmlEscape escapes a value for use in element text (e.g. a user-set speaker name).
func xmlEscape(s string) string {
	var b strings.Builder

	_ = xml.EscapeText(&b, []byte(s))

	return b.String()
}

func writeXML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	_, _ = io.WriteString(w, xmlDecl+body)
}

// soapOK writes a SOAP action response. inner is the raw XML of the response args.
func soapOK(w http.ResponseWriter, action, urn, inner string) {
	body := fmt.Sprintf(soapEnvelope,
		`<u:`+action+`Response xmlns:u="`+urn+`">`+inner+`</u:`+action+`Response>`)

	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, xmlDecl+body)
}

// soapFail writes a SOAP fault carrying a UPnPError code, as controllers expect.
func soapFail(w http.ResponseWriter, code faultCode) {
	fault := fmt.Sprintf(`<s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring>`+
		`<detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0">`+
		`<errorCode>%d</errorCode><errorDescription>%s</errorDescription></UPnPError></detail></s:Fault>`,
		int(code), code.description())

	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, xmlDecl+fmt.Sprintf(soapEnvelope, fault))
}

// faultCode is a UPnPError code of a SOAP fault, as the UPnP AV service specifications
// number them.
type faultCode int

const (
	faultInvalidAction          faultCode = 401
	faultInvalidArgs            faultCode = 402
	faultActionFailed           faultCode = 501
	faultTransitionNotAvailable faultCode = 701
	faultSeekModeNotSupported   faultCode = 710
	faultIllegalSeekTarget      faultCode = 711
	faultPlayModeNotSupported   faultCode = 712
)

// description is the errorDescription a fault carries with its code.
func (c faultCode) description() string {
	switch c {
	case faultInvalidAction:
		return "Invalid Action"
	case faultInvalidArgs:
		return "Invalid Args"
	case faultActionFailed:
		return "Action Failed"
	case faultTransitionNotAvailable:
		return "Transition not available"
	case faultSeekModeNotSupported:
		return "Seek mode not supported"
	case faultIllegalSeekTarget:
		return "Illegal seek target"
	case faultPlayModeNotSupported:
		return "Play mode not supported"
	default:
		return "" // every code soapFail is called with is named above
	}
}

const xmlDecl = `<?xml version="1.0" encoding="utf-8"?>` + "\n"

const soapEnvelope = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
	`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>%s</s:Body></s:Envelope>`

// descTmpl is the MediaRenderer device description. %s = friendlyName, %s = UDN.
// Every service sends GENA events (events.go).
const descTmpl = `<root xmlns="urn:schemas-upnp-org:device-1-0">` +
	`<specVersion><major>1</major><minor>0</minor></specVersion>` +
	`<device><deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>` +
	`<friendlyName>%s</friendlyName><manufacturer>SoundTap</manufacturer>` +
	`<modelName>SoundTap</modelName><UDN>%s</UDN>` +
	`<serviceList>` +
	`<service><serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>` +
	`<serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>` +
	`<controlURL>/upnp/control/avtransport</controlURL><eventSubURL>/upnp/event/avtransport</eventSubURL>` +
	`<SCPDURL>/upnp/avtransport.xml</SCPDURL></service>` +
	`<service><serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>` +
	`<serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId>` +
	`<controlURL>/upnp/control/renderingcontrol</controlURL><eventSubURL>/upnp/event/renderingcontrol</eventSubURL>` +
	`<SCPDURL>/upnp/renderingcontrol.xml</SCPDURL></service>` +
	`<service><serviceType>urn:schemas-upnp-org:service:ConnectionManager:1</serviceType>` +
	`<serviceId>urn:upnp-org:serviceId:ConnectionManager</serviceId>` +
	`<controlURL>/upnp/control/connectionmanager</controlURL><eventSubURL>/upnp/event/connectionmanager</eventSubURL>` +
	`<SCPDURL>/upnp/connectionmanager.xml</SCPDURL></service>` +
	`</serviceList></device></root>`
