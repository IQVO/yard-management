package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
)

// lhSaveFailingStore finds no stored answer and fails to store one; the error
// text carries a line break.
type lhSaveFailingStore struct{}

func (lhSaveFailingStore) Lookup(context.Context, string) (views.IdempotencyRecord, bool, error) {
	return views.IdempotencyRecord{}, false, nil
}

func (lhSaveFailingStore) Save(context.Context, views.IdempotencyRecord) error {
	return errors.New(lhForged)
}

type lhClock struct{}

func (lhClock) Now() time.Time { return time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC) }

// A failure to store an answer can carry request text (the key, the stored
// body); the error record must keep it on one line.
func TestLogHygiene_UnstoredAnswerIsLoggedOnOneLine(t *testing.T) {
	values := lhRecordLogs(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	c := &keyedCall{
		d:   IdempotencyDeps{Store: lhSaveFailingStore{}, Clock: lhClock{}},
		key: "key-1", fingerprint: "fp-1",
		r:    httptest.NewRequest(http.MethodPost, "/sweeps", nil),
		next: next,
	}
	c.servePlain(httptest.NewRecorder())
	lhAssertOneLine(t, *values, "an unstored idempotent answer")
}
