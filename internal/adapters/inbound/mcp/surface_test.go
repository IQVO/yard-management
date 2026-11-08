package mcp_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The agent surface is complete (AS-3): tools alone are half of it.
func TestResourceTemplatesAndPromptAreOffered(t *testing.T) {
	r := newRig(t)

	rts, err := r.session.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	got := map[string]bool{}
	for _, rt := range rts.ResourceTemplates {
		got[rt.URITemplate] = true
	}
	for _, want := range []string{"warehouse://yard/sites/{siteId}/board", "warehouse://yard/trailers/{trailerId}"} {
		if !got[want] {
			t.Errorf("resource template %s not offered (got %v)", want, got)
		}
	}

	prompts, err := r.session.ListPrompts(context.Background(), nil)
	if err != nil || len(prompts.Prompts) != 1 || prompts.Prompts[0].Name != "review_yard" {
		t.Fatalf("prompts = %+v, %v", prompts, err)
	}
	p, err := r.session.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "review_yard"})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	text := p.Messages[0].Content.(*sdk.TextContent).Text
	for _, must := range []string{"get_yard_board", "request_move", "idempotencyKey", "approvedBy", "floor-client"} {
		if !strings.Contains(text, must) {
			t.Errorf("the SOP never mentions %q", must)
		}
	}
	// The SOP names only tools that exist.
	for _, name := range toolNameRE.FindAllString(text, -1) {
		if _, ok := wantTools[name]; !ok {
			t.Errorf("the SOP mentions %q, which is not a tool of this server", name)
		}
	}
}

// toolNameRE finds the snake_case tool names a prompt mentions.
var toolNameRE = regexp.MustCompile(`\b(?:get|list|request|cancel|place|release|record|open)_[a-z_]+\b`)

func readResource(t *testing.T, r *rig, uri string) (string, error) {
	t.Helper()
	res, err := r.session.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: uri})
	if err != nil {
		return "", err
	}
	if len(res.Contents) != 1 || res.Contents[0].MIMEType != "application/json" {
		t.Fatalf("contents = %+v", res.Contents)
	}
	return res.Contents[0].Text, nil
}

func TestBoardResource(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	r.parkedTrailer("PLATE1", "T-100")

	text, err := readResource(t, r, "warehouse://yard/sites/WH1/board")
	if err != nil {
		t.Fatalf("read board: %v", err)
	}
	for _, must := range []string{`"siteId":"WH1"`, `"unitNumber":"T-100"`, `"WH1-D01"`} {
		if !strings.Contains(text, must) {
			t.Errorf("board resource lacks %s: %s", must, text)
		}
	}
	// The resource and the tool are the same read model.
	tool := r.ok("get_yard_board", map[string]any{"siteId": "WH1"}).Data
	if tool["siteId"] != "WH1" {
		t.Fatalf("tool board = %v", tool)
	}
}

func TestTrailerResource(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	id := r.parkedTrailer("PLATE1", "T-100")
	text, err := readResource(t, r, "warehouse://yard/trailers/"+id)
	if err != nil || !strings.Contains(text, `"trailerId":"`+id+`"`) {
		t.Fatalf("trailer resource = %q, %v", text, err)
	}
}

func TestResourcesRefuseBadURIs(t *testing.T) {
	r := newRig(t)
	r.seedSite()
	for _, uri := range []string{
		"warehouse://yard/sites//board",
		"warehouse://yard/sites/WH1/WH2/board",
		"warehouse://yard/sites/WH1",
		"warehouse://yard/trailers/",
		"warehouse://yard/trailers/a/b",
	} {
		if _, err := readResource(t, r, uri); err == nil {
			t.Errorf("%s was served", uri)
		}
	}
	if _, err := readResource(t, r, "warehouse://yard/trailers/trl-nope"); err == nil || !strings.Contains(err.Error(), "trailer-not-found") {
		t.Errorf("unknown trailer resource error = %v, want trailer-not-found", err)
	}
	if _, err := readResource(t, r, "warehouse://yard/sites/NOPE/board"); err == nil || !strings.Contains(err.Error(), "site-not-found") {
		t.Errorf("unknown site resource error = %v, want site-not-found", err)
	}
}
