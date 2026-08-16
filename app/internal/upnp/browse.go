// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/stein155/soundtap/app/internal/safehttp"
)

// Browse sends a ContentDirectory Browse(BrowseDirectChildren) for objectID
// on the server identified by usn. Returns up to browseTotalMax items.
func (r *Registry) Browse(ctx context.Context, usn, objectID string) ([]Item, error) {
	r.mu.Lock()
	srv := r.servers[usn]
	r.mu.Unlock()
	if srv == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownServer, usn)
	}
	// One browse at a time: each holds up to browseBytesMax plus a 1 MiB response.
	select {
	case r.browses <- struct{}{}:
		defer func() { <-r.browses }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return browseURL(ctx, r.client, srv.controlURL, objectID)
}

// browseURL is the testable core: pages through Browse until the server's TotalMatches
// is reached. A server that reports no total gets one page.
func browseURL(ctx context.Context, client *http.Client, controlURL, objectID string) ([]Item, error) {
	var out []Item
	used := 0
	for len(out) < browseTotalMax {
		items, total, err := browsePage(ctx, client, controlURL, objectID, len(out))
		if err != nil {
			return nil, err
		}
		// A server can spread a huge folder over many small-enough pages; once the byte
		// budget is spent return what fits instead of retaining the rest.
		over := false
		for _, it := range items {
			used += len(it.ID) + len(it.Title) + len(it.Class) + len(it.Artist) + len(it.Album) + len(it.Art) + len(it.URL)
			if used > browseBytesMax {
				over = true
				break
			}
			out = append(out, it)
		}
		if over || len(items) == 0 || len(out) >= total {
			break
		}
	}
	if len(out) > browseTotalMax {
		out = out[:browseTotalMax]
	}
	return out, nil
}

// browsePage fetches one page from start; total is the server's TotalMatches, 0 if unknown.
func browsePage(ctx context.Context, client *http.Client, controlURL, objectID string, start int) ([]Item, int, error) {
	body := fmt.Sprintf(browseSOAPTmpl, xmlEscape(objectID), start, browseMax)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controlURL, strings.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPACTION", `"`+cdServiceURN+`#Browse"`)

	resp, err := safehttp.Do(ctx, client, req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	// Read one byte past the cap so a document that hits the limit is reported as
	// oversized instead of silently truncated into an unparseable fragment.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, soapBodyMax+1))
	if err != nil {
		return nil, 0, err
	}
	if len(respBody) > soapBodyMax {
		return nil, 0, fmt.Errorf("Browse response exceeds %d bytes", soapBodyMax)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("Browse returned HTTP %s", resp.Status)
	}
	r, err := readBrowseResponse(respBody)
	if err != nil {
		return nil, 0, err
	}
	items, err := parseDIDL([]byte(r.result))
	return items, r.total, err
}

type browseResponse struct {
	result string
	total  int
}

// readBrowseResponse reads Result (the DIDL) and TotalMatches out of a SOAP Browse
// response with an XML decoder, so prefixes, namespace declarations and other attributes
// on either element are fine.
func readBrowseResponse(data []byte) (browseResponse, error) {
	var r browseResponse
	found := false
	d := xml.NewDecoder(bytes.NewReader(data))
	// Servers declare all sorts of encodings for what is in practice UTF-8 or ASCII.
	d.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "Result":
			if d.DecodeElement(&r.result, &start) == nil {
				found = true
			}
		case "TotalMatches":
			_ = d.DecodeElement(&r.total, &start)
		}
	}
	if !found {
		return r, fmt.Errorf("no <Result> in Browse response")
	}
	return r, nil
}

// parseBrowseResponse extracts the items of a SOAP Browse response.
func parseBrowseResponse(data []byte) ([]Item, error) {
	r, err := readBrowseResponse(data)
	if err != nil {
		return nil, err
	}
	return parseDIDL([]byte(r.result))
}

// parseDIDL parses a DIDL-Lite document and returns Item list.
func parseDIDL(data []byte) ([]Item, error) {
	var doc struct {
		Containers []struct {
			ID    string `xml:"id,attr"`
			Title string `xml:"http://purl.org/dc/elements/1.1/ title"`
			Class string `xml:"urn:schemas-upnp-org:metadata-1-0/upnp/ class"`
		} `xml:"urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/ container"`
		Items []struct {
			ID      string `xml:"id,attr"`
			Title   string `xml:"http://purl.org/dc/elements/1.1/ title"`
			Class   string `xml:"urn:schemas-upnp-org:metadata-1-0/upnp/ class"`
			Artist  string `xml:"urn:schemas-upnp-org:metadata-1-0/upnp/ artist"`
			Album   string `xml:"urn:schemas-upnp-org:metadata-1-0/upnp/ album"`
			Creator string `xml:"http://purl.org/dc/elements/1.1/ creator"`
			Art     string `xml:"urn:schemas-upnp-org:metadata-1-0/upnp/ albumArtURI"`
			// An item carries one <res> per available representation (a transcoded
			// stream, a thumbnail, the original file). Binding a single string would
			// keep whichever happened to come last, so collect them all and pick.
			Res []struct {
				URL          string `xml:",chardata"`
				ProtocolInfo string `xml:"protocolInfo,attr"`
			} `xml:"urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/ res"`
		} `xml:"urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/ item"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("didl parse: %w", err)
	}
	out := make([]Item, 0, len(doc.Containers)+len(doc.Items))
	for _, c := range doc.Containers {
		out = append(out, Item{ID: clip(c.ID), Title: clip(c.Title), Class: clip(c.Class)})
	}
	for _, it := range doc.Items {
		urls := make([]resource, 0, len(it.Res))
		for _, res := range it.Res {
			urls = append(urls, resource{url: strings.TrimSpace(res.URL), protocolInfo: res.ProtocolInfo})
		}
		artist := strings.TrimSpace(it.Artist)
		if artist == "" {
			artist = strings.TrimSpace(it.Creator)
		}
		u := pickResource(urls)
		if len(u) > urlMax {
			continue // a truncated URL would play the wrong thing; skip the item
		}
		art := safeArt(strings.TrimSpace(it.Art))
		if len(art) > urlMax {
			art = ""
		}
		out = append(out, Item{ID: clip(it.ID), Title: clip(it.Title), Class: clip(it.Class),
			URL: strings.Clone(u), Artist: clip(artist), Album: clip(strings.TrimSpace(it.Album)), Art: strings.Clone(art)})
	}
	return out, nil
}

// clip cuts s to fieldMax bytes without splitting a UTF-8 sequence.
func clip(s string) string {
	// Always a copy: a short value trimmed out of a huge parsed string would keep all of
	// it reachable.
	n := fieldMax
	if len(s) <= n {
		return strings.Clone(s)
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.Clone(s[:n])
}

// resource is one <res> of a DIDL item.
type resource struct {
	url          string
	protocolInfo string
}

// pickResource chooses the resource to play. An http-get audio resource is preferred:
// the daemon hands the URL to ffmpeg, which speaks HTTP but not the RTP or
// internal-transport profiles some servers also advertise. Falls back to the first
// usable URL so an item with an unrecognised protocolInfo is still playable.
func pickResource(res []resource) string {
	for _, r := range res {
		if r.url == "" {
			continue
		}
		if strings.HasPrefix(r.protocolInfo, "http-get:") && strings.Contains(r.protocolInfo, ":audio/") {
			return r.url
		}
	}
	for _, r := range res {
		if strings.HasPrefix(r.url, "http://") || strings.HasPrefix(r.url, "https://") {
			return r.url
		}
	}
	return ""
}

const browseSOAPTmpl = `<?xml version="1.0" encoding="utf-8"?>` +
	`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
	`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
	`<s:Body><u:Browse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">` +
	`<ObjectID>%s</ObjectID>` +
	`<BrowseFlag>BrowseDirectChildren</BrowseFlag>` +
	`<Filter>*</Filter>` +
	`<StartingIndex>%d</StartingIndex>` +
	`<RequestedCount>%d</RequestedCount>` +
	`<SortCriteria></SortCriteria>` +
	`</u:Browse></s:Body></s:Envelope>`
