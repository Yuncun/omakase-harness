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

	"github.com/Yuncun/omakase-harness/internal/overlay"
	"github.com/Yuncun/omakase-harness/internal/state"
	"github.com/Yuncun/omakase-harness/internal/status"
	"github.com/Yuncun/omakase-harness/internal/tui"
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
		Description: "Open the omakase consent menu: a form the HUMAN fills in to enable/disable individual harness files and gates. The host shows the form directly to the user — never answer it on their behalf. Set expand=true when the user asks for the full/expanded menu (every file as its own row instead of one row per directory). Set variant when the user asks for the triage, preset, or sections view by name.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"expand":{"type":"boolean","description":"Show every file as its own row instead of collapsing directories into one row (default false)."},"variant":{"type":"string","description":"View shape: \"triage\" (only items needing attention), \"preset\" (one posture question), \"sections\" (one row per dev-loop section with drill-down). Omit for the standard collapsed menu."}}}`),
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

// menuHandler raises the consent form and applies exactly what the human
// changed. Every state read happens per call so the form always reflects the
// repo as it is now. The optional expand argument swaps the one-row-per-
// directory view for one row per file; a malformed arguments payload is
// treated as expand=false/variant="" rather than an error, because the
// collapsed menu is always a safe answer. The variant argument picks one of
// the alternate view shapes (Tasks 2-4); absent, unknown, or malformed
// values fall through to the same collapsed flow as no variant at all, and
// expand always wins over variant when both are set.
func menuHandler(root string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			Expand  bool   `json:"expand"`
			Variant string `json:"variant"`
		}
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &args)
		}
		repo, err := state.Discover(root)
		if err != nil {
			return textResult("omakase: not inside a git repo", true), nil
		}
		items, _ := tui.LiveItems(repo)

		if !args.Expand && args.Variant == "triage" {
			return textResult(triageFlow(ctx, req.Session, repo, items), false), nil
		}

		var fields []Field
		var schema json.RawMessage
		switch {
		case args.Expand:
			fields, schema, err = BuildForm(items, true)
		case args.Variant == "preset":
			// Task 3 replaces this case with a call to presetFlow.
			fallthrough
		case args.Variant == "sections":
			// Task 4 replaces this case with a call to sectionsFlow.
			fallthrough
		default:
			fields, schema, err = BuildForm(items, false)
		}
		if err != nil {
			return textResult("omakase: could not build the menu: "+err.Error(), true), nil
		}
		if len(fields) == 0 {
			return textResult("Nothing to toggle — this repo has no omakase consent items. The status tool shows the full picture.", false), nil
		}
		res, err := elicit(ctx, req.Session, &mcp.ElicitParams{
			Message:         menuMessage(repo, len(fields)),
			RequestedSchema: schema,
		})
		if err != nil {
			// A client that never set an ElicitationHandler doesn't advertise the
			// elicitation capability, so Elicit fails here rather than showing a
			// form — the instructive fallback, not a protocol error, since the
			// human still needs a way to make the change.
			return textResult("This client could not show the omakase form ("+err.Error()+"). "+fallbackHelp, false), nil
		}
		if res.Action != "accept" {
			return textResult("Menu closed — no changes made.", false), nil
		}
		ops := Diff(fields, res.Content)
		return textResult(apply(repo, ops), false), nil
	}
}

// elicit wraps Session.Elicit with a panic recover. WHY: a spec-loose client
// can reply {"action":"accept"} with content omitted or null — a nil
// map[string]any that passes go-sdk's server-side object validation (a nil
// map still has Kind() == Map) but then reaches jsonschema-go's
// ApplyDefaults, which assigns straight into that map to backfill defaults
// and panics with "assignment to entry in nil map" (go-sdk v1.6.1 ->
// jsonschema-go v0.4.3, validate.go's applyDefaults). go-sdk does not
// recover panics inside tool handlers, so an unrecovered panic here would
// kill the whole `omakase mcp` process and the human's session with it. The
// consent surface has to survive a client that gets this wrong, so the
// panic is caught here and folded into the same err != nil branch that
// already handles "client can't elicit" — same graceful fallback text,
// whether the client refused to elicit or elicited badly.
func elicit(ctx context.Context, session *mcp.ServerSession, params *mcp.ElicitParams) (res *mcp.ElicitResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("elicitation failed: %v", r)
		}
	}()
	return session.Elicit(ctx, params)
}

// menuMessage is the text above the form: what checking a box means and the
// promise that submit is the only thing that changes state.
func menuMessage(repo *state.Repo, n int) string {
	return fmt.Sprintf("omakase consent menu — %d item(s) in %s.\nChecked = enabled. Adjust anything, then submit; nothing changes until you do, and declining changes nothing.", n, repo.Root)
}

// triageFlow runs the triage variant's elicitation chain: one form listing
// only what needs attention (off files/gates, a mixed group's off children)
// plus a bulk-change row and an "open the full list" escape hatch, then —
// only when the human asks for it — a second, full expanded form whose
// defaults already carry the first form's edits. THE TRANSACTION RULE binds
// both forms: nothing is applied to the repo until the LAST form in the
// chain is accepted, a decline or elicit error at either form applies
// nothing, and the final ops are computed once, against the state `items`
// already captured — never against whatever the repo drifted to mid-chain.
func triageFlow(ctx context.Context, session *mcp.ServerSession, repo *state.Repo, items []tui.Item) string {
	fields, schema, flagged, err := BuildTriageForm(items)
	if err != nil {
		return "omakase: could not build the triage form: " + err.Error()
	}
	res, err := elicit(ctx, session, &mcp.ElicitParams{
		Message:         triageMessage(repo, items, flagged),
		RequestedSchema: schema,
	})
	if err != nil {
		// First form of the chain: an elicit-capability failure gets the same
		// instructive fallback as the collapsed menu, per the transaction rule.
		return "This client could not show the omakase form (" + err.Error() + "). " + fallbackHelp
	}
	if res.Action != "accept" {
		return "Menu closed — no changes made."
	}

	ops, openFull := TriageOps(fields, res.Content, items)
	if !openFull {
		return apply(repo, ops)
	}

	pending := ApplyOps(items, ops)
	fullFields, fullSchema, err := BuildForm(pending, true)
	if err != nil {
		return "omakase: could not build the full form: " + err.Error()
	}
	res2, err := elicit(ctx, session, &mcp.ElicitParams{
		Message:         menuMessage(repo, len(fullFields)),
		RequestedSchema: fullSchema,
	})
	if err != nil || res2.Action != "accept" {
		// Mid-chain: an elicit error behaves like a decline, not the
		// first-form fallback — the human already saw a working form once.
		return "Menu closed — no changes made."
	}
	finalOps := EffectiveOps(fullFields, res2.Content, stateByKey(items))
	return apply(repo, finalOps)
}

// triageMessage is the text above the triage form: the flagged count, what
// stays on unconditionally (tracked, not consent-gated, no row at all), and
// the same "nothing changes until submit" promise every menu form makes.
func triageMessage(repo *state.Repo, items []tui.Item, flagged int) string {
	total := countToggleable(items)
	var b strings.Builder
	fmt.Fprintf(&b, "omakase triage — %d items in %s · %d on at defaults (hidden).\n", total, repo.Root, total-flagged)
	var tracked []string
	for _, it := range items {
		if !it.Toggleable {
			tracked = append(tracked, it.Label)
		}
	}
	if len(tracked) > 0 {
		fmt.Fprintf(&b, "Always on, tracked in the repo: %s.\n", strings.Join(tracked, ", "))
	}
	if flagged == 0 {
		b.WriteString("Nothing needs attention — everything is on at defaults.\n")
	} else {
		fmt.Fprintf(&b, "%d item(s) need your attention:\n", flagged)
	}
	b.WriteString("Nothing changes until you submit.")
	return b.String()
}

// toggleGate dispatches to the same GateOff/GateOn backend the interactive
// screen and the --disable/--enable flags use, picking by op.On rather than
// calling Off unconditionally — GateOff/GateOn are individually idempotent,
// but only one of them is the actually-requested direction.
func toggleGate(repo *state.Repo, op Op) error {
	if op.On {
		return overlay.GateOn(repo, op.Rel)
	}
	return overlay.GateOff(repo, op.Rel)
}

// toggleFile dispatches to FileOff/FileOn by the requested direction. Picking
// by `on` (rather than always calling FileOff and conditionally overriding
// with FileOn, as a naive symmetry with toggleGate might suggest) matters
// here in a way it doesn't for gates: an unconditional FileOff on a file
// that's already on for real (an untouched member of a group being switched
// to "all on") would delete it before FileOn restores it from the snapshot —
// a needless delete+restore that turns into data loss if the restore then
// fails. tui/model.go's applyGroup uses this same if/else shape.
func toggleFile(repo *state.Repo, rel string, on bool) error {
	if on {
		return overlay.FileOn(repo, rel)
	}
	return overlay.FileOff(repo, rel)
}

// apply runs each requested change through the same overlay backend as the
// interactive screen and the --disable/--enable flags. Refusals (tracked,
// locally edited) do not abort the batch — every outcome is reported so the
// human sees exactly what happened.
func apply(repo *state.Repo, ops []Op) string {
	if len(ops) == 0 {
		return "Submitted with no changes — everything stays as it was."
	}
	var b strings.Builder
	applied := 0
	for _, op := range ops {
		to := "off"
		if op.On {
			to = "on"
		}
		switch {
		case op.IsGate:
			if err := toggleGate(repo, op); err != nil {
				fmt.Fprintf(&b, "  ✗ gate %s → %s: %v\n", op.Rel, to, err)
			} else {
				applied++
				fmt.Fprintf(&b, "  ✓ gate %s → %s\n", op.Rel, to)
			}
		case op.Group:
			var failed []string
			for _, rel := range op.Children {
				if err := toggleFile(repo, rel, op.On); err != nil {
					failed = append(failed, fmt.Sprintf("%s: %v", rel, err))
				}
			}
			if len(failed) == 0 {
				applied++
				fmt.Fprintf(&b, "  ✓ %s/ → all %s (%d files)\n", op.Rel, to, len(op.Children))
			} else {
				fmt.Fprintf(&b, "  ✗ %s/ → all %s: %d of %d refused\n", op.Rel, to, len(failed), len(op.Children))
				for _, f := range failed {
					fmt.Fprintf(&b, "      %s\n", f)
				}
			}
		default:
			if err := toggleFile(repo, op.Rel, op.On); err != nil {
				fmt.Fprintf(&b, "  ✗ %s → %s: %v\n", op.Rel, to, err)
			} else {
				applied++
				fmt.Fprintf(&b, "  ✓ %s → %s\n", op.Rel, to)
			}
		}
	}
	return fmt.Sprintf("Applied %d of %d change(s):\n%sRun the status tool to see the updated page.", applied, len(ops), b.String())
}

// textResult wraps text as a single-content tool result.
func textResult(text string, isErr bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: isErr}
}
