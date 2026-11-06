package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
)

// pageOut is the ONLY collection answer of a list tool: the items (never
// null) and the opaque cursor of the next page, absent on the last one. It is
// the shape of the REST pages ({"items": [...], "nextCursor": "..."}).
type pageOut[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// cursorPosition is the keyset a cursor points after. The encoding is the
// fleet's (base64url of {"s": sort instant, "i": id}), so a cursor read from
// a REST page also works here and the other way round.
type cursorPosition struct {
	Sort string `json:"s"`
	ID   string `json:"i"`
}

func encodeCursor(k views.Key) string {
	b, _ := json.Marshal(cursorPosition{Sort: k.At.UTC().Format(time.RFC3339Nano), ID: k.ID}) // two strings cannot fail
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor parses an opaque cursor; "" is the first page (nil).
func decodeCursor(s string) (*views.Key, error) {
	if s == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, errInvalidCursor
	}
	var p cursorPosition
	if err := json.Unmarshal(b, &p); err != nil || p.ID == "" {
		return nil, errInvalidCursor
	}
	at, err := time.Parse(time.RFC3339Nano, p.Sort)
	if err != nil {
		return nil, errInvalidCursor
	}
	return &views.Key{At: at.UTC(), ID: p.ID}, nil
}

// pageParams validates the limit (omitted or 0 means the default 100; 1..500
// otherwise) and the cursor of a list call.
func pageParams(limit int, cursor string) (int, *views.Key, error) {
	if limit < 0 || limit > usecases.MaxPageSize {
		return 0, nil, errInvalidLimit
	}
	if limit == 0 {
		limit = usecases.DefaultPageSize
	}
	after, err := decodeCursor(cursor)
	if err != nil {
		return 0, nil, err
	}
	return limit, after, nil
}

// pageOf turns a use case page into the answer page.
func pageOf[T any](p views.Page[T]) pageOut[T] {
	out := pageOut[T]{Items: p.Items}
	if out.Items == nil {
		out.Items = []T{}
	}
	if p.Next != nil {
		out.NextCursor = encodeCursor(*p.Next)
	}
	return out
}

// oneOf reports whether v is empty or one of the allowed values, so a filter
// outside the declared enumeration fails loudly instead of returning an empty
// page (model input is untrusted: never default an unknown value).
func oneOf(v string, allowed ...string) error {
	if v == "" {
		return nil
	}
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("%w: %q, want one of %s", errInvalidState, v, strings.Join(allowed, ", "))
}
