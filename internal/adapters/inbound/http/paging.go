package http

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/claudioed/yard-management/internal/adapters/inbound/http/page"
	"github.com/claudioed/yard-management/internal/application/views"
)

// errUnknownQuery names the query parameter an operation does not declare
// (REST guideline 4.2: a retired paging parameter must fail loudly).
func errUnknownQuery(name string) error {
	return fmt.Errorf("%w: %q is not a parameter of this operation", errUnknownQueryParameter, name)
}

// listParams validates the query of a collection GET: no undeclared
// parameter (filters are the declared ones besides limit and cursor), a single
// integer limit in 1-500 and a decodable cursor. It returns the limit and the
// keyset position the page starts after (nil for the first page).
func listParams(r *http.Request, filters ...string) (int, *views.Key, error) {
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		return 0, nil, fmt.Errorf("%w: the query string is not valid", errMalformedRequest)
	}
	if name := page.Unknown(r, filters...); name != "" {
		return 0, nil, errUnknownQuery(name)
	}
	limit, err := page.Limit(r)
	if err != nil {
		return 0, nil, err
	}
	pos, err := page.After(r)
	if err != nil || pos == nil {
		return limit, nil, err
	}
	at, perr := time.Parse(time.RFC3339Nano, pos.Sort)
	if perr != nil {
		return 0, nil, page.ErrInvalidCursor
	}
	return limit, &views.Key{At: at.UTC(), ID: pos.ID}, nil
}

// pageOf turns a use case page into the wire page: the items (never null) and
// the opaque cursor of the last row when another page exists.
func pageOf[T any](p views.Page[T]) page.Page[T] {
	out := page.Page[T]{Items: p.Items}
	if out.Items == nil {
		out.Items = []T{}
	}
	if p.Next != nil {
		out.NextCursor = page.Encode(page.Position{Sort: p.Next.At.UTC().Format(time.RFC3339Nano), ID: p.Next.ID})
	}
	return out
}

// oneOf reports whether v is empty or one of the allowed values, so a filter
// outside the declared enumeration is a 400 rather than an empty page.
func oneOf(v string, allowed ...string) error {
	if v == "" {
		return nil
	}
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", errInvalidState, sanitizeLog(v))
}
