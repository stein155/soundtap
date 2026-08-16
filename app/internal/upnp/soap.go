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
func soapAction(h string) string {
	h = strings.Trim(strings.TrimSpace(h), `"`)
	if i := strings.LastIndex(h, "#"); i >= 0 {
		return h[i+1:]
	}
	return h
}

// searchTarget pulls the ST header out of an M-SEARCH datagram.
func searchTarget(msg string) string {
	for _, line := range strings.Split(msg, "\r\n") {
		if len(line) >= 3 && strings.EqualFold(line[:3], "ST:") {
			return strings.TrimSpace(line[3:])
		}
	}
	return ""
}

// soapBodyTimeout bounds how long a controller may take to send its SOAP body. The
// 64 KiB limit bounds its size only: a peer that stopped sending held the connection
// and its handler for good. A real request arrives in milliseconds.
var soapBodyTimeout = 10 * time.Second

// readBody returns the whole SOAP body, or answers 400 and reports false when the
// read failed or the body passed 64 KiB: no action may run from a partial body.
func readBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(soapBodyTimeout))
	const limit = 64 << 10
	b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || len(b) > limit {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return "", false
	}
	return string(b), true
}

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// unescapeXML reads element text: entities, or a CDATA section, which some controllers
// wrap the URI and the DIDL-Lite metadata in.
func unescapeXML(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<![CDATA[") && strings.HasSuffix(s, "]]>") {
		return s[len("<![CDATA[") : len(s)-len("]]>")]
	}
	return html.UnescapeString(s)
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
		fmt.Sprintf(`<u:%sResponse xmlns:u="%s">%s</u:%sResponse>`, action, urn, inner, action))
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, xmlDecl+body)
}

// soapFail writes a SOAP fault carrying a UPnPError code, as controllers expect.
func soapFail(w http.ResponseWriter, code int, desc string) {
	fault := fmt.Sprintf(`<s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring>`+
		`<detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0">`+
		`<errorCode>%d</errorCode><errorDescription>%s</errorDescription></UPnPError></detail></s:Fault>`,
		code, desc)
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, xmlDecl+fmt.Sprintf(soapEnvelope, fault))
}

const xmlDecl = `<?xml version="1.0" encoding="utf-8"?>` + "\n"

const soapEnvelope = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
	`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>%s</s:Body></s:Envelope>`

// descTmpl is the MediaRenderer device description. %s = friendlyName, %s = UDN.
// Every service sends GENA events (events.go).
const descTmpl = `<root xmlns="urn:schemas-upnp-org:device-1-0"><specVersion><major>1</major><minor>0</minor></specVersion>` +
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
