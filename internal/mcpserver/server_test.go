package mcpserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Yuncun/omakase-harness/internal/overlay"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// placedFixture builds a git repo with one placed rule file (AGENTS.md) and a
// lefthook stub whose `dump` output declares one omakase pre-commit gate
// (smoke) — the minimal repo where LiveItems yields both a toggleable file
// and a toggleable gate. It chdirs into the repo (status.Run resolves the
// repo from the working directory) and returns the dir plus discovered Repo.
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
