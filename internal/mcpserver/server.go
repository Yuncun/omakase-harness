// server.go wires the pure form layer to the MCP SDK: server construction,
// the two tools, and the stdio verb entry. The repo is the one containing
// the process working directory — agent CLIs launch MCP servers at the
// project root.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/status"
)

// fallbackHelp is what a human gets when the form cannot be shown — the
// scriptable twins of the menu, same as the interactive screen's agents path.
const fallbackHelp = "Fallback: `omakase status --plain` shows the page; `omakase status --disable <name>` / `--enable <name>` toggle one item."

// Run is the `omakase mcp` verb: serve MCP over stdio until the client hangs
// up. It refuses to start outside a git repo so a misconfigured host fails
// loudly at registration time, not on first tool call.
func Run(argv []string, stdout, stderr io.Writer) int {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return 1
	}
	if _, err := state.Discover(wd); err != nil {
		fmt.Fprintln(stderr, "omakase: not inside a git repo")
		return 1
	}
	if err := NewServer(wd).Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(stderr, "omakase mcp: %v\n", err)
		return 1
	}
	return 0
}

// NewServer builds the MCP server for the repo containing root. State is
// re-read on every tool call (like the screen's reload) so the menu and the
// page always show the repo as it is now, not as it was at connect time.
func NewServer(root string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "omakase", Title: "omakase harness", Version: "dev"}, nil)
	srv.AddTool(&mcp.Tool{
		Name:        "status",
		Title:       "omakase status",
		Description: "The omakase harness status page for this repo: what is injected, which gates run when, what is enabled. Read-only.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, statusHandler())
	srv.AddTool(&mcp.Tool{
		Name:        "menu",
		Title:       "omakase menu",
		Description: "Open the omakase consent menu: a form the HUMAN fills in to enable/disable individual harness files and gates. The host shows the form directly to the user — never answer it on their behalf.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Meta:        mcp.Meta{"anthropic/requiresUserInteraction": true},
	}, menuHandler(root))
	return srv
}

// statusHandler renders the plain page in-process. status.Run resolves the
// repo from the working directory — the same directory NewServer's root came
// from, since hosts launch the server at the project root.
func statusHandler() mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var out, errb strings.Builder
		if code := status.Run([]string{"--plain"}, &out, &errb); code != 0 {
			return textResult("omakase status failed: "+strings.TrimSpace(errb.String()), true), nil
		}
		return textResult(out.String(), false), nil
	}
}

// menuHandler is stubbed in Task 2 and implemented in Task 3.
func menuHandler(root string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult("The omakase menu is not available in this build. "+fallbackHelp, false), nil
	}
}

// textResult wraps text as a single-content tool result.
func textResult(text string, isErr bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: isErr}
}
