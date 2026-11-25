// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
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
		return nil, fmt.Errorf("browse: %w", ctx.Err())
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

		for _, item := range items {
			used += len(item.ID) + len(item.Title) + len(item.Class) + len(item.Artist) + len(item.Album) +
				len(item.Art) + len(item.URL)
			if used > browseBytesMax {
				over = true

				break
			}

			out = append(out, item)
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
		return nil, 0, fmt.Errorf("browse request: %w", err)
	}

	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SoapAction", `"`+cdServiceURN+`#Browse"`)

	resp, err := safehttp.Do(ctx, client, req)
	if err != nil {
		return nil, 0, fmt.Errorf("browse: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Read one byte past the cap so a document that hits the limit is reported as
	// oversized instead of silently truncated into an unparseable fragment.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, soapBodyMax+1))
	if err != nil {
		return nil, 0, fmt.Errorf("browse response: %w", err)
	}

	if len(respBody) > soapBodyMax {
		return nil, 0, fmt.Errorf("%w %d bytes", errBrowseTooLarge, soapBodyMax)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("%w %s", errBrowseHTTP, resp.Status)
	}

	page, err := readBrowseResponse(respBody)
	if err != nil {
		return nil, 0, err
	}

	items, err := parseDIDL([]byte(page.result))

	return items, page.total, err
}

var (
	// errBrowseTooLarge is a Browse response past soapBodyMax.
	errBrowseTooLarge = errors.New("Browse response exceeds")
	// errBrowseHTTP is a Browse answered with an HTTP status other than 200.
	errBrowseHTTP = errors.New("Browse returned HTTP")
	// errNoResult is a Browse response without its Result element.
	errNoResult = errors.New("no <Result> in Browse response")
)

type browseResponse struct {
	result string
	total  int
}

// readBrowseResponse reads Result (the DIDL) and TotalMatches out of a SOAP Browse
// response with an XML decoder, so prefixes, namespace declarations and other attributes
// on either element are fine.
func readBrowseResponse(data []byte) (browseResponse, error) {
	var page browseResponse

	found := false
	decoder := xml.NewDecoder(bytes.NewReader(data))
	// Servers declare all sorts of encodings for what is in practice UTF-8 or ASCII.
	decoder.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		switch start.Name.Local {
		case "Result":
			if decoder.DecodeElement(&page.result, &start) == nil {
				found = true
			}
		case "TotalMatches":
			_ = decoder.DecodeElement(&page.total, &start)
		default:
			// Every other element only frames these two.
		}
	}

	if !found {
		return page, errNoResult
	}

	return page, nil
}

// parseBrowseResponse extracts the items of a SOAP Browse response.
func parseBrowseResponse(data []byte) ([]Item, error) {
	page, err := readBrowseResponse(data)
	if err != nil {
		return nil, err
	}

	return parseDIDL([]byte(page.result))
}

// parseDIDL parses a DIDL-Lite document and returns Item list.
func parseDIDL(data []byte) ([]Item, error) {
	var doc didlDoc

	err := xml.Unmarshal(data, &doc)
	if err != nil {
		return nil, fmt.Errorf("didl parse: %w", err)
	}

	out := make([]Item, 0, len(doc.Containers)+len(doc.Items))
	for _, c := range doc.Containers {
		out = append(out, Item{
			ID: clip(c.ID), Title: clip(c.Title), Class: clip(c.Class), Artist: "", Album: "", Art: "", URL: "",
		})
	}

	for i := range doc.Items {
		item := &doc.Items[i]

		urls := make([]resource, 0, len(item.Res))
		for _, res := range item.Res {
			urls = append(urls, resource{url: strings.TrimSpace(res.URL), protocolInfo: res.ProtocolInfo})
		}

		artist := strings.TrimSpace(item.Artist)
		if artist == "" {
			artist = strings.TrimSpace(item.Creator)
		}

		resURL := pickResource(urls)
		if len(resURL) > urlMax {
			continue // a truncated URL would play the wrong thing; skip the item
		}

		art := safeArt(strings.TrimSpace(item.Art))
		if len(art) > urlMax {
			art = ""
		}

		out = append(out, Item{
			ID: clip(item.ID), Title: clip(item.Title), Class: clip(item.Class),
			URL: strings.Clone(resURL), Artist: clip(artist), Album: clip(strings.TrimSpace(item.Album)),
			Art: strings.Clone(art),
		})
	}

	return out, nil
}

// clip cuts value to fieldMax bytes without splitting a UTF-8 sequence.
func clip(value string) string {
	// Always a copy: a short value trimmed out of a huge parsed string would keep all of
	// it reachable.
	end := fieldMax
	if len(value) <= end {
		return strings.Clone(value)
	}

	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}

	return strings.Clone(value[:end])
}

// didlDoc is the part of a DIDL-Lite document parseDIDL reads.
type didlDoc struct {
	Containers []didlContainer `xml:"urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/ container"`
	Items      []didlItem      `xml:"urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/ item"`
}

// didlContainer is a DIDL-Lite container: a folder.
type didlContainer struct {
	ID    string `xml:"id,attr"`
	Title string `xml:"http://purl.org/dc/elements/1.1/ title"`
	Class string `xml:"urn:schemas-upnp-org:metadata-1-0/upnp/ class"`
}

// didlItem is a DIDL-Lite item: a track.
type didlItem struct {
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
	Res []didlRes `xml:"urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/ res"`
}

// didlRes is one <res> of a DIDL-Lite item.
type didlRes struct {
	URL          string `xml:",chardata"`
	ProtocolInfo string `xml:"protocolInfo,attr"`
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
	for _, candidate := range res {
		if candidate.url == "" {
			continue
		}

		info := candidate.protocolInfo
		if strings.HasPrefix(info, "http-get:") && strings.Contains(info, ":audio/") {
			return candidate.url
		}
	}

	for _, candidate := range res {
		if strings.HasPrefix(candidate.url, "http://") || strings.HasPrefix(candidate.url, "https://") {
			return candidate.url
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
