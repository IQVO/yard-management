package mcp_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// update regenerates testdata/tool_registry.golden.json:
//
//	go test ./internal/adapters/inbound/mcp -run TestToolRegistryGolden -update
var update = flag.Bool("update", false, "rewrite the MCP tool registry golden file")

// wantTools is the curated tool set (docs/adr/0007) with the authority level
// each declares.
var wantTools = map[string]string{
	"get_yard_board": "L1", "list_trailers": "L1", "get_trailer": "L1", "list_moves": "L1",
	"list_visits": "L1", "get_visit": "L1", "get_yard_policy": "L1", "get_yard_dwell_report": "L1",
	"request_move": "L3", "cancel_move": "L3", "place_hold": "L3", "release_hold": "L3",
	"record_seal": "L3", "open_yard_check": "L3",
}

// maxTools is the tool budget. Raising it is a deliberate, reviewed act (an
// ADR 0007 amendment): TestToolSurface still pins the exact curated set.
const maxTools = 14

// floorClientPhrases are the actions of the gate clerk, the hostler and the
// dock operator. They are floor-client actions on REST and must never be tools
// (docs/adr/0007): a tool whose name carries one of them as a word sequence is
// refused. ("open_yard_check" is the supervisor's audit, not the gate check-in.)
var floorClientPhrases = []string{
	"check_in", "check_out", "claim", "complete", "start", "reject", "declare", "sweep", "observe", "close", "depart", "door_work",
}

func floorVerbViolations(names []string) []string {
	var out []string
	for _, name := range names {
		for _, phrase := range floorClientPhrases {
			if strings.Contains("_"+name+"_", "_"+phrase+"_") {
				out = append(out, name+": contains the floor-client action "+phrase+" (docs/adr/0007: those actions stay on REST)")
			}
		}
	}
	return out
}

var (
	toolNaming = regexp.MustCompile(`^[a-z]+(_[a-z]+)+$`)
	argNaming  = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
)

func toolsOf(t *testing.T, r *rig) []*sdk.Tool {
	t.Helper()
	res, err := r.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	sort.Slice(res.Tools, func(i, j int) bool { return res.Tools[i].Name < res.Tools[j].Name })
	return res.Tools
}

// The governance charter's mechanical gate: the advertised tool set is exactly
// the curated one, within the budget, snake_case verb_noun, described,
// annotated, tagged with ONE authority level that matches its read/write
// nature, and every argument is camelCase and documented.
func TestToolSurface(t *testing.T) {
	r := newRig(t)
	tools := toolsOf(t, r)
	if len(tools) > maxTools {
		t.Fatalf("advertised %d tools, over the budget of %d", len(tools), maxTools)
	}
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
		checkToolMetadata(t, tool)
		checkToolArguments(t, tool)
	}
	var want []string
	for n := range wantTools {
		want = append(want, n)
	}
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("advertised tools %v, want exactly %v", names, want)
	}
	for _, v := range floorVerbViolations(names) {
		t.Error(v)
	}
}

// The floor-client sensor must actually be able to fail.
func TestFloorClientVerbSensorFailsOnFloorNames(t *testing.T) {
	for _, phrase := range floorClientPhrases {
		if len(floorVerbViolations([]string{phrase + "_visit", "bulk_" + phrase + "_moves"})) < 2 {
			t.Errorf("floor-client tool name with %q was not flagged", phrase)
		}
	}
	var all []string
	for n := range wantTools {
		all = append(all, n)
	}
	if got := floorVerbViolations(all); len(got) != 0 {
		t.Errorf("curated tool names were flagged: %v", got)
	}
	if got := floorVerbViolations([]string{"get_checklist", "list_closures", "get_declaration"}); len(got) != 0 {
		t.Errorf("false positive on non-verb words: %v", got)
	}
}

func checkToolMetadata(t *testing.T, tool *sdk.Tool) {
	t.Helper()
	if !toolNaming.MatchString(tool.Name) {
		t.Errorf("tool %q is not snake_case verb_noun", tool.Name)
	}
	if tool.Description == "" {
		t.Errorf("tool %q has no description", tool.Name)
	}
	level, _ := tool.Meta["com.warehouse/authority"].(string)
	if level != wantTools[tool.Name] {
		t.Errorf("tool %q authority = %q, want %q", tool.Name, level, wantTools[tool.Name])
	}
	if tool.Annotations == nil {
		t.Errorf("tool %q has no annotations", tool.Name)
		return
	}
	// Read tools are read-only and L1; write tools are not read-only, are L3
	// and carry an idempotencyKey.
	switch level {
	case "L1":
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("L1 tool %q is not annotated read-only", tool.Name)
		}
	case "L3":
		if tool.Annotations.ReadOnlyHint {
			t.Errorf("L3 tool %q is annotated read-only", tool.Name)
		}
		if !tool.Annotations.IdempotentHint {
			t.Errorf("write tool %q is not annotated idempotent although it takes an idempotencyKey", tool.Name)
		}
	}
	if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
		t.Errorf("tool %q is not annotated closed-world", tool.Name)
	}
}

func checkToolArguments(t *testing.T, tool *sdk.Tool) {
	t.Helper()
	schema, isMap := tool.InputSchema.(map[string]any)
	if !isMap {
		t.Errorf("tool %q input schema = %T", tool.Name, tool.InputSchema)
		return
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		t.Errorf("tool %q has no arguments in its schema", tool.Name)
	}
	required := map[string]bool{}
	if req, ok := schema["required"].([]any); ok {
		for _, n := range req {
			required[n.(string)] = true
		}
	}
	for arg, p := range props {
		if !argNaming.MatchString(arg) {
			t.Errorf("tool %q argument %q is not camelCase", tool.Name, arg)
		}
		if d, _ := p.(map[string]any)["description"].(string); d == "" {
			t.Errorf("tool %q argument %q has no description", tool.Name, arg)
		}
	}
	if schema["additionalProperties"] != false {
		t.Errorf("tool %q accepts unknown arguments (additionalProperties = %v)", tool.Name, schema["additionalProperties"])
	}
	if wantTools[tool.Name] == "L3" {
		checkWriteArguments(t, tool.Name, props, required)
	}
}

// Every write tool requires the idempotencyKey and offers the three optional
// attribution arguments.
func checkWriteArguments(t *testing.T, name string, props map[string]any, required map[string]bool) {
	t.Helper()
	if _, ok := props["idempotencyKey"]; !ok || !required["idempotencyKey"] {
		t.Errorf("write tool %q does not require an idempotencyKey", name)
	}
	for _, opt := range []string{"proposalId", "approvedBy", "agentId"} {
		if _, ok := props[opt]; !ok {
			t.Errorf("write tool %q has no %s argument", name, opt)
		}
		if required[opt] {
			t.Errorf("write tool %q requires the optional attribution argument %s", name, opt)
		}
	}
}

// TestToolRegistryGolden pins the whole advertised registry (names,
// descriptions, annotations, authority _meta, input AND output schemas) byte
// for byte, so any change to what an MCP client sees shows up as a reviewed
// golden diff.
func TestToolRegistryGolden(t *testing.T) {
	r := newRig(t)
	got, err := json.MarshalIndent(toolsOf(t, r), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "tool_registry.golden.json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("the MCP tool registry drifted from %s; review the change and re-run with -update.\ngot:\n%s", path, got)
	}
}
