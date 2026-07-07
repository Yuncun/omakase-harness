package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Yuncun/omakase-harness/internal/overlay"
	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/tui"
)

// lexists is `[ -e p ] || [ -L p ]` (copied from internal/overlay/init.go's
// test-visible twin — test helpers are not importable across packages).
func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// placedIndex finds rel's row in a placed.tsv read (copied from
// internal/overlay/toggle.go's test-visible twin, same reason as lexists).
func placedIndex(rows []state.PlacedRow, rel string) int {
	for i, r := range rows {
		if r.Rel == rel {
			return i
		}
	}
	return -1
}

// placedFixture builds a git repo with one placed rule file (AGENTS.md), two
// placed skill files under .claude/skills/ (a.md, b.md — >= 2 path separators,
// so internal/tui/items.go groups them under "dir:.claude/skills"), and a
// lefthook stub whose `dump` output declares one omakase pre-commit gate
// (smoke) — the minimal repo where LiveItems yields a toggleable file, a
// toggleable group, and a toggleable gate. It chdirs into the repo
// (status.Run resolves the repo from the working directory) and returns the
// dir plus discovered Repo.
func placedFixture(t *testing.T) (string, *state.Repo) {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"}, {"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "lefthook")
	dump := "pre-commit:\n  jobs:\n    - name: smoke\n      run: bash .omakase/bin/omakase-gate.sh smoke --step 'true'\n"
	script := "#!/bin/sh\nif [ \"$1\" = dump ]; then printf '%s' \"$LEFTHOOK_STUB_DUMP\"; fi\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEFTHOOK_BIN", stub)
	t.Setenv("LEFTHOOK_STUB_DUMP", dump)

	payload := t.TempDir()
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "AGENTS.md"), []byte("# agents\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills := filepath.Join(payload, ".claude", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "a.md"), []byte("# skill a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "b.md"), []byte("# skill b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMAKASE_PAYLOAD", payload)

	var stdout, stderr strings.Builder
	if code := overlay.RunInit(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("init exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	repo, err := state.Discover(dir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	return dir, repo
}

// connect builds a real client/server pair over in-memory transports against
// the fixture repo. A nil elicitation handler yields a client WITHOUT the
// elicitation capability (SDK advertises it only when the handler is set).
func connect(t *testing.T, root string, eh func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error)) *mcp.ClientSession {
	t.Helper()
	st, ct := mcp.NewInMemoryTransports()
	srv := NewServer(root)
	if _, err := srv.Connect(context.Background(), st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	opts := &mcp.ClientOptions{}
	if eh != nil {
		opts.ElicitationHandler = eh
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, opts)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// scriptedElicit runs a different step for each successive Elicit call — the
// chain flows (triage, preset, sections) answer form 1 differently from form
// 2, and a step can assert on what a later form's schema actually asked for
// (e.g. a chain's pending edits carried into the next question's defaults).
// Any call past the scripted steps fails the test loudly rather than hanging
// or silently declining, since an unscripted extra call means the flow chose
// to elicit again when it shouldn't have.
func scriptedElicit(t *testing.T, steps ...func(*testing.T, *mcp.ElicitRequest) *mcp.ElicitResult) func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	t.Helper()
	i := 0
	return func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		if i >= len(steps) {
			t.Fatalf("elicit called more times (%d) than scripted (%d)", i+1, len(steps))
		}
		step := steps[i]
		i++
		return step(t, req), nil
	}
}

// text flattens a tool result to its first text content for assertions.
func text(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("tool result has no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return tc.Text
}

// The status tool returns the plain status page for the fixture repo.
func TestStatusTool(t *testing.T) {
	dir, _ := placedFixture(t) // helper described above; chdirs into the repo like initRepo does
	cs := connect(t, dir, nil)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "status"})
	if err != nil {
		t.Fatalf("CallTool(status): %v", err)
	}
	if res.IsError {
		t.Fatalf("status IsError: %s", text(t, res))
	}
	out := text(t, res)
	if !strings.Contains(out, "zero footprint") || !strings.Contains(out, "AGENTS.md") {
		t.Errorf("status output missing expected lines:\n%s", out)
	}
}

// Both tools are listed, and menu carries the requiresUserInteraction marker
// so Claude Code never auto-answers the consent form.
func TestToolListAndMenuMeta(t *testing.T) {
	dir, _ := placedFixture(t)
	cs := connect(t, dir, nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}
	if byName["status"] == nil || byName["menu"] == nil {
		t.Fatalf("tools = %v, want status and menu", res.Tools)
	}
	if v, _ := byName["menu"].Meta["anthropic/requiresUserInteraction"].(bool); !v {
		t.Errorf("menu tool _meta missing anthropic/requiresUserInteraction=true: %v", byName["menu"].Meta)
	}
}

// Accepting the form with one file flipped off applies it: the file leaves
// the working tree, the ledger row goes enabled=0, and the summary names it.
func TestMenuAppliesFileOff(t *testing.T) {
	dir, repo := placedFixture(t)
	var sawSchema string
	eh := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		b, _ := json.Marshal(req.Params.RequestedSchema)
		sawSchema = string(b)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:AGENTS.md": rowDisabled}}, nil
	}
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"})
	if err != nil {
		t.Fatalf("CallTool(menu): %v", err)
	}
	if res.IsError {
		t.Fatalf("menu IsError: %s", text(t, res))
	}
	if !strings.Contains(sawSchema, `"file:AGENTS.md"`) || !strings.Contains(sawSchema, `"gate:smoke"`) {
		t.Errorf("elicited schema missing expected fields: %s", sawSchema)
	}
	if lexists(filepath.Join(dir, "AGENTS.md")) {
		t.Errorf("AGENTS.md still present after menu off")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if i := placedIndex(rows, "AGENTS.md"); i < 0 || rows[i].Enabled != "0" {
		t.Errorf("ledger not enabled=0 after menu off: %+v", rows)
	}
	if out := text(t, res); !strings.Contains(out, "AGENTS.md") || !strings.Contains(out, "off") {
		t.Errorf("summary does not name the change:\n%s", out)
	}
}

// Toggling the gate off lands in disabled-gates via the same backend as the
// screen and the --disable flag.
func TestMenuAppliesGateOff(t *testing.T) {
	dir, repo := placedFixture(t)
	eh := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"gate:smoke": rowDisabled}}, nil
	}
	cs := connect(t, dir, eh)
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"}); err != nil {
		t.Fatalf("CallTool(menu): %v", err)
	}
	if !overlay.DisabledGates(repo.OMK)["smoke"] {
		t.Errorf("gate smoke not in disabled-gates after menu off")
	}
}

// Declining changes nothing.
func TestMenuDecline(t *testing.T) {
	dir, repo := placedFixture(t)
	eh := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "decline"}, nil
	}
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"})
	if err != nil {
		t.Fatalf("CallTool(menu): %v", err)
	}
	if !lexists(filepath.Join(dir, "AGENTS.md")) {
		t.Errorf("decline still removed AGENTS.md")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if i := placedIndex(rows, "AGENTS.md"); i < 0 || rows[i].Enabled != "1" {
		t.Errorf("decline altered the ledger: %+v", rows)
	}
	if out := text(t, res); !strings.Contains(out, "no changes") {
		t.Errorf("decline summary = %q, want mention of no changes", out)
	}
}

// Submitting the form untouched (all defaults echoed back) changes nothing.
func TestMenuUntouchedSubmit(t *testing.T) {
	dir, _ := placedFixture(t)
	eh := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:AGENTS.md": rowEnabled, "gate:smoke": rowEnabled}}, nil
	}
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"})
	if err != nil {
		t.Fatalf("CallTool(menu): %v", err)
	}
	if !lexists(filepath.Join(dir, "AGENTS.md")) {
		t.Errorf("untouched submit removed AGENTS.md")
	}
	if out := text(t, res); !strings.Contains(out, "no changes") && !strings.Contains(out, "No changes") {
		t.Errorf("untouched-submit summary = %q", out)
	}
}

// A client that accepts with content omitted (nil, not even an empty
// object) must not crash the server. Upstream (go-sdk v1.6.1 ->
// jsonschema-go v0.4.3): a nil map[string]any passes the SDK's server-side
// object validation (a nil map still reports Kind() == Map) but then reaches
// ApplyDefaults, which assigns straight into that map to backfill declared
// defaults and panics with "assignment to entry in nil map" — inside
// req.Session.Elicit, before menuHandler ever sees a result. Regression for
// finding C1: this must be recovered and folded into the same graceful
// fallback as "client can't elicit at all", and the session must still be
// usable afterwards.
func TestMenuAcceptWithNilContentDoesNotCrash(t *testing.T) {
	dir, repo := placedFixture(t)
	eh := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept"}, nil // Content deliberately omitted (nil map)
	}
	cs := connect(t, dir, eh)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"})
	if err != nil {
		t.Fatalf("CallTool(menu) with nil-content accept killed the connection: %v", err)
	}
	if out := text(t, res); !strings.Contains(out, "--disable") {
		t.Errorf("nil-content accept did not fall back to the flag hint:\n%s", out)
	}

	if !lexists(filepath.Join(dir, "AGENTS.md")) {
		t.Errorf("AGENTS.md removed despite nil-content accept producing no ops")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if i := placedIndex(rows, "AGENTS.md"); i < 0 || rows[i].Enabled != "1" {
		t.Errorf("ledger disturbed by nil-content accept: %+v", rows)
	}

	// The session must survive the recovered panic: a further call on the
	// same connection has to succeed, not fail because the server (or the
	// process) died with it.
	statusRes, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "status"})
	if err != nil {
		t.Fatalf("CallTool(status) after nil-content accept: %v", err)
	}
	if statusRes.IsError {
		t.Fatalf("status IsError after nil-content accept: %s", text(t, statusRes))
	}
}

// An unrecognized variant behaves exactly like the no-args collapsed menu —
// Task 1 wires the dispatch switch before any of triage/preset/sections
// exist, so every variant value falls through to the same collapsed flow
// until Tasks 2-4 replace one case each.
func TestMenuUnknownVariantIsCollapsed(t *testing.T) {
	dir, _ := placedFixture(t)
	eh := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:AGENTS.md": rowEnabled, "gate:smoke": rowEnabled}}, nil
	}
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"variant": "banana"},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, variant=banana): %v", err)
	}
	if !lexists(filepath.Join(dir, "AGENTS.md")) {
		t.Errorf("unknown-variant submit removed AGENTS.md")
	}
	if out := text(t, res); !strings.Contains(out, "no changes") && !strings.Contains(out, "No changes") {
		t.Errorf("unknown-variant summary = %q", out)
	}
}

// A client with no elicitation capability gets the instructive fallback, not
// a protocol error.
func TestMenuWithoutElicitationCapability(t *testing.T) {
	dir, _ := placedFixture(t)
	cs := connect(t, dir, nil)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"})
	if err != nil {
		t.Fatalf("CallTool(menu): %v", err)
	}
	if out := text(t, res); !strings.Contains(out, "--disable") {
		t.Errorf("fallback text missing flag hint:\n%s", out)
	}
}

// Accepting the form with a whole group flipped off applies it to every
// child: both files leave the working tree, both ledger rows go enabled=0,
// and the summary names the group and the file count.
func TestMenuGroupOff(t *testing.T) {
	dir, repo := placedFixture(t)
	eh := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"dir:.claude/skills": rowDisabled}}, nil
	}
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"})
	if err != nil {
		t.Fatalf("CallTool(menu): %v", err)
	}
	if res.IsError {
		t.Fatalf("menu IsError: %s", text(t, res))
	}
	if lexists(filepath.Join(dir, ".claude", "skills", "a.md")) {
		t.Errorf("a.md still present after group off")
	}
	if lexists(filepath.Join(dir, ".claude", "skills", "b.md")) {
		t.Errorf("b.md still present after group off")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	for _, rel := range []string{".claude/skills/a.md", ".claude/skills/b.md"} {
		if i := placedIndex(rows, rel); i < 0 || rows[i].Enabled != "0" {
			t.Errorf("ledger not enabled=0 for %s after group off: %+v", rel, rows)
		}
	}
	if out := text(t, res); !strings.Contains(out, ".claude/skills/ → all off (2 files)") {
		t.Errorf("summary does not name the group change:\n%s", out)
	}
}

// A tracked child refuses to toggle (omakase never deletes committed files):
// the group op still applies to the untracked sibling, and the summary
// reports the partial refusal rather than aborting the whole batch.
func TestMenuGroupPartialRefusal(t *testing.T) {
	dir, repo := placedFixture(t)
	for _, args := range [][]string{
		{"-C", dir, "add", "-f", ".claude/skills/a.md"},
		{"-C", dir, "commit", "-q", "-m", "track a.md"},
	} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	eh := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"dir:.claude/skills": rowDisabled}}, nil
	}
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"})
	if err != nil {
		t.Fatalf("CallTool(menu): %v", err)
	}
	if res.IsError {
		t.Fatalf("menu IsError: %s", text(t, res))
	}
	if out := text(t, res); !strings.Contains(out, "1 of 2 refused") {
		t.Errorf("summary does not report the partial refusal:\n%s", out)
	}
	if !lexists(filepath.Join(dir, ".claude", "skills", "a.md")) {
		t.Errorf("tracked a.md was deleted despite the refusal")
	}
	if lexists(filepath.Join(dir, ".claude", "skills", "b.md")) {
		t.Errorf("untracked b.md still present after the group op")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if i := placedIndex(rows, ".claude/skills/a.md"); i < 0 || rows[i].Enabled != "1" {
		t.Errorf("refused a.md's ledger row changed: %+v", rows)
	}
	if i := placedIndex(rows, ".claude/skills/b.md"); i < 0 || rows[i].Enabled != "0" {
		t.Errorf("ledger not enabled=0 for b.md after group off: %+v", rows)
	}
}

// A partially-off group ("keep as-is" submitted unchanged) leaves the mixed
// state alone; submitting "all on" restores every child, including the ones
// already on.
func TestMenuGroupPartialRoundTrip(t *testing.T) {
	dir, repo := placedFixture(t)
	if err := overlay.FileOff(repo, ".claude/skills/b.md"); err != nil {
		t.Fatalf("arrange: FileOff(b.md): %v", err)
	}

	keep := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"dir:.claude/skills": "keep as-is"}}, nil
	}
	cs := connect(t, dir, keep)
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"}); err != nil {
		t.Fatalf("menu keep-as-is: %v", err)
	}
	if !lexists(filepath.Join(dir, ".claude", "skills", "a.md")) {
		t.Errorf("keep-as-is disturbed a.md")
	}
	if lexists(filepath.Join(dir, ".claude", "skills", "b.md")) {
		t.Errorf("keep-as-is restored b.md")
	}

	on := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"dir:.claude/skills": "all on"}}, nil
	}
	cs2 := connect(t, dir, on)
	if _, err := cs2.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"}); err != nil {
		t.Fatalf("menu all-on: %v", err)
	}
	if !lexists(filepath.Join(dir, ".claude", "skills", "a.md")) {
		t.Errorf("a.md missing after all-on")
	}
	if !lexists(filepath.Join(dir, ".claude", "skills", "b.md")) {
		t.Errorf("b.md not restored by all-on")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	for _, rel := range []string{".claude/skills/a.md", ".claude/skills/b.md"} {
		if i := placedIndex(rows, rel); i < 0 || rows[i].Enabled != "1" {
			t.Errorf("ledger not enabled=1 for %s after all-on: %+v", rel, rows)
		}
	}
}

// Menu round-trip: off then back on restores the file from the snapshot.
func TestMenuFileBackOn(t *testing.T) {
	dir, _ := placedFixture(t)
	off := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:AGENTS.md": rowDisabled}}, nil
	}
	cs := connect(t, dir, off)
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"}); err != nil {
		t.Fatalf("menu off: %v", err)
	}
	on := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:AGENTS.md": rowEnabled}}, nil
	}
	cs2 := connect(t, dir, on)
	if _, err := cs2.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"}); err != nil {
		t.Fatalf("menu on: %v", err)
	}
	if !lexists(filepath.Join(dir, "AGENTS.md")) {
		t.Errorf("AGENTS.md not restored by menu on")
	}
}

// The expand argument dissolves groups into per-file rows, and a single file
// inside a group can be toggled alone — the granularity the collapsed menu
// deliberately gives up.
func TestMenuExpandTogglesSingleGroupMember(t *testing.T) {
	dir, repo := placedFixture(t)
	var sawSchema string
	eh := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		b, _ := json.Marshal(req.Params.RequestedSchema)
		sawSchema = string(b)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:.claude/skills/b.md": rowDisabled}}, nil
	}
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"expand": true},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, expand): %v", err)
	}
	if res.IsError {
		t.Fatalf("menu IsError: %s", text(t, res))
	}
	for _, want := range []string{`"file:.claude/skills/a.md"`, `"file:.claude/skills/b.md"`} {
		if !strings.Contains(sawSchema, want) {
			t.Errorf("expanded schema missing %s:\n%s", want, sawSchema)
		}
	}
	if strings.Contains(sawSchema, `"dir:`) {
		t.Errorf("expanded schema still contains a group field:\n%s", sawSchema)
	}
	if lexists(filepath.Join(dir, ".claude/skills/b.md")) {
		t.Errorf("b.md still present after expanded menu off")
	}
	if !lexists(filepath.Join(dir, ".claude/skills/a.md")) {
		t.Errorf("a.md was removed — sibling must be untouched")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if i := placedIndex(rows, ".claude/skills/b.md"); i < 0 || rows[i].Enabled != "0" {
		t.Errorf("b.md ledger row not enabled=0: %+v", rows)
	}
	if i := placedIndex(rows, ".claude/skills/a.md"); i < 0 || rows[i].Enabled != "1" {
		t.Errorf("a.md ledger row changed: %+v", rows)
	}
}

// The triage variant's single-form path: turning on the one flagged row
// (the mixed group's off child) restores it without touching its on
// sibling, in one round trip (no open-full chain).
func TestMenuTriageSingleForm(t *testing.T) {
	dir, repo := placedFixture(t)
	if err := overlay.FileOff(repo, ".claude/skills/b.md"); err != nil {
		t.Fatalf("arrange: FileOff(b.md): %v", err)
	}
	eh := scriptedElicit(t, func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:.claude/skills/b.md": rowEnabled}}
	})
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"variant": "triage"},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, triage): %v", err)
	}
	if res.IsError {
		t.Fatalf("menu IsError: %s", text(t, res))
	}
	if !lexists(filepath.Join(dir, ".claude/skills/a.md")) {
		t.Errorf("a.md removed, want untouched")
	}
	if !lexists(filepath.Join(dir, ".claude/skills/b.md")) {
		t.Errorf("b.md not restored")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if i := placedIndex(rows, ".claude/skills/b.md"); i < 0 || rows[i].Enabled != "1" {
		t.Errorf("b.md ledger not enabled=1: %+v", rows)
	}
	if i := placedIndex(rows, ".claude/skills/a.md"); i < 0 || rows[i].Enabled != "1" {
		t.Errorf("a.md ledger changed: %+v", rows)
	}
}

// Declining the triage form applies nothing, per the transaction rule.
func TestMenuTriageDeclineAppliesNothing(t *testing.T) {
	dir, repo := placedFixture(t)
	if err := overlay.FileOff(repo, ".claude/skills/b.md"); err != nil {
		t.Fatalf("arrange: FileOff(b.md): %v", err)
	}
	eh := scriptedElicit(t, func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
		return &mcp.ElicitResult{Action: "decline"}
	})
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"variant": "triage"},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, triage): %v", err)
	}
	if out := text(t, res); out != "Menu closed — no changes made." {
		t.Errorf("decline output = %q, want exact closed text", out)
	}
	if lexists(filepath.Join(dir, ".claude/skills/b.md")) {
		t.Errorf("decline restored b.md")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if i := placedIndex(rows, ".claude/skills/b.md"); i < 0 || rows[i].Enabled != "0" {
		t.Errorf("decline altered the ledger: %+v", rows)
	}
}

// Asking to open the full list chains into a second, expanded form whose
// defaults already carry form 1's edits (ApplyOps pre-seeding) — accepting
// form 2 unchanged still applies form 1's b.md-on edit, proving the pending
// state, not the stale original, reached the second form.
func TestMenuTriageOpenFullChain(t *testing.T) {
	dir, repo := placedFixture(t)
	if err := overlay.FileOff(repo, ".claude/skills/b.md"); err != nil {
		t.Fatalf("arrange: FileOff(b.md): %v", err)
	}
	wantTitle := fmt.Sprintf("[%s] %s", stageShort(tui.StageOnDemand), ".claude/skills/b.md")
	eh := scriptedElicit(t,
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{
				keyOpenFull: true, "file:.claude/skills/b.md": rowEnabled,
			}}
		},
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			// The SDK re-serializes RequestedSchema through its own typed
			// jsonschema.Schema (a map internally), so property/key order isn't
			// preserved across the wire — assert on the decoded value instead
			// of a literal JSON substring.
			b, _ := json.Marshal(req.Params.RequestedSchema)
			var decoded struct {
				Properties map[string]struct {
					Title   string `json:"title"`
					Default any    `json:"default"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(b, &decoded); err != nil {
				t.Fatalf("decode second form schema: %v\n%s", err, b)
			}
			prop, ok := decoded.Properties["file:.claude/skills/b.md"]
			if !ok {
				t.Fatalf("second form schema missing file:.claude/skills/b.md:\n%s", b)
			}
			if prop.Title != wantTitle {
				t.Errorf("b.md title = %q, want %q", prop.Title, wantTitle)
			}
			if want, ok := prop.Default.(string); !ok || want != rowEnabled {
				t.Errorf("b.md default = %v, want pending %q (form 1's edit carried forward)", prop.Default, rowEnabled)
			}
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{}}
		},
	)
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"variant": "triage"},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, triage): %v", err)
	}
	if res.IsError {
		t.Fatalf("menu IsError: %s", text(t, res))
	}
	if !lexists(filepath.Join(dir, ".claude/skills/b.md")) {
		t.Errorf("b.md not enabled after open-full chain")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if i := placedIndex(rows, ".claude/skills/b.md"); i < 0 || rows[i].Enabled != "1" {
		t.Errorf("b.md ledger not enabled=1: %+v", rows)
	}
}

// The headline's total and "on at defaults (hidden)" counts are LEAVES, the
// same unit BuildTriageForm's flagged rows count in — not top-level
// toggleable items (countToggleable), which undercounts a group as 1. A
// single mixed group of 3 children, 2 off, is the exact regression scenario
// a reviewer caught: with the old total-flagged arithmetic (1 group - 2
// flagged rows) the headline went negative ("-1 on at defaults"); both
// numbers here must be non-negative and must add up with flagged the same
// way the message's own row-count line does.
func TestTriageMessageCountsLeavesNotGroups(t *testing.T) {
	items := []tui.Item{
		{Label: "skills/", Rel: "skills", Stage: tui.StageOnDemand, Group: true, Toggleable: true,
			Children: []tui.ChildRef{
				{Rel: "skills/a.md", Enabled: true},
				{Rel: "skills/b.md", Enabled: false},
				{Rel: "skills/c.md", Enabled: false},
			},
			Enabled: false, PartialOff: true, Count: 3},
	}
	_, _, flagged, err := BuildTriageForm(items)
	if err != nil {
		t.Fatalf("BuildTriageForm: %v", err)
	}
	if flagged != 2 {
		t.Fatalf("flagged = %d, want 2 (b.md, c.md rows)", flagged)
	}
	repo := &state.Repo{Root: "/repo"}
	msg := triageMessage(repo, items, flagged)
	wantLine := "omakase triage — 3 items in /repo · 1 on at defaults (hidden)."
	if !strings.Contains(msg, wantLine) {
		t.Fatalf("triageMessage =\n%s\nwant a line:\n%s", msg, wantLine)
	}
	if strings.Contains(msg, "-1") || strings.Contains(msg, "· -") {
		t.Fatalf("triageMessage went negative:\n%s", msg)
	}
}

// The preset variant's guards-only posture strips everything but the gates:
// the standalone file and both group members leave the working tree, while
// the already-on gate stays enabled — one form, no chain.
func TestMenuPresetGuardsOnly(t *testing.T) {
	dir, repo := placedFixture(t)
	eh := scriptedElicit(t, func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{keyPosture: postureGuards}}
	})
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"variant": "preset"},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, preset): %v", err)
	}
	if res.IsError {
		t.Fatalf("menu IsError: %s", text(t, res))
	}
	if lexists(filepath.Join(dir, "AGENTS.md")) {
		t.Errorf("AGENTS.md still present after guards-only preset")
	}
	if lexists(filepath.Join(dir, ".claude/skills/a.md")) {
		t.Errorf("a.md still present after guards-only preset")
	}
	if lexists(filepath.Join(dir, ".claude/skills/b.md")) {
		t.Errorf("b.md still present after guards-only preset")
	}
	if overlay.DisabledGates(repo.OMK)["smoke"] {
		t.Errorf("gate smoke disabled after guards-only preset, want still on")
	}
}

// Choosing customize item-by-item… chains into the full expanded form,
// defaulted to the CURRENT state (not to a posture the human never picked):
// accepting form 2 with only b.md flipped on changes exactly b.md, leaving
// its already-on sibling and the standalone file untouched.
func TestMenuPresetCustomizeChain(t *testing.T) {
	dir, repo := placedFixture(t)
	if err := overlay.FileOff(repo, ".claude/skills/b.md"); err != nil {
		t.Fatalf("arrange: FileOff(b.md): %v", err)
	}
	var sawSchema string
	eh := scriptedElicit(t,
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{keyPosture: postureCustomize}}
		},
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			b, _ := json.Marshal(req.Params.RequestedSchema)
			sawSchema = string(b)
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:.claude/skills/b.md": rowEnabled}}
		},
	)
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"variant": "preset"},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, preset): %v", err)
	}
	if res.IsError {
		t.Fatalf("menu IsError: %s", text(t, res))
	}
	if !strings.Contains(sawSchema, `"file:.claude/skills/a.md"`) || !strings.Contains(sawSchema, `"file:AGENTS.md"`) {
		t.Errorf("second form schema not the full expanded form: %s", sawSchema)
	}
	if !lexists(filepath.Join(dir, "AGENTS.md")) {
		t.Errorf("AGENTS.md removed, want untouched")
	}
	if !lexists(filepath.Join(dir, ".claude/skills/a.md")) {
		t.Errorf("a.md removed, want untouched")
	}
	if !lexists(filepath.Join(dir, ".claude/skills/b.md")) {
		t.Errorf("b.md not restored")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if i := placedIndex(rows, ".claude/skills/b.md"); i < 0 || rows[i].Enabled != "1" {
		t.Errorf("b.md ledger not enabled=1: %+v", rows)
	}
	if i := placedIndex(rows, "AGENTS.md"); i < 0 || rows[i].Enabled != "1" {
		t.Errorf("AGENTS.md ledger changed: %+v", rows)
	}
}

// Declining the second form of the customize chain applies nothing, per the
// transaction rule — even though form 1 already picked the customize
// posture, no ops exist until the LAST form is accepted.
func TestMenuPresetCustomizeDecline(t *testing.T) {
	dir, repo := placedFixture(t)
	eh := scriptedElicit(t,
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{keyPosture: postureCustomize}}
		},
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			return &mcp.ElicitResult{Action: "decline"}
		},
	)
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"variant": "preset"},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, preset): %v", err)
	}
	if out := text(t, res); out != "Menu closed — no changes made." {
		t.Errorf("chain-decline output = %q, want exact closed text", out)
	}
	if !lexists(filepath.Join(dir, "AGENTS.md")) {
		t.Errorf("decline removed AGENTS.md")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if i := placedIndex(rows, "AGENTS.md"); i < 0 || rows[i].Enabled != "1" {
		t.Errorf("decline altered the ledger: %+v", rows)
	}
}

// expand=true wins over variant even when variant names a real chain flow —
// guards the dispatch order against a future reordering that would route an
// expand request into a chain flow instead of the plain expanded form. The
// elicited schema is the full expanded shape (a per-file row for each skill),
// not a sections-style per-stage enum.
func TestMenuExpandWinsOverVariant(t *testing.T) {
	dir, _ := placedFixture(t)
	var sawSchema string
	eh := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		b, _ := json.Marshal(req.Params.RequestedSchema)
		sawSchema = string(b)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{}}, nil
	}
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"expand": true, "variant": "sections"},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, expand+variant=sections): %v", err)
	}
	if res.IsError {
		t.Fatalf("menu IsError: %s", text(t, res))
	}
	if !strings.Contains(sawSchema, `"file:.claude/skills/a.md"`) {
		t.Errorf("expand did not win over variant=sections, schema missing expanded file row: %s", sawSchema)
	}
	if strings.Contains(sawSchema, keySection) {
		t.Errorf("expand did not win over variant=sections, schema still has a section field: %s", sawSchema)
	}
}

// The sections variant's bulk-only path: choosing "all off" for the on-demand
// section in form 1 removes both skill files in one round trip, no chain.
func TestMenuSectionsBulkOnly(t *testing.T) {
	dir, repo := placedFixture(t)
	eh := scriptedElicit(t, func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{
			keySection + stageShort(tui.StageOnDemand): sectionAllOff,
		}}
	})
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"variant": "sections"},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, sections): %v", err)
	}
	if res.IsError {
		t.Fatalf("menu IsError: %s", text(t, res))
	}
	if lexists(filepath.Join(dir, ".claude/skills/a.md")) {
		t.Errorf("a.md still present after bulk all-off")
	}
	if lexists(filepath.Join(dir, ".claude/skills/b.md")) {
		t.Errorf("b.md still present after bulk all-off")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	for _, rel := range []string{".claude/skills/a.md", ".claude/skills/b.md"} {
		if i := placedIndex(rows, rel); i < 0 || rows[i].Enabled != "0" {
			t.Errorf("ledger not enabled=0 for %s after bulk all-off: %+v", rel, rows)
		}
	}
}

// Choosing "open this section…" for on-demand chains into a second form
// scoped to just that section's two skill rows; accepting it with b.md on
// restores b.md without touching a.md or the unrelated session-start file.
func TestMenuSectionsOpenChain(t *testing.T) {
	dir, repo := placedFixture(t)
	if err := overlay.FileOff(repo, ".claude/skills/b.md"); err != nil {
		t.Fatalf("arrange: FileOff(b.md): %v", err)
	}
	eh := scriptedElicit(t,
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{
				keySection + stageShort(tui.StageOnDemand): sectionOpen,
			}}
		},
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			b, _ := json.Marshal(req.Params.RequestedSchema)
			var decoded struct {
				Properties map[string]any `json:"properties"`
			}
			if err := json.Unmarshal(b, &decoded); err != nil {
				t.Fatalf("decode second form schema: %v\n%s", err, b)
			}
			if len(decoded.Properties) != 2 {
				t.Fatalf("second form has %d properties, want 2 (only the two skill rows): %s", len(decoded.Properties), b)
			}
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:.claude/skills/b.md": rowEnabled}}
		},
	)
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"variant": "sections"},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, sections): %v", err)
	}
	if res.IsError {
		t.Fatalf("menu IsError: %s", text(t, res))
	}
	if !lexists(filepath.Join(dir, ".claude/skills/a.md")) {
		t.Errorf("a.md removed, want untouched")
	}
	if !lexists(filepath.Join(dir, ".claude/skills/b.md")) {
		t.Errorf("b.md not restored")
	}
	if !lexists(filepath.Join(dir, "AGENTS.md")) {
		t.Errorf("AGENTS.md removed, want untouched — a different section")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if i := placedIndex(rows, ".claude/skills/b.md"); i < 0 || rows[i].Enabled != "1" {
		t.Errorf("b.md ledger not enabled=1: %+v", rows)
	}
}

// Declining the sub-form discards EVERYTHING accumulated in the chain,
// including a DIFFERENT section's already-computed bulk change from form 1 —
// the sections-specific transaction rule, stricter than triage/preset's
// two-form chains where only one form's edits were ever pending.
func TestMenuSectionsDeclineSubFormCancelsBulk(t *testing.T) {
	dir, repo := placedFixture(t)
	eh := scriptedElicit(t,
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{
				keySection + stageShort(tui.StagePreCommit): sectionAllOff,
				keySection + stageShort(tui.StageOnDemand):  sectionOpen,
			}}
		},
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			return &mcp.ElicitResult{Action: "decline"}
		},
	)
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"variant": "sections"},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, sections): %v", err)
	}
	if out := text(t, res); out != "Menu closed — no changes made." {
		t.Errorf("decline output = %q, want exact closed text", out)
	}
	if overlay.DisabledGates(repo.OMK)["smoke"] {
		t.Errorf("gate smoke disabled despite sub-form decline discarding the accumulated bulk change")
	}
}

// Opening two sections in form 1 chains into two sub-forms, one per section
// in declared order; the handler must see exactly three elicit calls total,
// and both sub-forms' edits apply together in the one final batch.
func TestMenuSectionsTwoOpens(t *testing.T) {
	dir, repo := placedFixture(t)
	if err := overlay.FileOff(repo, ".claude/skills/b.md"); err != nil {
		t.Fatalf("arrange: FileOff(b.md): %v", err)
	}
	calls := 0
	eh := scriptedElicit(t,
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			calls++
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{
				keySection + stageShort(tui.StageOnDemand):  sectionOpen,
				keySection + stageShort(tui.StagePreCommit): sectionOpen,
			}}
		},
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			calls++
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:.claude/skills/b.md": rowEnabled}}
		},
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			calls++
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"gate:smoke": rowDisabled}}
		},
	)
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"variant": "sections"},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, sections): %v", err)
	}
	if res.IsError {
		t.Fatalf("menu IsError: %s", text(t, res))
	}
	if calls != 3 {
		t.Errorf("elicit called %d times, want 3 (form 1 + one sub-form per opened section)", calls)
	}
	if !lexists(filepath.Join(dir, ".claude/skills/b.md")) {
		t.Errorf("b.md not restored")
	}
	if !overlay.DisabledGates(repo.OMK)["smoke"] {
		t.Errorf("gate smoke not disabled after two-opens chain")
	}
}

// A decline on the SECOND form of the open-full chain applies nothing, even
// though form 1 already computed a b.md-on edit — the transaction rule's
// "nothing applies until the LAST form is accepted".
func TestMenuTriageChainDeclineSecondForm(t *testing.T) {
	dir, repo := placedFixture(t)
	if err := overlay.FileOff(repo, ".claude/skills/b.md"); err != nil {
		t.Fatalf("arrange: FileOff(b.md): %v", err)
	}
	eh := scriptedElicit(t,
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{
				keyOpenFull: true, "file:.claude/skills/b.md": rowEnabled,
			}}
		},
		func(t *testing.T, req *mcp.ElicitRequest) *mcp.ElicitResult {
			return &mcp.ElicitResult{Action: "decline"}
		},
	)
	cs := connect(t, dir, eh)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "menu",
		Arguments: map[string]any{"variant": "triage"},
	})
	if err != nil {
		t.Fatalf("CallTool(menu, triage): %v", err)
	}
	if out := text(t, res); out != "Menu closed — no changes made." {
		t.Errorf("chain-decline output = %q, want exact closed text", out)
	}
	if lexists(filepath.Join(dir, ".claude/skills/b.md")) {
		t.Errorf("b.md enabled despite second-form decline")
	}
	rows := state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv"))
	if i := placedIndex(rows, ".claude/skills/b.md"); i < 0 || rows[i].Enabled != "0" {
		t.Errorf("b.md ledger changed despite decline: %+v", rows)
	}
}
