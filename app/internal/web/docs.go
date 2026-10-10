// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/http"
)

// The API reference is two small embedded files, no CDN and no UI bundle: the page
// renders openapi.json itself, so it works on a LAN with no internet. The spec is
// kept indented in the repo so it can be edited and reviewed, and served compacted.
var (
	//go:embed openapi.json
	openapiSource []byte
	openapiSpec   = compactJSON(openapiSource)
	//go:embed docs.html
	docsPage []byte
)

func (s *Server) openapi(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(openapiSpec)
}

func (s *Server) docs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(docsPage)
}

func compactJSON(b []byte) []byte {
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil {
		panic("openapi.json: " + err.Error())
	}
	return out.Bytes()
}
