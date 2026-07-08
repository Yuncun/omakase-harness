// Package mcpserver is the `omakase mcp` verb: a stdio MCP server that puts
// the omakase consent menu INSIDE agent CLIs. Neither Claude Code nor Copilot
// CLI can host the interactive TUI (no PTY reaches a subprocess), but both
// natively render MCP elicitation forms — the host draws the form itself, so
// the human sees and answers ground truth the agent cannot rewrite. form.go
// is the pure layer: screen items -> form schema, submitted form -> ops.
package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/tui"
)

// Enum values for a header field and a partially-off collapsed group.
// Neither can be expressed as a boolean: a header's "keep as-is" means leave
// every row under it untouched (not an on/off state at all), and a mixed
// group's existing split can't be captured by true/false either — a header
// or partial-group field is the only kind of Field that carries one of these
// three strings as its Default; every other field is a plain bool.
const (
	keepAsIs = "keep as-is"
	allOn    = "all on"
	allOff   = "all off"
)

// Field is one form property: the schema key, the item it controls, and the
// default sent to the client. Diff/NestedOps compare submissions against
// Default, so only fields the human actually changed become operations.
type Field struct {
	Key      string // property name: "stage:<stageShort>" for a header, "gate:<name>"/"file:<rel>"/"dir:<rel>" for a child row
	Rel      string // gate name, file rel, or group dir prefix; empty for a header field
	IsGate   bool
	Group    bool
	Children []string  // group fields: member rels in ledger order
	Default  any       // bool for a leaf row (gate, file, uniform group); keepAsIs for a header or a partial group's 3-choice enum
	Stage    tui.Stage // which stage this field belongs to; only a header field's Stage is read (by NestedOps, to scope SectionBulkOps)
}

// Op is one consent change the human requested: flip Rel (a gate, a file, or
// every member of a group) to On.
type Op struct {
	IsGate   bool
	Group    bool
	Rel      string
	Children []string
	On       bool
}

// dedupeGates collapses gate items that share a name into one. lefthook can
// wire the same gate at both pre-commit and pre-push, which tui.BuildItems
// surfaces as two gate items with the same Rel; left as-is BuildForm would
// emit two "gate:<name>" fields — a duplicate JSON schema key and a
// double-counted op for one real change (gates are name-keyed in
// disabled-gates). Applied once where items enter the server so every ops
// path sees one gate item per name. The survivor keeps the first
// occurrence's position and, when the gate runs on more than one hook, gains a
// "(hook, hook)" label suffix so the row still names every hook it runs on.
// Non-gate items pass through untouched, in order.
func dedupeGates(items []tui.Item) []tui.Item {
	hooks := map[string][]string{}
	for _, it := range items {
		if it.IsGate {
			hooks[it.Rel] = append(hooks[it.Rel], gateHook(it.Stage))
		}
	}
	seen := map[string]bool{}
	out := make([]tui.Item, 0, len(items))
	for _, it := range items {
		if !it.IsGate {
			out = append(out, it)
			continue
		}
		if seen[it.Rel] {
			continue
		}
		seen[it.Rel] = true
		if hs := hooks[it.Rel]; len(hs) > 1 {
			it.Label = fmt.Sprintf("%s (%s)", it.Rel, strings.Join(hs, ", "))
		}
		out = append(out, it)
	}
	return out
}

// gateHook names the hook a gate item's stage represents, for a merged gate's
// multi-hook label. Gate items only ever carry StagePreCommit or StagePrePush.
func gateHook(s tui.Stage) string {
	if s == tui.StagePrePush {
		return "pre-push"
	}
	return "pre-commit"
}

// stageShort is the compact section tag used in field titles — the screen's
// full headers (tui.StageTitle) are too long for a form row.
func stageShort(s tui.Stage) string {
	switch s {
	case tui.StageSessionStart:
		return "session start"
	case tui.StageOnDemand:
		return "on demand"
	case tui.StageDuringSession:
		return "during session"
	case tui.StagePreCommit:
		return "pre-commit"
	case tui.StagePrePush:
		return "pre-push"
	default:
		return "other"
	}
}

// BuildForm turns the interactive screen's Item list into the nested cascade
// elicitation form (spec §The form): one header field per dev-loop stage that
// has at least one toggleable item, immediately followed by that stage's
// child rows — a stage with nothing toggleable contributes no fields at all.
// A header is always the keepAsIs/allOn/allOff enum; every leaf row (a gate,
// a standalone file, an expanded group member, or a uniform collapsed group)
// is a boolean mirroring its current on/off state, since only a MIXED
// collapsed group needs the header's 3-choice enum to express "leave the
// split alone" (a boolean cannot). With expand, every group dissolves into
// one file field per member instead of one dir: row — the full per-file view
// of the status page. All titles are built once, then padded together so a
// host that renders them verbatim in a monospace terminal still lines up the
// "title: value" column across the whole form, not just within one stage.
func BuildForm(items []tui.Item, expand bool) ([]Field, json.RawMessage, error) {
	type row struct {
		f     Field
		title string
	}
	var rows []row

	for s := tui.StageSessionStart; s <= tui.StageOther; s++ {
		var stageItems []tui.Item
		for _, it := range items {
			if it.Toggleable && it.Stage == s {
				stageItems = append(stageItems, it)
			}
		}
		if len(stageItems) == 0 {
			continue
		}

		// n/on are LEAVES (a group contributes its child count, never 1) —
		// the unit the header's "(n/m on)" title reports. allGates holds only
		// for pre-commit/pre-push, the only stages BuildItems ever assigns a
		// gate item to, so it is equivalent to (and cheaper than) checking the
		// stage number directly.
		allGates, n, on := true, 0, 0
		for _, it := range stageItems {
			if !it.IsGate {
				allGates = false
			}
			if it.Group {
				n += it.Count
				for _, c := range it.Children {
					if c.Enabled {
						on++
					}
				}
				continue
			}
			n++
			if it.Enabled {
				on++
			}
		}

		short := stageShort(s)
		headerTitle := fmt.Sprintf("[%s] (%d/%d on)", short, on, n)
		if allGates {
			headerTitle = fmt.Sprintf("[%s] gates (%d/%d on)", short, on, n)
		}
		rows = append(rows, row{f: Field{Key: "stage:" + short, Stage: s, Default: keepAsIs}, title: headerTitle})

		for _, it := range stageItems {
			switch {
			case it.IsGate:
				rows = append(rows, row{
					f:     Field{Key: "gate:" + it.Rel, Rel: it.Rel, IsGate: true, Default: it.Enabled},
					title: "· " + ellipsizeRel(it.Label, 48),
				})
			case it.Group && expand:
				for _, c := range it.Children {
					rows = append(rows, row{
						f:     Field{Key: "file:" + c.Rel, Rel: c.Rel, Default: c.Enabled},
						title: "· " + ellipsizeRel(c.Rel, 48),
					})
				}
			case it.Group:
				children := make([]string, len(it.Children))
				for i, c := range it.Children {
					children[i] = c.Rel
				}
				f := Field{Key: "dir:" + it.Rel, Rel: it.Rel, Group: true, Children: children}
				title := fmt.Sprintf("· %s (%d files)", ellipsizeRel(it.Label, 48), it.Count)
				if it.PartialOff {
					// A boolean cannot express "keep 5/9 on", so a mixed group
					// keeps the collapsed menu's 3-choice enum instead.
					f.Default = keepAsIs
					groupOn := 0
					for _, c := range it.Children {
						if c.Enabled {
							groupOn++
						}
					}
					title = fmt.Sprintf("%s — %d/%d on", title, groupOn, it.Count)
				} else {
					f.Default = it.Enabled
				}
				rows = append(rows, row{f: f, title: title})
			default:
				rows = append(rows, row{
					f:     Field{Key: "file:" + it.Rel, Rel: it.Rel, Default: it.Enabled},
					title: "· " + ellipsizeRel(it.Rel, 48),
				})
			}
		}
	}

	titles := make([]string, len(rows))
	for i, r := range rows {
		titles[i] = r.title
	}
	titles = padTitles(titles)

	var props strings.Builder
	fields := make([]Field, len(rows))
	for i, r := range rows {
		prop, err := propertyJSON(r.f, titles[i])
		if err != nil {
			return nil, nil, err
		}
		if props.Len() > 0 {
			props.WriteByte(',')
		}
		props.WriteString(prop)
		fields[i] = r.f
	}
	schema := json.RawMessage(`{"type":"object","properties":{` + props.String() + `}}`)
	return fields, schema, nil
}

// ellipsizeRel middle-truncates rel to exactly max runes (the "…" itself
// counting as one) so a long path still fits one form row before padTitles
// aligns the whole form's title column; rel shorter than max passes through
// unchanged. Truncating in the middle, not at either end, keeps both the
// distinguishing leading path and the trailing filename visible.
func ellipsizeRel(rel string, max int) string {
	runes := []rune(rel)
	if len(runes) <= max {
		return rel
	}
	if max <= 1 {
		return "…"
	}
	keep := max - 1 // runes left for rel's own text once the ellipsis is counted
	head := (keep + 1) / 2
	tail := keep - head
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}

// padTitles right-pads every title with spaces to the widest one's rune
// count, so a host that renders titles verbatim in a monospace terminal still
// lines up the collapsed "title: value" column across every row in the form.
func padTitles(titles []string) []string {
	width := 0
	runeCounts := make([]int, len(titles))
	for i, t := range titles {
		n := len([]rune(t))
		runeCounts[i] = n
		if n > width {
			width = n
		}
	}
	out := make([]string, len(titles))
	for i, t := range titles {
		out[i] = t + strings.Repeat(" ", width-runeCounts[i])
	}
	return out
}

// propertyJSON renders one schema property, marshaling every dynamic string
// through encoding/json so titles and rels with quotes or backslashes stay
// valid JSON. Every Field BuildForm constructs is either the keepAsIs/allOn/
// allOff enum (a header, or a partially-off collapsed group) or a plain
// boolean (every other row) — there is no third shape.
func propertyJSON(f Field, title string) (string, error) {
	key, err := json.Marshal(f.Key)
	if err != nil {
		return "", err
	}
	t, err := json.Marshal(title)
	if err != nil {
		return "", err
	}
	if s, ok := f.Default.(string); ok && s == keepAsIs {
		return fmt.Sprintf(`%s:{"type":"string","title":%s,"enum":["%s","%s","%s"],"default":"%s"}`,
			key, t, keepAsIs, allOn, allOff, keepAsIs), nil
	}
	return fmt.Sprintf(`%s:{"type":"boolean","title":%s,"default":%v}`, key, t, f.Default.(bool)), nil
}

// ApplyOps returns a deep copy of items with each op's target state applied.
// Nothing in the current single-form flow calls this at runtime — it exists
// for tests that want to assert on the post-op state of an items slice
// without touching the caller's own copy: a gate op flips that gate's
// Enabled; a group op flips every child's Enabled and recomputes the parent's
// Enabled/PartialOff; a file op flips either a standalone item or, if none
// matches, the group child with that Rel (then recomputes that group's
// Enabled/PartialOff). Unknown rels are ignored.
func ApplyOps(items []tui.Item, ops []Op) []tui.Item {
	out := make([]tui.Item, len(items))
	copy(out, items)
	for i := range out {
		if out[i].Children != nil {
			children := make([]tui.ChildRef, len(out[i].Children))
			copy(children, out[i].Children)
			out[i].Children = children
		}
	}
	for _, op := range ops {
		applyOp(out, op)
	}
	return out
}

// applyOp mutates out — already a deep copy — in place for one Op.
func applyOp(out []tui.Item, op Op) {
	switch {
	case op.IsGate:
		for i := range out {
			if out[i].IsGate && out[i].Rel == op.Rel {
				out[i].Enabled = op.On
				return
			}
		}
	case op.Group:
		for i := range out {
			if out[i].Group && out[i].Rel == op.Rel {
				for c := range out[i].Children {
					out[i].Children[c].Enabled = op.On
				}
				recomputeGroup(&out[i])
				return
			}
		}
	default:
		for i := range out {
			if !out[i].Group && !out[i].IsGate && out[i].Rel == op.Rel {
				out[i].Enabled = op.On
				return
			}
		}
		for i := range out {
			if !out[i].Group {
				continue
			}
			for c := range out[i].Children {
				if out[i].Children[c].Rel == op.Rel {
					out[i].Children[c].Enabled = op.On
					recomputeGroup(&out[i])
					return
				}
			}
		}
	}
}

// recomputeGroup derives a group item's Enabled/PartialOff from its
// (already-updated) Children — the same rule tui/items.go's buildGroupItem
// uses: Enabled means every child is on, PartialOff means some but not all.
func recomputeGroup(it *tui.Item) {
	on := 0
	for _, c := range it.Children {
		if c.Enabled {
			on++
		}
	}
	it.Enabled = on == len(it.Children)
	it.PartialOff = on > 0 && on < len(it.Children)
}

// stateByKey returns the current on/off state of every toggleable leaf, keyed
// the same way BuildForm keys its leaf fields: "file:<rel>" for standalone
// files AND group children, "gate:<name>" for gates. Group parents ("dir:"
// rows are not leaves) and non-toggleable items are absent. menuMessage uses
// this to count gates and files separately for the headline, in the same
// LEAF unit BuildForm's header titles count in.
func stateByKey(items []tui.Item) map[string]bool {
	byKey := map[string]bool{}
	for _, it := range items {
		if !it.Toggleable {
			continue
		}
		switch {
		case it.IsGate:
			byKey["gate:"+it.Rel] = it.Enabled
		case it.Group:
			for _, c := range it.Children {
				byKey["file:"+c.Rel] = c.Enabled
			}
		default:
			byKey["file:"+it.Rel] = it.Enabled
		}
	}
	return byKey
}

// Diff compares the submitted form content against each field's default.
// Missing keys and unchanged values yield no operation — an untouched form
// changes nothing, and a mixed group left at "keep as-is" stays mixed.
func Diff(fields []Field, content map[string]any) []Op {
	var ops []Op
	for _, f := range fields {
		got, present := content[f.Key]
		if !present {
			continue
		}
		if s, ok := f.Default.(string); ok && s == keepAsIs {
			// The server-side enum schema restricts this field to the three
			// declared values, so the SDK rejects anything else before it
			// reaches us today. Checking explicitly for allOn/allOff (rather
			// than "anything that isn't keepAsIs") means a future SDK that
			// relaxes that validation can't turn stray junk into a
			// destructive group-off by falling through this branch.
			choice, ok := got.(string)
			if !ok || (choice != allOn && choice != allOff) {
				continue
			}
			ops = append(ops, Op{IsGate: f.IsGate, Group: f.Group, Rel: f.Rel, Children: f.Children, On: choice == allOn})
			continue
		}
		want, ok := got.(bool)
		if !ok || want == f.Default.(bool) {
			continue
		}
		ops = append(ops, Op{IsGate: f.IsGate, Group: f.Group, Rel: f.Rel, Children: f.Children, On: want})
	}
	return ops
}

// groupDiff reports whether any child of the group item differs from target,
// and returns the children's rels in ledger order for a group Op's Children
// field. The only remaining caller is SectionBulkOps.
func groupDiff(it tui.Item, target bool) (bool, []string) {
	children := make([]string, len(it.Children))
	diff := false
	for i, c := range it.Children {
		children[i] = c.Rel
		if c.Enabled != target {
			diff = true
		}
	}
	return diff, children
}

// NestedOps turns a nested-cascade form submission (spec §Cascade semantics)
// into the ops to apply. Header fields (key prefix "stage:") submitted
// exactly allOn/allOff bulk-change that stage via SectionBulkOps — the same
// explicit-other-choice hardening as Diff's keepAsIs branch, so a header
// value that isn't one of the two declared choices does nothing. Every
// non-header field is then run through Diff as usual (its bool branch
// handles boolean child rows, its enum branches handle a partial group's
// enum row). Header ops are appended first, child ops after: apply order is
// last-wins, so an explicit child change always survives its own stage's
// header bulk.
func NestedOps(fields []Field, content map[string]any, items []tui.Item) []Op {
	var ops []Op
	var childFields []Field
	for _, f := range fields {
		if !strings.HasPrefix(f.Key, "stage:") {
			childFields = append(childFields, f)
			continue
		}
		choice, ok := content[f.Key].(string)
		if !ok || (choice != allOn && choice != allOff) {
			continue
		}
		ops = append(ops, SectionBulkOps(items, f.Stage, choice == allOn)...)
	}
	return append(ops, Diff(childFields, content)...)
}

// SectionBulkOps targets one stage's toggleable items at a single on/off
// value: a group becomes one group Op (groupDiff's "does any child differ
// from target" check), a gate or standalone file becomes one Op. Only items
// that actually differ from the target produce an Op.
func SectionBulkOps(items []tui.Item, stage tui.Stage, on bool) []Op {
	var ops []Op
	for _, it := range items {
		if !it.Toggleable || it.Stage != stage {
			continue
		}
		switch {
		case it.IsGate:
			if it.Enabled != on {
				ops = append(ops, Op{IsGate: true, Rel: it.Rel, On: on})
			}
		case it.Group:
			if diff, children := groupDiff(it, on); diff {
				ops = append(ops, Op{Group: true, Rel: it.Rel, Children: children, On: on})
			}
		default:
			if it.Enabled != on {
				ops = append(ops, Op{Rel: it.Rel, On: on})
			}
		}
	}
	return ops
}
