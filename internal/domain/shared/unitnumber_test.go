package shared_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Valid ISO 6346 numbers. CSQU3054383 is the worked example of the standard.
// The others were derived with an independent implementation and checked by
// hand: MSCU000000 is 24 + 30*2 + 13*4 + 32*8 = 392 = 35*11 + 7; ABCU000007
// is 10 + 12*2 + 13*4 + 32*8 + 7*512 = 3926 = 356*11 + 10, so the remainder
// 10 is written 0; CMAU000001 sums to 869 = 79*11, remainder 0.
var validISO = []string{
	"CSQU3054383", "MSCU0000007", "TEMU0000006", "ABCU0000070", "CMAU0000010",
	"MSKU0070702", "ABCU1234560", "MAEU1234567", "HLXU1234561", "TRLZ1200046", "ZZZU9999992",
}

func TestISO6346ValidNumbers(t *testing.T) {
	for _, n := range validISO {
		u, err := shared.NewUnitNumber(n, shared.KindContainer, "")
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		if u.String() != n || u.Scheme() != shared.SchemeISO6346 {
			t.Errorf("%s: got %q scheme %q", n, u.String(), u.Scheme())
		}
	}
}

func TestISO6346CheckDigit(t *testing.T) {
	for _, n := range validISO {
		got, ok := shared.ISO6346CheckDigit(n[:10])
		if !ok || got != int(n[10]-'0') {
			t.Errorf("%s: check digit = %d ok=%v", n, got, ok)
		}
	}
	for _, body := range []string{"", "CSQU30543", "CSQU30543833", "csqu305438", "CSQX305438", "CSQU30543A", "CSQ1305438", "CSQU3054 8", "CSQ\xc3\x9c305438"} {
		if _, ok := shared.ISO6346CheckDigit(body); ok {
			t.Errorf("%q must not have a check digit", body)
		}
	}
}

// Every letter value, against the table of the standard (no multiples of 11).
func TestISO6346LetterValues(t *testing.T) {
	want := []int{10, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 34, 35, 36, 37, 38}
	for i, w := range want {
		// body "A" + "AA" + "U" + 000000: only the first letter varies; its
		// weight is 1, so its value is the sum minus the rest.
		letter := string(rune('A' + i))
		body := letter + "AAU000000"
		rest := 10*2 + 10*4 + 32*8 // A at 1, A at 2, U at 3
		got, ok := shared.ISO6346CheckDigit(body)
		if !ok || got != (w+rest)%11%10 {
			t.Errorf("letter %s: check digit %d, want %d", letter, got, (w+rest)%11%10)
		}
	}
}

func TestISO6346WrongCheckDigitNamesTheExpectedOne(t *testing.T) {
	for _, bad := range []string{"CSQU3054380", "CSQU3054381", "CSQU3054384", "CSQU3054389", "ABCU0000071", "ABCU0000079"} {
		_, err := shared.NewUnitNumber(bad, shared.KindContainer, "")
		if !errors.Is(err, shared.ErrInvalidUnitNumber) {
			t.Fatalf("%s: err = %v, want ErrInvalidUnitNumber", bad, err)
		}
		if errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: an invalid unit number is its own error, not ErrValidation", bad)
		}
	}
	_, err := shared.NewUnitNumber("CSQU3054380", shared.KindContainer, "")
	if !strings.Contains(err.Error(), "expected check digit is 3") || !strings.Contains(err.Error(), "check digit 0") {
		t.Errorf("detail = %q", err)
	}
	_, err = shared.NewUnitNumber("ABCU0000071", shared.KindContainer, "")
	if !strings.Contains(err.Error(), "expected check digit is 0") {
		t.Errorf("a remainder of 10 expects 0: %q", err)
	}
}

func TestISO6346MalformedNumbers(t *testing.T) {
	for _, bad := range []string{
		"CSQU305438", "CSQU30543833", "csqu3054383", "CSQX3054383", "CSQU30543A3", "CSQ13054383", "CSQU 305438 3", "TR-1042", "12345678901", "CSQU-3054383",
	} {
		if _, err := shared.NewUnitNumber(bad, shared.KindContainer, ""); !errors.Is(err, shared.ErrInvalidUnitNumber) {
			t.Errorf("%q: err = %v, want ErrInvalidUnitNumber", bad, err)
		}
	}
	_, err := shared.NewUnitNumber("CSQU305438", shared.KindContainer, "")
	if !strings.Contains(err.Error(), "no check digit") || !strings.Contains(err.Error(), "expected check digit is 3") {
		t.Errorf("a ten character number names the digit it lacks: %q", err)
	}
	_, err = shared.NewUnitNumber("TR-1042", shared.KindContainer, "")
	if !strings.Contains(err.Error(), "owner code") {
		t.Errorf("a malformed number says what the form is: %q", err)
	}
}

func TestUnitNumberSchemeSelection(t *testing.T) {
	cases := []struct {
		name     string
		number   string
		kind     shared.TrailerKind
		declared shared.UnitScheme
		scheme   shared.UnitScheme
		wantErr  error
	}{
		{"container checks ISO", "TR-1042", shared.KindContainer, "", "", shared.ErrInvalidUnitNumber},
		{"container valid", "CSQU3054383", shared.KindContainer, "", shared.SchemeISO6346, nil},
		{"dry van is free-form", "TR-1042", shared.KindDryVan, "", "", nil},
		{"reefer is free-form", "any text 7", shared.KindReefer, "", "", nil},
		{"flatbed is free-form", "FB 3", shared.KindFlatbed, "", "", nil},
		{"declared ISO on a dry van checks", "TR-1042", shared.KindDryVan, shared.SchemeISO6346, "", shared.ErrInvalidUnitNumber},
		{"declared ISO on a dry van, valid", "CSQU3054383", shared.KindDryVan, shared.SchemeISO6346, shared.SchemeISO6346, nil},
		{"declared ISO on a container", "CSQU3054383", shared.KindContainer, shared.SchemeISO6346, shared.SchemeISO6346, nil},
		{"unknown scheme", "TR-1042", shared.KindDryVan, "GS1", "", shared.ErrValidation},
		{"blank number", "  ", shared.KindDryVan, "", "", shared.ErrValidation},
		{"empty number", "", shared.KindContainer, "", "", shared.ErrValidation},
		{"too long", strings.Repeat("A", 65), shared.KindDryVan, "", "", shared.ErrValidation},
	}
	for _, c := range cases {
		u, err := shared.NewUnitNumber(c.number, c.kind, c.declared)
		if c.wantErr != nil {
			if !errors.Is(err, c.wantErr) {
				t.Errorf("%s: err = %v, want %v", c.name, err, c.wantErr)
			}
			continue
		}
		if err != nil || u.String() != c.number || u.Scheme() != c.scheme {
			t.Errorf("%s: got %q/%q err %v", c.name, u.String(), u.Scheme(), err)
		}
	}
}

func TestUnitSchemeValid(t *testing.T) {
	if !shared.UnitScheme("").Valid() || !shared.SchemeISO6346.Valid() || shared.UnitScheme("iso6346").Valid() {
		t.Fatal("UnitScheme.Valid wrong")
	}
}
