package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// maxBodyBytes bounds a request body; every body here is a few hundred bytes.
const maxBodyBytes = 64 << 10

type statusBody struct {
	Status string `json:"status"`
}

// problemDetail is the RFC 7807 application/problem+json body.
type problemDetail struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

// writeJSON encodes v as the response body with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON strictly decodes r's body into dest: unknown fields, trailing
// data and malformed JSON are errMalformedRequest. An empty body leaves dest
// untouched; required is whether that is an error.
func decodeJSON(r *http.Request, dest any, required bool) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		if errors.Is(err, io.EOF) && !required {
			return nil
		}
		return fmt.Errorf("%w: %w", errMalformedRequest, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: trailing data after the JSON body", errMalformedRequest)
	}
	return nil
}

// decodeRequiredJSON decodes a body the operation requires.
func decodeRequiredJSON(r *http.Request, dest any) error { return decodeJSON(r, dest, true) }

// decodeOptionalJSON decodes a body the operation may omit.
func decodeOptionalJSON(r *http.Request, dest any) error { return decodeJSON(r, dest, false) }

// outcomeRefusals lists the refusals that are an OUTCOME of the command, not
// a rejection of it: the command committed state (the use case returned the
// refusal after its unit of work committed) and the 4xx answer must be
// remembered under the Idempotency-Key. For a listed refusal writeError marks
// its writer (outcomeKeeper) and the Idempotency-Key middleware commits the
// handler's writes together with the key row, in one transaction, instead of
// rolling them back. Every other 4xx stays a rejection: rolled back, its
// answer stored on its own (ADR 0016, addendum of 2026-11-18). One list, to
// be extended only with an ADR.
//
//   - yardvisit.ErrCarrierSuspended: the gate recorded a REJECTED visit and its
//     VisitRejected event and then answered 409 carrier-suspended.
var outcomeRefusals = []error{
	yardvisit.ErrCarrierSuspended,
}

// outcomeKeeper is implemented by the middleware's capturing writer.
type outcomeKeeper interface{ KeepWrites() }

// isOutcomeRefusal reports whether err is one of the outcomeRefusals.
func isOutcomeRefusal(err error) bool {
	for _, target := range outcomeRefusals {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// writeError writes err as its RFC 7807 problem. An unmapped error is a 500
// whose detail never leaks the internal message (it is logged instead).
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if k, ok := w.(outcomeKeeper); ok && isOutcomeRefusal(err) {
		k.KeepWrites()
	}
	p := problemFor(err)
	detail := err.Error()
	if p == internalProblem {
		slog.ErrorContext(r.Context(), "request failed", "error", err, "method", sanitizeLog(r.Method), "path", sanitizeLog(r.URL.Path))
		detail = "an unexpected error occurred"
	}
	writeProblem(w, r, p, detail)
}

// writeProblem writes p with an explicit detail.
func writeProblem(w http.ResponseWriter, r *http.Request, p problem, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(problemDetail{
		Type:     problemBaseURI + p.slug,
		Title:    p.title,
		Status:   p.status,
		Detail:   detail,
		Instance: r.URL.EscapedPath(),
	})
}

// writeVersioned answers 200 with the aggregate and a weak ETag of its version.
func writeVersioned(w http.ResponseWriter, v any, version int64) {
	w.Header().Set("ETag", etagOf(version))
	writeJSON(w, http.StatusOK, v)
}

// etagOf is the weak validator of an aggregate version, W/"3".
func etagOf(version int64) string { return `W/"` + strconv.FormatInt(version, 10) + `"` }

// versionFromETag reads the version out of an If-Match value: W/"3", "3" or 3.
// An empty value or "*" is 0 (unconditional); anything else that is not a
// positive integer is errInvalidIfMatch.
func versionFromETag(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" {
		return 0, nil
	}
	raw = strings.TrimPrefix(raw, "W/")
	raw = strings.Trim(raw, `"`)
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w: %q", errInvalidIfMatch, sanitizeLog(raw))
	}
	return n, nil
}
