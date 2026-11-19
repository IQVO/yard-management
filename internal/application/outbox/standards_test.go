package outbox

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

var instant = time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)

func header(entity, name string) shared.Header {
	return shared.NewHeader(entity, name, "subj-1", instant)
}

// dataOf returns the `data` member of a payload envelope.
func dataOf(t *testing.T, payload []byte) string {
	t.Helper()
	var env struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatal(err)
	}
	return string(env.Data)
}

func TestVariantOfReadsTheDecidingFieldFromTheWireData(t *testing.T) {
	cases := []struct {
		name, event, data, want string
	}{
		{"an inspection outcome", "InspectionCompleted", `{"outcome":"PASSED","gate":"PRE_DOCK"}`, "PASSED"},
		{"a hold reason", "TrailerHeld", `{"reason":"SEAL_BROKEN","version":3}`, "SEAL_BROKEN"},
		{"an event decided as a whole", "TrailerDeparted", `{"reason":"DAMAGED"}`, ""},
		{"the field is absent", "TrailerHeld", `{"version":3}`, ""},
		{"the field is not a string", "TrailerHeld", `{"reason":3}`, ""},
		{"the field is null", "TrailerHeld", `{"reason":null}`, ""},
		{"the data is not an object", "TrailerHeld", `[1]`, ""},
		{"the data is not JSON", "InspectionCompleted", `{`, ""},
	}
	for _, c := range cases {
		if got := variantOf(c.event, []byte(c.data)); got != c.want {
			t.Errorf("%s: variantOf(%s, %s) = %q, want %q", c.name, c.event, c.data, got, c.want)
		}
	}
}

// attributedLike is the shape of the MCP attribution (internal/adapters/inbound/
// mcp): an event that marshals through a map and adds `initiated_by`, so its Go
// type is not the domain's and its keys are sorted. The standard meaning must not
// depend on which of the two an event is.
type attributedLike struct{ shared.Event }

func (a attributedLike) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(a.Event)
	if err != nil {
		return nil, err
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	data["initiated_by"] = json.RawMessage(`{"via":"mcp"}`)
	return json.Marshal(data)
}

func TestAnAttributedEventMeansTheSameAsTheBareOne(t *testing.T) {
	events := []shared.Event{
		inspection.InspectionCompleted{Header: header("inspection", "InspectionCompleted"), InspectionID: "i1", Outcome: "FAILED"},
		trailer.TrailerHeld{Header: header("trailer", "TrailerHeld"), TrailerID: "t1", SiteID: "WH1", Reason: trailer.HoldDamaged, Version: 3},
		trailer.TrailerDeparted{Header: header("trailer", "TrailerDeparted"), TrailerID: "t1", SiteID: "WH1"},
	}
	for _, e := range events {
		bare, err := Payload(e)
		if err != nil {
			t.Fatal(err)
		}
		wrapped, err := Payload(attributedLike{Event: e})
		if err != nil {
			t.Fatal(err)
		}
		var b, w map[string]any
		if err := json.Unmarshal([]byte(dataOf(t, bare)), &b); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(dataOf(t, wrapped)), &w); err != nil {
			t.Fatal(err)
		}
		delete(w, "initiated_by")
		if !reflect.DeepEqual(b, w) {
			t.Errorf("%s: the attributed payload differs from the bare one:\n bare:       %v\n attributed: %v", e.EventName(), b, w)
		}
		if !strings.Contains(dataOf(t, wrapped), `"initiated_by"`) {
			t.Errorf("%s: the attribution is lost: %s", e.EventName(), dataOf(t, wrapped))
		}
		if !strings.Contains(dataOf(t, bare), `"cbv_`) && !strings.Contains(dataOf(t, bare), `"x12_`) {
			t.Errorf("%s: the bare event carries no standard field: %s", e.EventName(), dataOf(t, bare))
		}
	}
}

// A mapped fact carries the fields AFTER every existing field, and the existing
// bytes are the ones json.Marshal of the event wrote.
func TestPayloadAppendsTheStandardFieldsAfterTheExistingOnes(t *testing.T) {
	e := yardvisit.VisitCheckedOut{
		Header: header("yardvisit", "VisitCheckedOut"), VisitID: "v1", SiteID: "WH1", TrailersOut: []string{}, DepartedAt: shared.Stamp(instant), OnSiteMinutes: 5, Version: 2,
	}
	plain, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := Payload(e)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSuffix(string(plain), "}") + `,"x12_status":"OA","cbv_biz_step":"urn:epcglobal:cbv:bizstep:departing"}`
	if got := dataOf(t, payload); got != want {
		t.Fatalf("data = %s\nwant   %s", got, want)
	}
}

// A fact with no honest mapping is byte-for-byte what it was before ADR 0018:
// the payload is exactly the envelope of json.Marshal of the event. A held trailer
// whose reason maps to nothing is such a fact, and so is every other event the
// table decides to leave alone.
func TestPayloadOfAnUnmappedFactIsTheLegacyBytes(t *testing.T) {
	events := []shared.Event{
		trailer.TrailerHeld{Header: header("trailer", "TrailerHeld"), TrailerID: "t1", SiteID: "WH1", Reason: trailer.HoldQuality, Version: 3},
		trailer.TrailerHeld{Header: header("trailer", "TrailerHeld"), TrailerID: "t1", SiteID: "WH1", Reason: trailer.HoldNotFound, Version: 3},
		trailer.TrailerDocked{Header: header("trailer", "TrailerDocked"), TrailerID: "t1", SiteID: "WH1", DoorCode: "D1", DockedAt: shared.Stamp(instant), Version: 2},
		trailer.DoorWorkCompleted{Header: header("trailer", "DoorWorkCompleted"), TrailerID: "t1", SiteID: "WH1", DoorCode: "D1", LoadState: shared.LoadEmpty, Version: 2},
		yardvisit.VisitRejected{Header: header("yardvisit", "VisitRejected"), VisitID: "v1", SiteID: "WH1", Reason: yardvisit.ReasonUnknownAppointment},
		yardcheck.YardCheckClosed{Header: header("yardcheck", "YardCheckClosed")},
	}
	for _, e := range events {
		plain, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := Payload(e)
		if err != nil {
			t.Fatal(err)
		}
		if got := dataOf(t, payload); got != string(plain) {
			t.Errorf("%s: data = %s, want the unchanged %s", e.EventName(), got, plain)
		}
		for _, field := range []string{"x12_status", "cbv_biz_step", "cbv_disposition"} {
			if strings.Contains(string(payload), field) {
				t.Errorf("%s: carries %s though the table maps nothing", e.EventName(), field)
			}
		}
	}
}

func TestPayloadOfAHeldTrailerCarriesItsDisposition(t *testing.T) {
	for reason, disposition := range map[trailer.HoldReason]string{
		trailer.HoldDamaged:    "urn:epcglobal:cbv:disp:damaged",
		trailer.HoldSealBroken: "urn:epcglobal:cbv:disp:container_open",
	} {
		e := trailer.TrailerHeld{Header: header("trailer", "TrailerHeld"), TrailerID: "t1", SiteID: "WH1", Reason: reason, Version: 3}
		payload, err := Payload(e)
		if err != nil {
			t.Fatal(err)
		}
		want := `{"trailer_id":"t1","site_id":"WH1","reason":"` + string(reason) + `","version":3,"cbv_disposition":"` + disposition + `"}`
		if got := dataOf(t, payload); got != want {
			t.Errorf("%s: data = %s, want %s", reason, got, want)
		}
	}
}

func TestWithStandardsRefusesDataThatIsNotAnObject(t *testing.T) {
	e := yardvisit.VisitCheckedIn{Header: header("yardvisit", "VisitCheckedIn")}
	for _, bad := range []string{``, `[]`, `null`, `{`, `"x"`, `{"a":1`} {
		if out, err := withStandards(e, []byte(bad)); err == nil {
			t.Errorf("withStandards(%q) = %s, want an error", bad, out)
		}
	}
	// A fact the table leaves alone is returned untouched, whatever the bytes are:
	// there is nothing to append, so nothing to validate.
	if out, err := withStandards(yardcheck.YardCheckClosed{Header: header("yardcheck", "YardCheckClosed")}, []byte(`not json`)); err != nil || string(out) != "not json" {
		t.Errorf("an unmapped fact must pass through unchanged, got %q, %v", out, err)
	}
}

func TestWithStandardsOnAnEmptyObjectAddsNoLeadingComma(t *testing.T) {
	out, err := withStandards(yardvisit.VisitCheckedIn{Header: header("yardvisit", "VisitCheckedIn")}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"x12_status":"I1","cbv_biz_step":"urn:epcglobal:cbv:bizstep:arriving"}`; string(out) != want {
		t.Errorf("got %s, want %s", out, want)
	}
}
