// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package apierror writes the HTTP API's one error shape (ADR 0091), for internal/web and
// its lanes: {"error": "<message for a person>", "code": "<stable code for a program>"}.
// The message is English and may change in any release; the code does not, so a client,
// the UI among them, reacts to the code and says it in its own language.
package apierror

import (
	"encoding/json"
	"net/http"
)

// Code names what went wrong, independent of the message's wording. The list is open: a
// client treats a code it does not know as the generic failure its status says.
type Code string

const (
	// InvalidRequest is a request the speaker will not take as sent: a malformed body,
	// a missing or out-of-range field.
	InvalidRequest Code = "invalid_request"
	// NotFound is a route, item or device the speaker does not know.
	NotFound Code = "not_found"
	// MethodNotAllowed is a method the route does not take.
	MethodNotAllowed Code = "method_not_allowed"
	// Forbidden is a request refused for where it came from (host, origin, address).
	Forbidden Code = "forbidden"
	// SignatureInvalid is an upload without the signature it needs, or with one that
	// does not verify.
	SignatureInvalid Code = "signature_invalid"
	// ConfirmRequired is a request that needs an explicit confirmation it did not carry
	// (X-SoundTap-Confirm for an install, a downgrade or a return to stock).
	ConfirmRequired Code = "confirm_required"
	// NotConfirmed is a request held for a key held on the speaker that did not come.
	NotConfirmed Code = "not_confirmed"
	// ConfirmSuperseded is a request that waited for a key held on the speaker until a
	// newer one took its place.
	ConfirmSuperseded Code = "confirm_superseded"
	// Busy is a request refused because another operation (an install, a restore, an
	// upload) is still running.
	Busy Code = "busy"
	// Conflict is a request the speaker's current state does not allow.
	Conflict Code = "conflict"
	// Timeout is a request that ran out of time, on either side.
	Timeout Code = "timeout"
	// TooLarge is a body over the route's limit.
	TooLarge Code = "too_large"
	// PackageRejected is an upload that is not a valid package.
	PackageRejected Code = "package_rejected"
	// PreconditionFailed is a request this speaker cannot carry out as it is now (an
	// install on an unknown model or partition layout).
	PreconditionFailed Code = "precondition_failed"
	// Unsupported is something this build or model cannot do.
	Unsupported Code = "unsupported"
	// Unavailable is a part of the speaker that cannot answer right now.
	Unavailable Code = "unavailable"
	// ClockNotSet is a request that needs an outside service over TLS (the update check)
	// before NTP has set the speaker's clock since boot; it can be asked again in a minute.
	ClockNotSet Code = "clock_not_set"
	// UpstreamFailed is another speaker or an outside service that failed or did not answer.
	UpstreamFailed Code = "upstream_failed"
	// StorageFull is a write that failed because the speaker's storage is full.
	StorageFull Code = "storage_full"
	// Internal is any other failure on the speaker's side.
	Internal Code = "internal"
)

// Codes is every code, in the order openapi.json lists them.
func Codes() []Code {
	return []Code{
		InvalidRequest, NotFound, MethodNotAllowed, Forbidden, SignatureInvalid,
		ConfirmRequired, NotConfirmed, ConfirmSuperseded, Busy, Conflict, Timeout, TooLarge,
		PackageRejected, PreconditionFailed, Unsupported, Unavailable, ClockNotSet, UpstreamFailed,
		StorageFull, Internal,
	}
}

// Body is the error shape on the wire.
type Body struct {
	Error string `json:"error"`
	Code  Code   `json:"code"`
}

// The status ranges ForStatus tells apart.
const (
	firstClientError = 400
	firstServerError = 500
)

// ForStatus is the code an error with this status has unless its call site names a more
// precise one.
func ForStatus(status int) Code {
	if status >= firstClientError && status < firstServerError {
		return forClientError(status)
	}

	return forServerError(status)
}

// forClientError is ForStatus for a 4xx status.
func forClientError(status int) Code {
	switch status {
	case http.StatusNotFound:
		return NotFound
	case http.StatusMethodNotAllowed:
		return MethodNotAllowed
	case http.StatusForbidden:
		return Forbidden
	case http.StatusPreconditionRequired:
		return ConfirmRequired
	case http.StatusConflict:
		return Conflict
	case http.StatusRequestTimeout:
		return Timeout
	case http.StatusRequestEntityTooLarge:
		return TooLarge
	case http.StatusUnsupportedMediaType:
		return PackageRejected
	case http.StatusPreconditionFailed:
		return PreconditionFailed
	default:
		return InvalidRequest
	}
}

// forServerError is ForStatus for any status outside 4xx.
func forServerError(status int) Code {
	switch status {
	case http.StatusGatewayTimeout:
		return Timeout
	case http.StatusNotImplemented:
		return Unsupported
	case http.StatusServiceUnavailable:
		return Unavailable
	case http.StatusBadGateway:
		return UpstreamFailed
	case http.StatusInsufficientStorage:
		return StorageFull
	default:
		return Internal
	}
}

// Write answers with the error shape: status, code and a message for a person.
func Write(w http.ResponseWriter, status int, code Code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	body := Body{Error: msg, Code: code}
	_ = json.NewEncoder(w).Encode(body) //nolint:errchkjson // the client may be gone; nothing left to tell it
}
