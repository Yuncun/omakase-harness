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

// Enum values and synthetic keys for the triage variant's bulk-change and
// open-full-list rows (Task 2). bulkNone is the default — "don't touch
// anything the flagged rows above didn't already say" — and is spelled
// distinctly from keepAsIs because it means something different: keepAsIs
// preserves ONE mixed group's existing split, bulkNone leaves EVERY item
// alone.
const (
	bulkNone    = "no bulk change"
	bulkAllOn   = "everything on"
	bulkAllOff  = "everything off"
	keyBulk     = "bulk:"
	keyOpenFull = "open:full"
)

// Enum values and synthetic key for the preset variant's single posture
// question (Task 3). Order matters: BuildPresetForm's enum choices must
// appear in exactly this order. postureAllOn/postureAllOff happen to spell
// the same English phrases as bulkAllOn/bulkAllOff — the two variants never
// build the same schema, so the shared wording only matters to a human
// reading both, not to the code.
const (
	postureKeep      = "keep current"
	postureAllOn     = "everything on"
	postureGuards    = "guards only"
	postureAllOff    = "everything off"
	postureCustomize = "customize item-by-item…"
	keyPosture       = "posture:"
)

// Enum values, key prefix, and section-kind labels for the sections
// variant's per-stage question (Task 4). sectionKeep/sectionAllOn/
// sectionAllOff intentionally spell the same phrases as keepAsIs/allOn/
// allOff — same reasoning as postureAllOn/postureAllOff above, since the
// sections variant never builds the same schema as those. Because
// sectionKeep's VALUE collides with keepAsIs's, propertyJSON dispatches
// section fields by their keySection key prefix, not by matching this
// Default value against the other variants' enums the way it does for them.
const (
	sectionKeep   = "keep as-is"
	sectionAllOn  = "all on"
	sectionAllOff = "all off"
	sectionOpen   = "open this section…"
	keySection    = "section:"
)

// Field is one form property: the schema key, the item it controls, and the
// default sent to the client. Diff compares submissions against Default, so
// only fields the human actually changed become operations.
type Field struct {
	Key      string // property name: "gate:<name>", "file:<rel>", "dir:<prefix>", "section:<stageShort>"
	Rel      string // gate name, file rel, or group dir prefix
	IsGate   bool
	Group    bool
	Children []string  // group fields: member rels in ledger order
	Default  any       // bool, or keepAsIs/bulkNone/postureKeep/sectionKeep for an enum field
	Stage    tui.Stage // sections variant only: which stage this field's "open…" choice re-opens; zero value (StageSessionStart) is harmless noise on every other Field kind, which never reads it
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
	if strings.HasPrefix(f.Key, keySection) {
		// Dispatch by key prefix, not by matching Default's value: sectionKeep
		// deliberately spells the same string as keepAsIs, so a value-based
		// switch would render the wrong (3-choice) enum here.
		return fmt.Sprintf(`%s:{"type":"string","title":%s,"enum":["%s","%s","%s","%s"],"default":"%s"}`,
			key, t, sectionKeep, sectionAllOn, sectionAllOff, sectionOpen, sectionKeep), nil
	}
	if s, ok := f.Default.(string); ok {
		switch s {
		case keepAsIs:
			return fmt.Sprintf(`%s:{"type":"string","title":%s,"enum":["%s","%s","%s"],"default":"%s"}`,
				key, t, keepAsIs, allOn, allOff, keepAsIs), nil
		case bulkNone:
			return fmt.Sprintf(`%s:{"type":"string","title":%s,"enum":["%s","%s","%s"],"default":"%s"}`,
				key, t, bulkNone, bulkAllOn, bulkAllOff, bulkNone), nil
		case postureKeep:
			return fmt.Sprintf(`%s:{"type":"string","title":%s,"enum":["%s","%s","%s","%s","%s"],"default":"%s"}`,
				key, t, postureKeep, postureAllOn, postureGuards, postureAllOff, postureCustomize, postureKeep), nil
		}
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

// countToggleable counts top-level toggleable items — the same units
// BuildForm's collapsed shape emits as fields (a group is one item, not one
// per child) — for the preset and sections variants' headline messages. The
// triage variant counts LEAVES instead (via stateByKey), since its rows and
// its two synthetic rows' totals must agree with the per-leaf flagged count.
func countToggleable(items []tui.Item) int {
	n := 0
	for _, it := range items {
		if it.Toggleable {
			n++
		}
	}
	return n
}

// BuildTriageForm builds the triage variant's first form: a row for every
// item that needs attention (something off), plus a bulk-change row and an
// escape hatch to the full expanded list. An already-on file or gate and a
// fully-on group get no row — they're folded into the message's "on at
// defaults (hidden)" count instead. flagged counts only the rows built from
// actual items, not the two synthetic rows appended after them. The two
// synthetic rows' counts are LEAVES (stateByKey), the same unit flagged
// counts in — not top-level toggleable items (countToggleable) — so "ALL <n>
// items" and triageMessage's own total always agree, and the open-full row's
// count matches what BuildForm(expand=true) actually returns for form 2.
func BuildTriageForm(items []tui.Item) ([]Field, json.RawMessage, int, error) {
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
		switch {
		case it.IsGate:
			if it.Enabled {
				continue
			}
			f := Field{Key: "gate:" + it.Rel, Rel: it.Rel, IsGate: true, Default: false}
			title := fmt.Sprintf("[%s] gate: %s — currently off", stageShort(it.Stage), it.Label)
			if err := emit(f, title); err != nil {
				return nil, nil, 0, err
			}
		case it.Group && it.PartialOff:
			// Mixed group: one row per OFF child only — the on children stay
			// hidden, same as any other already-on item.
			for _, c := range it.Children {
				if c.Enabled {
					continue
				}
				f := Field{Key: "file:" + c.Rel, Rel: c.Rel, Default: false}
				title := fmt.Sprintf("[%s] %s — currently off", stageShort(it.Stage), c.Rel)
				if err := emit(f, title); err != nil {
					return nil, nil, 0, err
				}
			}
		case it.Group:
			if it.Enabled {
				continue // fully on: nothing to flag
			}
			f := Field{Key: "dir:" + it.Rel, Rel: it.Rel, Group: true, Default: false}
			for _, c := range it.Children {
				f.Children = append(f.Children, c.Rel)
			}
			title := fmt.Sprintf("[%s] %s (%d files) — currently off", stageShort(it.Stage), it.Label, it.Count)
			if err := emit(f, title); err != nil {
				return nil, nil, 0, err
			}
		default:
			if it.Enabled {
				continue
			}
			f := Field{Key: "file:" + it.Rel, Rel: it.Rel, Default: false}
			title := fmt.Sprintf("[%s] %s — currently off", stageShort(it.Stage), it.Label)
			if err := emit(f, title); err != nil {
				return nil, nil, 0, err
			}
		}
	}
	flagged := len(fields)

	total := len(stateByKey(items))
	bulkTitle := fmt.Sprintf("bulk change to ALL %d items", total)
	if err := emit(Field{Key: keyBulk, Default: bulkNone}, bulkTitle); err != nil {
		return nil, nil, 0, err
	}
	openTitle := fmt.Sprintf("open the full %d-item list next", total)
	if err := emit(Field{Key: keyOpenFull, Default: false}, openTitle); err != nil {
		return nil, nil, 0, err
	}

	schema := json.RawMessage(`{"type":"object","properties":{` + props.String() + `}}`)
	return fields, schema, flagged, nil
}

// groupDiff reports whether any child of the group item it differs from
// target, and returns the children's rels in ledger order for a group Op's
// Children field.
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

// TriageOps turns a triage-form submission into the ops to apply, plus
// whether the human asked to see the full list next. With no bulk row set,
// the flagged rows are the only source of ops — the trailing two synthetic
// fields are sliced off first so Diff never sees keyOpenFull's plain boolean
// (which has no real item behind it) or keyBulk's non-bool value.
//
// With a bulk row set, it targets EVERY toggleable item in `items`, not just
// the flagged ones — an already-on file, gate, or fully-on group is a bulk
// target too. A flagged row's own submission always wins over the bulk value
// for that same item. A mixed group with one overridden child can't be
// expressed as a single group Op (one Op is one On value), so it dissolves
// into one file Op per child instead: the untouched children get the bulk
// target, the overridden one gets its row value.
func TriageOps(fields []Field, content map[string]any, items []tui.Item) (ops []Op, openFull bool) {
	if v, ok := content[keyOpenFull].(bool); ok {
		openFull = v
	}

	flagged := fields
	if n := len(fields); n >= 2 {
		flagged = fields[:n-2]
	}

	var target bool
	bulkSet := true
	switch choice, _ := content[keyBulk].(string); choice {
	case bulkAllOn:
		target = true
	case bulkAllOff:
		target = false
	default:
		bulkSet = false
	}
	if !bulkSet {
		return Diff(flagged, content), openFull
	}

	overrides := map[string]bool{}
	for _, f := range flagged {
		got, present := content[f.Key]
		if !present {
			continue
		}
		want, ok := got.(bool)
		if !ok || want == f.Default.(bool) {
			continue
		}
		overrides[f.Key] = want
	}

	for _, it := range items {
		if !it.Toggleable {
			continue
		}
		switch {
		case it.IsGate:
			want := target
			if v, ok := overrides["gate:"+it.Rel]; ok {
				want = v
			}
			if want != it.Enabled {
				ops = append(ops, Op{IsGate: true, Rel: it.Rel, On: want})
			}
		case it.Group && it.PartialOff:
			childOverridden := false
			for _, c := range it.Children {
				if _, ok := overrides["file:"+c.Rel]; ok {
					childOverridden = true
					break
				}
			}
			if !childOverridden {
				if diff, children := groupDiff(it, target); diff {
					ops = append(ops, Op{Group: true, Rel: it.Rel, Children: children, On: target})
				}
				continue
			}
			for _, c := range it.Children {
				want := target
				if v, ok := overrides["file:"+c.Rel]; ok {
					want = v
				}
				if want != c.Enabled {
					ops = append(ops, Op{Rel: c.Rel, On: want})
				}
			}
		case it.Group:
			want := target
			if v, ok := overrides["dir:"+it.Rel]; ok {
				want = v
			}
			if diff, children := groupDiff(it, want); diff {
				ops = append(ops, Op{Group: true, Rel: it.Rel, Children: children, On: want})
			}
		default:
			want := target
			if v, ok := overrides["file:"+it.Rel]; ok {
				want = v
			}
			if want != it.Enabled {
				ops = append(ops, Op{Rel: it.Rel, On: want})
			}
		}
	}
	return ops, openFull
}

// BuildPresetForm builds the preset variant's only form: a single posture
// question rather than a per-item checklist. items is accepted for
// signature symmetry with the other Build* functions (and so a future
// posture could read the repo's shape), but today's five postures don't
// depend on it — presetFlow's message, not this schema, is what reports the
// current on/off split.
func BuildPresetForm(items []tui.Item) ([]Field, json.RawMessage, error) {
	f := Field{Key: keyPosture, Default: postureKeep}
	prop, err := propertyJSON(f, "posture")
	if err != nil {
		return nil, nil, err
	}
	schema := json.RawMessage(`{"type":"object","properties":{` + prop + `}}`)
	return []Field{f}, schema, nil
}

// PresetOps turns a posture choice into the ops to apply. keepCurrent and
// any unrecognized string (a stale reply, a client that ignores the enum)
// both mean "change nothing" — customize item-by-item… is handled entirely
// by presetFlow's chain to the full form and never reaches here. The three
// one-shot postures each reduce to a per-item target: everything on/off
// targets every toggleable item the same way; guards only targets gates ON
// (defenses stay running) and every standalone file or group OFF (the
// injected-instructions noise this posture strips). A group is one op for
// the whole group — groupDiff's "does any child differ from target" check —
// same as PresetOps' sibling TriageOps' bulk path, so a mixed group with any
// child off gets pulled fully to target rather than left half-mixed. Ops are
// only emitted where the target differs from the item's current state.
func PresetOps(choice string, items []tui.Item) []Op {
	var target func(it tui.Item) bool
	switch choice {
	case postureAllOn:
		target = func(tui.Item) bool { return true }
	case postureAllOff:
		target = func(tui.Item) bool { return false }
	case postureGuards:
		target = func(it tui.Item) bool { return it.IsGate }
	default:
		return nil
	}

	var ops []Op
	for _, it := range items {
		if !it.Toggleable {
			continue
		}
		want := target(it)
		switch {
		case it.IsGate:
			if want != it.Enabled {
				ops = append(ops, Op{IsGate: true, Rel: it.Rel, On: want})
			}
		case it.Group:
			if diff, children := groupDiff(it, want); diff {
				ops = append(ops, Op{Group: true, Rel: it.Rel, Children: children, On: want})
			}
		default:
			if want != it.Enabled {
				ops = append(ops, Op{Rel: it.Rel, On: want})
			}
		}
	}
	return ops
}

// sectionCounts reports one stage's field-title ingredients: kind (the
// literal word "gates" when every toggleable top-level item in the stage is
// a gate, else "items"), n (toggleable LEAVES — a group's children count
// individually, the group row itself does not), and on (how many of those
// leaves are currently enabled). BuildSectionsForm's field titles and
// sectionsFlow's sub-form messages both need the same three numbers.
func sectionCounts(items []tui.Item, stage tui.Stage) (kind string, n, on int) {
	kind = "gates"
	found := false
	for _, it := range items {
		if !it.Toggleable || it.Stage != stage {
			continue
		}
		found = true
		if !it.IsGate {
			kind = "items"
		}
		if it.Group {
			for _, c := range it.Children {
				n++
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
	if !found {
		kind = "items"
	}
	return kind, n, on
}

// BuildSectionsForm builds the sections variant's first form: one enum field
// per dev-loop stage that has at least one toggleable item — keep as-is by
// default, or bulk the whole stage on/off, or open a follow-up form scoped
// to just that stage. Declaration order is dev-loop stage order, the same
// rule as every other Build* function; the loop bound matches
// tui/model.go's visible()/render() section iteration
// (StageSessionStart..StageOther).
func BuildSectionsForm(items []tui.Item) ([]Field, json.RawMessage, error) {
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

	for s := tui.StageSessionStart; s <= tui.StageOther; s++ {
		has := false
		for _, it := range items {
			if it.Toggleable && it.Stage == s {
				has = true
				break
			}
		}
		if !has {
			continue
		}
		kind, n, on := sectionCounts(items, s)
		f := Field{Key: keySection + stageShort(s), Stage: s, Default: sectionKeep}
		title := fmt.Sprintf("[%s] %s (%d) — %d/%d on", stageShort(s), kind, n, on, n)
		if err := emit(f, title); err != nil {
			return nil, nil, err
		}
	}

	schema := json.RawMessage(`{"type":"object","properties":{` + props.String() + `}}`)
	return fields, schema, nil
}

// BuildSectionForm builds one stage's expanded-shape boolean rows — the same
// per-child dissolution BuildForm's expand path uses (groups dissolve into
// one field per member) — scoped to just this stage's items. A sections
// sub-form always uses this expanded shape, never the mixed-group enum,
// because a stage that reaches this form was already picked "open this
// section…" over "all on"/"all off" specifically to edit individual members.
func BuildSectionForm(items []tui.Item, stage tui.Stage) ([]Field, json.RawMessage, error) {
	var scoped []tui.Item
	for _, it := range items {
		if it.Stage == stage {
			scoped = append(scoped, it)
		}
	}
	return BuildForm(scoped, true)
}

// SectionBulkOps targets one stage's toggleable items at a single on/off
// value: a group becomes one group Op (groupDiff's "does any child differ
// from target" check — the same rule PresetOps and TriageOps' bulk path
// use), a gate or standalone file becomes one Op. Only items that actually
// differ from the target produce an Op.
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
