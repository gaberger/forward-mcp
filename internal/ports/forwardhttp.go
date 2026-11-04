package ports

import (
	"context"
	"net/url"
)

// RawRequest is one HTTP call to the Forward API, for endpoints the typed
// client does not model (Forward Predict is unpublished). Path is relative
// to the API host and starts with /api.
type RawRequest struct {
	Method string
	Path   string
	Query  url.Values
	// RawQuery is appended verbatim for bare flags the reference documents
	// without a value ("?count", "?counts", "?view=summary").
	RawQuery    string
	ContentType string // "" for no body
	Accept      string // "" → application/json
	Body        []byte
	// Idempotent marks calls that may be retried on transport errors.
	Idempotent bool
}

// RawResponse is the status and body; non-2xx statuses are returned, not
// turned into errors, so the caller can interpret them.
type RawResponse struct {
	Status      int
	ContentType string
	Body        []byte
}

// ForwardHTTP performs raw calls with the configured credentials and TLS
// settings. forward.Client implements it; a fixture adapter replays
// recorded exchanges for contract tests.
type ForwardHTTP interface {
	Do(ctx context.Context, req RawRequest) (RawResponse, error)
}
