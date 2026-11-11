package shared_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

func TestParseSealClass(t *testing.T) {
	for in, want := range map[string]shared.SealClass{"": "", "I": shared.SealClassIndicative, "S": shared.SealClassSecurity, "H": shared.SealClassHighSecurity} {
		got, err := shared.ParseSealClass(in)
		if err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"h", "X", "HS", " I", "1", "HIGH"} {
		if _, err := shared.ParseSealClass(bad); !errors.Is(err, shared.ErrInvalidSealClass) {
			t.Errorf("%q: err = %v, want ErrInvalidSealClass", bad, err)
		}
	}
}

func TestSealClassValid(t *testing.T) {
	if shared.SealClass("").Valid() || !shared.SealClassIndicative.Valid() || !shared.SealClassSecurity.Valid() || !shared.SealClassHighSecurity.Valid() || shared.SealClass("X").Valid() {
		t.Fatal("SealClass.Valid wrong")
	}
}

func TestSealClassSatisfies(t *testing.T) {
	const i, s, h = shared.SealClassIndicative, shared.SealClassSecurity, shared.SealClassHighSecurity
	cases := []struct {
		have, min shared.SealClass
		want      bool
	}{
		{"", "", true}, {i, "", true}, {h, "", true},
		{"", i, false}, {"", h, false},
		{i, i, true}, {i, s, false}, {i, h, false},
		{s, i, true}, {s, s, true}, {s, h, false},
		{h, i, true}, {h, s, true}, {h, h, true},
	}
	for _, c := range cases {
		if got := c.have.Satisfies(c.min); got != c.want {
			t.Errorf("%q satisfies %q = %v, want %v", c.have, c.min, got, c.want)
		}
	}
}

func TestRequireSealClass(t *testing.T) {
	if err := shared.RequireSealClass(shared.SealClassHighSecurity, shared.SealClassHighSecurity); err != nil {
		t.Fatal(err)
	}
	err := shared.RequireSealClass(shared.SealClassIndicative, shared.SealClassHighSecurity)
	if !errors.Is(err, shared.ErrSealClassTooLow) || !strings.Contains(err.Error(), "requires class H") || !strings.Contains(err.Error(), "class I") {
		t.Errorf("err = %v", err)
	}
	err = shared.RequireSealClass("", shared.SealClassSecurity)
	if !errors.Is(err, shared.ErrSealClassTooLow) || !strings.Contains(err.Error(), "has none") {
		t.Errorf("err = %v", err)
	}
	if errors.Is(err, shared.ErrValidation) {
		t.Error("too low is a conflict, not a validation failure")
	}
}
