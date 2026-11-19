package http

import (
	"bytes"
	"net/http"
)

// capture records a handler's response so it can be stored and committed
// BEFORE anything reaches the client.
type capture struct {
	header http.Header
	code   int
	body   bytes.Buffer
	// class is the replay class of the problem the handler answered, set by
	// writeError (MarkReplay); replayUnset for a success or an unclassified answer.
	class replayClass
}

func newCapture() *capture { return &capture{header: http.Header{}} }

// MarkReplay records the class of the problem about to be written: the
// middleware decides from it whether the answer is stored, committed with the
// handler's writes (an OUTCOME) or released (a RETRYABLE refusal).
func (c *capture) MarkReplay(class replayClass) { c.class = class }

func (c *capture) Header() http.Header { return c.header }

func (c *capture) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func (c *capture) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.body.Write(b)
}

func (c *capture) status() int {
	if c.code == 0 {
		return http.StatusOK
	}
	return c.code
}

// storable reports whether the answer is remembered under its key: a success,
// and a refusal whose class says so (FINAL, OUTCOME). A RETRYABLE refusal, an
// unclassified 4xx and a 5xx are not (the same decision as verdict).
func (c *capture) storable() bool { return verdict(c) != errNotStored }

// flush copies the captured response onto the real client connection.
func (c *capture) flush(w http.ResponseWriter) {
	for k, vs := range c.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(c.status())
	_, _ = w.Write(c.body.Bytes())
}
