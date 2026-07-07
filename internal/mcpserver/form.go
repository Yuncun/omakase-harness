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

// Enum values for a partially-off group field. A boolean cannot express
// "leave the mixed state alone", so mixed groups get these three choices.
const (
	keepAsIs = "keep as-is"
	allOn    = "all on"
	allOff   = "all off"
)

// Field is one form property: the schema key, the item it controls, and the
// default sent to the client. Diff compares submissions against Default, so
// only fields the human actually changed become operations.
type Field struct {
	Key      string // property name: "gate:<name>", "file:<rel>", "dir:<prefix>"
	Rel      string // gate name, file rel, or group dir prefix
	IsGate   bool
	Group    bool
	Children []string // group fields: member rels in ledger order
	Default  any      // bool, or keepAsIs for a partially-off group
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

// BuildForm turns the interactive screen's Item list into an elicitation
// form: the Field list Diff later checks submissions against, plus the form's
// JSON schema as ordered raw bytes — hosts render properties in declaration
// order, so the form reads in the same dev-loop order as the screen. Only
// toggleable items become fields; tracked files and machinery stay on the
// status page. With expand, every group member becomes its own file field —
// the full per-file view of the status page — instead of one row per
// directory; the mixed-group enum disappears because each file is just a
// boolean.
func BuildForm(items []tui.Item, expand bool) ([]Field, json.RawMessage, error) {
	var fields []Field
	var props strings.Builder
	emit := func(f Field, title string) error {
		prop, err := propertyJSON(f, title)
		if err != nil {
			return err
		}
		if props.Len() > 0 {
			props.WriteByte(',')
		}
		props.WriteString(prop)
		fields = append(fields, f)
		return nil
	}
	for _, it := range items {
		if !it.Toggleable {
			continue
		}
		f := Field{Rel: it.Rel, IsGate: it.IsGate, Group: it.Group}
		title := fmt.Sprintf("[%s] %s", stageShort(it.Stage), it.Label)
		switch {
		case it.IsGate:
			f.Key = "gate:" + it.Rel
			f.Default = it.Enabled
			title = fmt.Sprintf("[%s] gate: %s", stageShort(it.Stage), it.Label)
		case it.Group && expand:
			for _, c := range it.Children {
				cf := Field{Key: "file:" + c.Rel, Rel: c.Rel, Default: c.Enabled}
				if err := emit(cf, fmt.Sprintf("[%s] %s", stageShort(it.Stage), c.Rel)); err != nil {
					return nil, nil, err
				}
			}
			continue
		case it.Group:
			f.Key = "dir:" + it.Rel
			for _, c := range it.Children {
				f.Children = append(f.Children, c.Rel)
			}
			on := 0
			for _, c := range it.Children {
				if c.Enabled {
					on++
				}
			}
			title = fmt.Sprintf("[%s] %s (%d files)", stageShort(it.Stage), it.Label, it.Count)
			if it.PartialOff {
				f.Default = keepAsIs
				title = fmt.Sprintf("%s — %d/%d on", title, on, it.Count)
			} else {
				f.Default = it.Enabled
			}
		default:
			f.Key = "file:" + it.Rel
			f.Default = it.Enabled
		}

		if err := emit(f, title); err != nil {
			return nil, nil, err
		}
	}
	schema := json.RawMessage(`{"type":"object","properties":{` + props.String() + `}}`)
	return fields, schema, nil
}

// propertyJSON renders one schema property, marshaling every dynamic string
// through encoding/json so titles and rels with quotes or backslashes stay
// valid JSON.
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
// The chain flows (Tasks 2-4) use this to preview the effect of an
// in-progress selection — one question answered, the next question's field
// list built from the result — without ever touching the caller's slice: a
// gate op flips that gate's Enabled; a group op flips every child's Enabled
// and recomputes the parent's Enabled/PartialOff; a file op flips either a
// standalone item or, if none matches, the group child with that Rel (then
// recomputes that group's Enabled/PartialOff). Unknown rels are ignored.
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

// stateByKey returns the current on/off state of every toggleable leaf,
// keyed the same way BuildForm keys its fields: "file:<rel>" for standalone
// files AND group children, "gate:<name>" for gates. Group parents ("dir:"
// rows are not leaves) and non-toggleable items are absent. A chain flow
// (Tasks 2-4) calls this once against the pre-chain items to get a baseline
// EffectiveOps can diff later steps' fields against.
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

// EffectiveOps computes the ops a chain flow (Tasks 2-4) needs to apply at
// the end of a chain, from an expanded-shape submission — every field here
// is a plain boolean, never the mixed-group enum, because a chain question
// only ever asks about leaves. A field's effective value is the submitted
// bool when present and well-typed, else the field's Default: a chain step
// the human never touched still carries forward whatever an earlier step in
// the same chain already set as that field's Default. One Op is emitted per
// field whose effective value differs from original[field.Key]; fields
// missing from original (not part of the pre-chain baseline) are skipped.
func EffectiveOps(fields []Field, content map[string]any, original map[string]bool) []Op {
	var ops []Op
	for _, f := range fields {
		orig, known := original[f.Key]
		if !known {
			continue
		}
		def, ok := f.Default.(bool)
		if !ok {
			// The mixed-group enum default (keepAsIs) is not part of the
			// all-boolean expanded shape this helper serves; skip rather
			// than guess at an enum's "effective" boolean.
			continue
		}
		effective := def
		if got, present := content[f.Key]; present {
			if b, ok := got.(bool); ok {
				effective = b
			}
		}
		if effective == orig {
			continue
		}
		ops = append(ops, Op{IsGate: f.IsGate, Group: f.Group, Rel: f.Rel, Children: f.Children, On: effective})
	}
	return ops
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
