package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Yuncun/omakase-harness/internal/overlay"
	"github.com/Yuncun/omakase-harness/internal/state"
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
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:AGENTS.md": false}}, nil
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
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"gate:smoke": false}}, nil
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
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:AGENTS.md": true, "gate:smoke": true}}, nil
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
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"dir:.claude/skills": false}}, nil
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
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"dir:.claude/skills": false}}, nil
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
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:AGENTS.md": false}}, nil
	}
	cs := connect(t, dir, off)
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"}); err != nil {
		t.Fatalf("menu off: %v", err)
	}
	on := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"file:AGENTS.md": true}}, nil
	}
	cs2 := connect(t, dir, on)
	if _, err := cs2.CallTool(context.Background(), &mcp.CallToolParams{Name: "menu"}); err != nil {
		t.Fatalf("menu on: %v", err)
	}
	if !lexists(filepath.Join(dir, "AGENTS.md")) {
		t.Errorf("AGENTS.md not restored by menu on")
	}
}
