// Package system holds the production implementations of the two ambient
// ports every service needs: the wall clock and the id generator. They live
// outside the Postgres adapter because they touch no database; the shipped
// binaries wire them (the in-memory test doubles are never imported by cmd/).
package system

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// IDGen is the production ports.IDGen: prefixed, unique ids
// (`<prefix>-<uuid>`), safe across processes and restarts.
type IDGen struct{}

// NewIDGen returns the production id generator.
func NewIDGen() *IDGen { return &IDGen{} }

// New implements ports.IDGen.
func (IDGen) New(prefix string) string {
	return fmt.Sprintf("%s-%s", strings.ToLower(prefix), uuid.NewString())
}

// Clock is the production ports.Clock: the wall clock, UTC.
type Clock struct{}

// NewClock returns the production clock.
func NewClock() *Clock { return &Clock{} }

// Now implements ports.Clock.
func (Clock) Now() time.Time { return time.Now().UTC() }
