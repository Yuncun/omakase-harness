package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/tui"
)

func sampleItems() []tui.Item {
	return []tui.Item{
		{Label: "AGENTS.md", Rel: "AGENTS.md", Stage: tui.StageSessionStart, Toggleable: true, Enabled: true},
		{Label: ".claude/skills/", Rel: ".claude/skills", Stage: tui.StageOnDemand, Group: true, Toggleable: true,
			Children: []tui.ChildRef{{Rel: ".claude/skills/a.md", Enabled: true}, {Rel: ".claude/skills/b.md", Enabled: false}},
			Enabled:  false, PartialOff: true, Count: 2},
		{Label: "smoke", Rel: "smoke", Stage: tui.StagePreCommit, IsGate: true, Toggleable: true, Enabled: true},
		{Label: "tracked.md", Rel: "tracked.md", Stage: tui.StageSessionStart, Toggleable: false, Enabled: true},
	}
}

// A gate wired at both pre-commit and pre-push surfaces as two gate items with
// the same Rel; dedupeGates must collapse them to one so the form emits one
// "gate:<name>" field (no duplicate schema key) and Diff produces one op — with
// a label that still names both hooks. (Fix F / finding 8)
func TestDedupeGatesOneFieldPerName(t *testing.T) {
	items := []tui.Item{
		{Label: "smoke", Rel: "smoke", Stage: tui.StagePreCommit, IsGate: true, Toggleable: true, Enabled: true},
		{Label: "smoke", Rel: "smoke", Stage: tui.StagePrePush, IsGate: true, Toggleable: true, Enabled: true},
	}
	deduped := dedupeGates(items)

	nGate, label := 0, ""
	for _, it := range deduped {
		if it.IsGate {
			nGate++
			label = it.Label
		}
	}
	if nGate != 1 {
		t.Fatalf("dedupeGates left %d gate items, want 1", nGate)
	}
	if !strings.Contains(label, "pre-commit") || !strings.Contains(label, "pre-push") {
		t.Errorf("deduped gate label %q does not name both hooks", label)
	}

	fields, schema, err := BuildForm(deduped, false)
	if err != nil {
		t.Fatal(err)
	}
	gateFields := 0
	for _, f := range fields {
		if f.IsGate {
			gateFields++
		}
	}
	if gateFields != 1 {
		t.Fatalf("BuildForm emitted %d gate fields, want 1", gateFields)
	}
	if n := strings.Count(string(schema), `"gate:smoke"`); n != 1 {
		t.Errorf("schema has %d 'gate:smoke' keys, want 1:\n%s", n, schema)
	}
	if ops := Diff(fields, map[string]any{"gate:smoke": false}); len(ops) != 1 {
		t.Fatalf("Diff produced %d ops, want 1: %+v", len(ops), ops)
	}
}

// Only toggleable items become fields, one header per stage plus its
// children; the tracked row is absent.
func TestBuildFormFieldsAndKeys(t *testing.T) {
	fields, schema, err := BuildForm(sampleItems(), false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	wantKeys := []string{
		"stage:session start", "file:AGENTS.md",
		"stage:on demand", "dir:.claude/skills",
		"stage:pre-commit", "gate:smoke",
	}
	if len(fields) != len(wantKeys) {
		t.Fatalf("fields = %d, want %d (tracked row excluded): %+v", len(fields), len(wantKeys), fields)
	}
	for i, w := range wantKeys {
		if fields[i].Key != w {
			t.Errorf("fields[%d].Key = %q, want %q", i, fields[i].Key, w)
		}
	}
	if s := string(schema); strings.Contains(s, "tracked.md") {
		t.Errorf("schema contains non-toggleable row: %s", s)
	}
}

// A stage with no toggleable items (during session, pre-push, other, for
// sampleItems) contributes no header and no child fields at all.
func TestBuildFormSkipsEmptyStages(t *testing.T) {
	fields, schema, err := BuildForm(sampleItems(), false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	for _, absent := range []string{"stage:during session", "stage:pre-push", "stage:other"} {
		for _, f := range fields {
			if f.Key == absent {
				t.Errorf("field %q present, want no fields for an empty stage", absent)
			}
		}
		if strings.Contains(string(schema), `"`+absent+`"`) {
			t.Errorf("schema contains %q, want absent", absent)
		}
	}
}

// The raw schema keeps declaration order: each stage's header immediately
// followed by its children, stages in dev-loop order, because hosts render
// properties in declaration order.
func TestBuildFormPreservesOrder(t *testing.T) {
	_, schema, err := BuildForm(sampleItems(), false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	s := string(schema)
	positions := map[string]int{}
	for _, key := range []string{"stage:session start", "file:AGENTS.md", "stage:on demand", "dir:.claude/skills", "stage:pre-commit", "gate:smoke"} {
		i := strings.Index(s, `"`+key+`"`)
		if i < 0 {
			t.Fatalf("schema missing %q:\n%s", key, s)
		}
		positions[key] = i
	}
	last := -1
	for _, key := range []string{"stage:session start", "file:AGENTS.md", "stage:on demand", "dir:.claude/skills", "stage:pre-commit", "gate:smoke"} {
		if positions[key] < last {
			t.Errorf("key %q out of declared order:\n%s", key, s)
		}
		last = positions[key]
	}
	var v map[string]any
	if err := json.Unmarshal(schema, &v); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
}

// A header field is always the keepAsIs/allOn/allOff enum defaulting to
// keepAsIs, titled with the leaf-counted "(n/m on)"; leaf rows (a standalone
// file, a gate) default to their current on/off state as a real boolean
// (hosts render a checkbox for these, not visible text, but the host draws
// the form so that's the host's concern); a partially-off collapsed group is
// still the 3-choice enum, since a boolean can't express "keep 5/9 on".
func TestBuildFormDefaults(t *testing.T) {
	fields, schema, err := BuildForm(sampleItems(), false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	if d, ok := fields[0].Default.(string); !ok || d != keepAsIs {
		t.Errorf("session-start header default = %v, want %q", fields[0].Default, keepAsIs)
	}
	if d, ok := fields[1].Default.(bool); !ok || !d {
		t.Errorf("file default = %v, want true", fields[1].Default)
	}
	if d, ok := fields[3].Default.(string); !ok || d != keepAsIs {
		t.Errorf("partial group default = %v, want %q", fields[3].Default, keepAsIs)
	}
	if d, ok := fields[5].Default.(bool); !ok || !d {
		t.Errorf("gate default = %v, want true", fields[5].Default)
	}
	s := string(schema)
	for _, want := range []string{`"keep as-is"`, `"all on"`, `"all off"`, `"type":"boolean"`, `"default":true`} {
		if !strings.Contains(s, want) {
			t.Errorf("schema missing %s:\n%s", want, s)
		}
	}
}

// Header titles carry LEAF counts (a group's children, never 1) and name
// "gates" only for a stage whose toggleable items are all gates — pre-commit
// and pre-push, the only stages BuildItems ever assigns a gate to; every
// other stage's header omits the word entirely.
func TestBuildFormHeaderTitlesCountLeavesAndNameGates(t *testing.T) {
	_, schema, err := BuildForm(sampleItems(), false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	var decoded struct {
		Properties map[string]struct {
			Title   string   `json:"title"`
			Enum    []string `json:"enum"`
			Default any      `json:"default"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &decoded); err != nil {
		t.Fatalf("decode schema: %v\n%s", err, schema)
	}
	cases := []struct{ key, wantSubstr string }{
		{"stage:session start", "(1/1 on)"}, // AGENTS.md, on
		{"stage:on demand", "(1/2 on)"},      // .claude/skills: a on, b off
		{"stage:pre-commit", "gates (1/1 on)"},
	}
	for _, c := range cases {
		p, ok := decoded.Properties[c.key]
		if !ok {
			t.Fatalf("schema missing %q:\n%s", c.key, schema)
		}
		if !strings.Contains(p.Title, c.wantSubstr) {
			t.Errorf("%s title = %q, want substring %q", c.key, p.Title, c.wantSubstr)
		}
		if p.Default != keepAsIs {
			t.Errorf("%s default = %v, want %q", c.key, p.Default, keepAsIs)
		}
		wantEnum := []string{keepAsIs, allOn, allOff}
		if len(p.Enum) != len(wantEnum) {
			t.Fatalf("%s enum = %v, want %v", c.key, p.Enum, wantEnum)
		}
		for i, w := range wantEnum {
			if p.Enum[i] != w {
				t.Errorf("%s enum[%d] = %q, want %q", c.key, i, p.Enum[i], w)
			}
		}
	}
	if strings.Contains(decoded.Properties["stage:on demand"].Title, "gates") {
		t.Errorf("on-demand header wrongly says gates: %q", decoded.Properties["stage:on demand"].Title)
	}
}

// Child rows are prefixed "· ", and a collapsed group's title carries the
// substring a human relies on to tell a uniform group from a partial one at
// a glance: "(N files)" alone for uniform, "(N files) — n/m on" for partial.
func TestBuildFormChildAndGroupTitleCopy(t *testing.T) {
	_, partialSchema, err := BuildForm(sampleItems(), false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	var partialDecoded struct {
		Properties map[string]struct {
			Title string `json:"title"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(partialSchema, &partialDecoded); err != nil {
		t.Fatalf("decode partial schema: %v\n%s", err, partialSchema)
	}
	cases := []struct{ key, wantSubstr string }{
		{"file:AGENTS.md", "· "},
		{"gate:smoke", "· "},
		{"dir:.claude/skills", "(2 files) — 1/2 on"},
	}
	for _, c := range cases {
		p, ok := partialDecoded.Properties[c.key]
		if !ok {
			t.Fatalf("schema missing %q:\n%s", c.key, partialSchema)
		}
		if !strings.HasPrefix(p.Title, "· ") {
			t.Errorf("%s title = %q, want prefix %q", c.key, p.Title, "· ")
		}
		if !strings.Contains(p.Title, c.wantSubstr) {
			t.Errorf("%s title = %q, want substring %q", c.key, p.Title, c.wantSubstr)
		}
	}

	uniform := []tui.Item{{
		Label: ".claude/skills/", Rel: ".claude/skills", Stage: tui.StageOnDemand, Group: true, Toggleable: true,
		Children: []tui.ChildRef{{Rel: ".claude/skills/a.md", Enabled: true}, {Rel: ".claude/skills/b.md", Enabled: true}},
		Enabled:  true, Count: 2,
	}}
	_, uniformSchema, err := BuildForm(uniform, false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	var uniformDecoded struct {
		Properties map[string]struct {
			Title string `json:"title"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(uniformSchema, &uniformDecoded); err != nil {
		t.Fatalf("decode uniform schema: %v\n%s", err, uniformSchema)
	}
	up, ok := uniformDecoded.Properties["dir:.claude/skills"]
	if !ok {
		t.Fatalf("schema missing dir:.claude/skills:\n%s", uniformSchema)
	}
	if !strings.Contains(up.Title, "(2 files)") {
		t.Errorf("uniform group title = %q, want substring %q", up.Title, "(2 files)")
	}
	if strings.Contains(up.Title, "—") {
		t.Errorf("uniform group title = %q, wrongly carries the partial-group on/m suffix", up.Title)
	}
}

// Diff: unchanged, missing, and keep-as-is values emit nothing; real changes
// emit one Op each carrying the group children for Task 3's apply loop.
func TestDiff(t *testing.T) {
	fields, _, err := BuildForm(sampleItems(), false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}

	if ops := Diff(fields, map[string]any{}); len(ops) != 0 {
		t.Errorf("empty content: ops = %+v, want none", ops)
	}
	same := map[string]any{"file:AGENTS.md": true, "dir:.claude/skills": "keep as-is", "gate:smoke": true}
	if ops := Diff(fields, same); len(ops) != 0 {
		t.Errorf("all-defaults content: ops = %+v, want none", ops)
	}

	changed := map[string]any{"file:AGENTS.md": false, "dir:.claude/skills": "all off", "gate:smoke": true}
	ops := Diff(fields, changed)
	if len(ops) != 2 {
		t.Fatalf("ops = %+v, want 2", ops)
	}
	if ops[0].IsGate || ops[0].Group || ops[0].Rel != "AGENTS.md" || ops[0].On {
		t.Errorf("ops[0] = %+v, want file AGENTS.md -> off", ops[0])
	}
	if !ops[1].Group || ops[1].Rel != ".claude/skills" || ops[1].On ||
		len(ops[1].Children) != 2 || ops[1].Children[0] != ".claude/skills/a.md" {
		t.Errorf("ops[1] = %+v, want group .claude/skills -> all off with 2 children", ops[1])
	}
}

// A wrong-typed (string, not bool) value on an item row's boolean field emits
// no op — Diff's bool branch requires an actual bool, so a stray string can
// never be misread as a toggle.
func TestDiffItemRowJunkChoiceIsNoOp(t *testing.T) {
	fields, _, err := BuildForm(sampleItems(), false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	ops := Diff(fields, map[string]any{"file:AGENTS.md": "banana"})
	if len(ops) != 0 {
		t.Errorf("junk choice: ops = %+v, want none", ops)
	}
}

// A junk string on a partial group's enum field (something other than the
// three declared choices) emits no op — Diff must not treat "anything but
// keep as-is" as "all off". This only matters if a future SDK relaxes the
// server-side enum validation that rejects junk today; the test guards the
// hardened branch directly at the Diff layer.
func TestDiffPartialGroupJunkChoiceIsNoOp(t *testing.T) {
	fields, _, err := BuildForm(sampleItems(), false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	ops := Diff(fields, map[string]any{"dir:.claude/skills": "banana"})
	if len(ops) != 0 {
		t.Errorf("junk choice: ops = %+v, want none", ops)
	}
}

// A fully-on (uniform) collapsed group is a plain boolean field, not the
// 3-choice mixed enum — that enum only exists to express a PARTIAL split —
// and turning it off diffs to one group Op.
func TestBuildFormUniformGroupBoolean(t *testing.T) {
	items := []tui.Item{{
		Label: ".claude/skills/", Rel: ".claude/skills", Stage: tui.StageOnDemand, Group: true, Toggleable: true,
		Children: []tui.ChildRef{{Rel: ".claude/skills/a.md", Enabled: true}, {Rel: ".claude/skills/b.md", Enabled: true}},
		Enabled:  true, Count: 2,
	}}
	fields, _, err := BuildForm(items, false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	// fields[0] is the on-demand stage header; fields[1] is the group's own row.
	if len(fields) != 2 || fields[1].Key != "dir:.claude/skills" {
		t.Fatalf("fields = %+v, want [header, dir:.claude/skills]", fields)
	}
	if d, ok := fields[1].Default.(bool); !ok || !d {
		t.Fatalf("uniform group default = %v, want true", fields[1].Default)
	}
	ops := Diff(fields, map[string]any{"dir:.claude/skills": false})
	if len(ops) != 1 || !ops[0].Group || ops[0].On {
		t.Errorf("ops = %+v, want one group off Op", ops)
	}
}

// With expand, groups dissolve into one boolean file field per member — the
// full per-file view — keeping declaration order (stage headers still lead
// each stage) and per-child defaults; no dir: rows survive, even for a mixed
// group, since each file gets its own boolean row instead.
func TestBuildFormExpanded(t *testing.T) {
	fields, schema, err := BuildForm(sampleItems(), true)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	wantKeys := []string{
		"stage:session start", "file:AGENTS.md",
		"stage:on demand", "file:.claude/skills/a.md", "file:.claude/skills/b.md",
		"stage:pre-commit", "gate:smoke",
	}
	if len(fields) != len(wantKeys) {
		t.Fatalf("fields = %d, want %d: %+v", len(fields), len(wantKeys), fields)
	}
	for i, w := range wantKeys {
		if fields[i].Key != w {
			t.Errorf("fields[%d].Key = %q, want %q", i, fields[i].Key, w)
		}
	}
	if d, ok := fields[4].Default.(bool); !ok || d {
		t.Errorf("disabled child default = %v, want false", fields[4].Default)
	}
	s := string(schema)
	if strings.Contains(s, "dir:") {
		t.Errorf("expanded schema still has a group field:\n%s", s)
	}
	iA := strings.Index(s, `"file:.claude/skills/a.md"`)
	iGate := strings.Index(s, `"gate:smoke"`)
	if iA < 0 || iGate < 0 || iA > iGate {
		t.Errorf("expanded schema order wrong (a.md=%d gate=%d):\n%s", iA, iGate, s)
	}

	ops := Diff(fields, map[string]any{"file:.claude/skills/b.md": true})
	if len(ops) != 1 || ops[0].Group || ops[0].IsGate || ops[0].Rel != ".claude/skills/b.md" || !ops[0].On {
		t.Errorf("ops = %+v, want one file on Op for b.md", ops)
	}
}

// ellipsizeRel leaves a short rel untouched and middle-truncates a long one
// to exactly max runes, ellipsis included — the truncation this task's
// title-padding relies on to bound a long path before padding runs.
func TestEllipsizeRel(t *testing.T) {
	if got := ellipsizeRel("short.md", 48); got != "short.md" {
		t.Errorf("short rel changed: %q", got)
	}
	long := strings.Repeat("x", 60)
	got := ellipsizeRel(long, 48)
	if n := len([]rune(got)); n != 48 {
		t.Fatalf("ellipsized length = %d, want 48: %q", n, got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("ellipsized rel missing …: %q", got)
	}
}

// padTitles right-pads every title with spaces to the widest one's rune
// count, so a host rendering titles verbatim in a monospace terminal still
// lines up the "title: value" column across every row.
func TestPadTitles(t *testing.T) {
	got := padTitles([]string{"a", "bb", "ccc"})
	want := []string{"a  ", "bb ", "ccc"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("padTitles()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// BuildForm's own titles: a rel long enough to need truncation (60 runes) is
// middle-ellipsized to 48 before padding, and every title in the resulting
// form — headers included — pads out to one common rune width.
func TestBuildFormTitlesPaddedAndEllipsized(t *testing.T) {
	longRel := strings.Repeat("a", 60)
	items := []tui.Item{
		{Label: "x.md", Rel: "x.md", Stage: tui.StageSessionStart, Toggleable: true, Enabled: true},
		{Label: longRel, Rel: longRel, Stage: tui.StageSessionStart, Toggleable: true, Enabled: true},
	}
	_, schema, err := BuildForm(items, false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	if !json.Valid(schema) {
		t.Fatalf("schema is not valid JSON:\n%s", schema)
	}
	var decoded struct {
		Properties map[string]struct {
			Title string `json:"title"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &decoded); err != nil {
		t.Fatalf("decode schema: %v\n%s", err, schema)
	}
	p, ok := decoded.Properties["file:"+longRel]
	if !ok {
		t.Fatalf("schema missing file:%s:\n%s", longRel, schema)
	}
	longTitle := p.Title
	if strings.Contains(longTitle, strings.Repeat("a", 49)) {
		t.Errorf("60-rune rel not ellipsized: %q", longTitle)
	}
	if !strings.Contains(longTitle, "…") {
		t.Errorf("long title missing an ellipsis: %q", longTitle)
	}

	width := -1
	for key, p := range decoded.Properties {
		w := len([]rune(p.Title))
		if width == -1 {
			width = w
			continue
		}
		if w != width {
			t.Errorf("title width mismatch: %q (key %s) has %d runes, want %d", p.Title, key, w, width)
		}
	}
}

// findItem locates rel's item in an Item slice, or -1 — a small test helper
// for asserting on ApplyOps output.
func findItem(items []tui.Item, rel string) int {
	for i, it := range items {
		if it.Rel == rel {
			return i
		}
	}
	return -1
}

// ApplyOps is the chain flows' (Tasks 2-4) preview step: it applies a batch
// of in-progress ops to a copy of the screen's items so the next question in
// a chain can be built against the post-op state, without ever touching the
// caller's slice.
func TestApplyOps(t *testing.T) {
	t.Run("gate flip", func(t *testing.T) {
		items := sampleItems()
		out := ApplyOps(items, []Op{{IsGate: true, Rel: "smoke", On: false}})
		if i := findItem(out, "smoke"); i < 0 || out[i].Enabled {
			t.Fatalf("gate smoke not flipped off: %+v", out)
		}
		if i := findItem(items, "smoke"); i < 0 || !items[i].Enabled {
			t.Errorf("original items mutated: %+v", items)
		}
	})

	t.Run("group off sets both children off and parent Enabled=false PartialOff=false", func(t *testing.T) {
		items := sampleItems()
		out := ApplyOps(items, []Op{{
			Group: true, Rel: ".claude/skills",
			Children: []string{".claude/skills/a.md", ".claude/skills/b.md"}, On: false,
		}})
		i := findItem(out, ".claude/skills")
		if i < 0 {
			t.Fatal("group item missing from output")
		}
		g := out[i]
		if g.Children[0].Enabled || g.Children[1].Enabled {
			t.Errorf("children = %+v, want both off", g.Children)
		}
		if g.Enabled || g.PartialOff {
			t.Errorf("group state = Enabled=%v PartialOff=%v, want false/false", g.Enabled, g.PartialOff)
		}
	})

	t.Run("single child on in a fully-off group leaves it partial with correct counts", func(t *testing.T) {
		items := []tui.Item{{
			Label: ".claude/skills/", Rel: ".claude/skills", Group: true, Toggleable: true,
			Children: []tui.ChildRef{{Rel: ".claude/skills/a.md", Enabled: false}, {Rel: ".claude/skills/b.md", Enabled: false}},
			Enabled:  false, Count: 2,
		}}
		out := ApplyOps(items, []Op{{Rel: ".claude/skills/a.md", On: true}})
		g := out[0]
		if !g.Children[0].Enabled || g.Children[1].Enabled {
			t.Errorf("children = %+v, want only a.md on", g.Children)
		}
		if g.Enabled || !g.PartialOff {
			t.Errorf("group state = Enabled=%v PartialOff=%v, want false/true (1 of 2 on)", g.Enabled, g.PartialOff)
		}
	})

	t.Run("child off in a fully-on group becomes partial", func(t *testing.T) {
		items := []tui.Item{{
			Label: ".claude/skills/", Rel: ".claude/skills", Group: true, Toggleable: true,
			Children: []tui.ChildRef{{Rel: ".claude/skills/a.md", Enabled: true}, {Rel: ".claude/skills/b.md", Enabled: true}},
			Enabled:  true, Count: 2,
		}}
		out := ApplyOps(items, []Op{{Rel: ".claude/skills/b.md", On: false}})
		g := out[0]
		if !g.Children[0].Enabled || g.Children[1].Enabled {
			t.Errorf("children = %+v, want a.md on, b.md off", g.Children)
		}
		if g.Enabled || !g.PartialOff {
			t.Errorf("group state = Enabled=%v PartialOff=%v, want false/true", g.Enabled, g.PartialOff)
		}
	})

	t.Run("original slice and its Children are never mutated", func(t *testing.T) {
		items := sampleItems()
		_ = ApplyOps(items, []Op{
			{IsGate: true, Rel: "smoke", On: false},
			{Rel: ".claude/skills/b.md", On: true},
			{Group: true, Rel: ".claude/skills", Children: []string{".claude/skills/a.md", ".claude/skills/b.md"}, On: false},
		})
		want := sampleItems()
		if !items[2].Enabled {
			t.Errorf("original gate mutated: %+v", items[2])
		}
		if items[1].Children[1].Enabled != want[1].Children[1].Enabled || items[1].Children[0].Enabled != want[1].Children[0].Enabled {
			t.Errorf("original group children mutated: %+v", items[1].Children)
		}
		if items[1].Enabled != want[1].Enabled || items[1].PartialOff != want[1].PartialOff {
			t.Errorf("original group state mutated: Enabled=%v PartialOff=%v", items[1].Enabled, items[1].PartialOff)
		}
	})

	t.Run("unknown rel is ignored", func(t *testing.T) {
		items := sampleItems()
		out := ApplyOps(items, []Op{{Rel: "nonexistent.md", On: true}})
		if len(out) != len(items) {
			t.Fatalf("length changed: %d vs %d", len(out), len(items))
		}
		if i := findItem(out, "AGENTS.md"); i < 0 || !out[i].Enabled {
			t.Errorf("unrelated item disturbed by unknown-rel op: %+v", out)
		}
	})
}

// stateByKey is the flat on/off lookup EffectiveOps diffs submissions
// against: one entry per toggleable leaf, keyed the same way BuildForm keys
// its fields, so a chain flow can look up "was this leaf on before the
// chain started" regardless of which step is currently being asked.
func TestStateByKey(t *testing.T) {
	got := stateByKey(sampleItems())
	want := map[string]bool{
		"file:AGENTS.md":           true,
		"file:.claude/skills/a.md": true,
		"file:.claude/skills/b.md": false,
		"gate:smoke":               true,
	}
	if len(got) != len(want) {
		t.Fatalf("stateByKey = %+v, want %+v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("stateByKey[%q] = %v, want %v", k, got[k], v)
		}
	}
	if _, ok := got["dir:.claude/skills"]; ok {
		t.Errorf("stateByKey has a dir: entry, want none: %+v", got)
	}
	if _, ok := got["file:tracked.md"]; ok {
		t.Errorf("stateByKey includes non-toggleable tracked.md, want absent: %+v", got)
	}
}

// EffectiveOps is how a chain flow (Tasks 2-4) turns a partial, expanded-shape
// submission into ops at the end of the chain: the field list reflects
// whatever earlier chain steps already changed (via ApplyOps), so a field's
// Default can differ from the pre-chain original even when the human never
// touched that particular question.
//
// EffectiveOps' own contract (string rowEnabled/rowDisabled defaults) predates
// this task and is untouched by it — BuildForm's leaves are booleans now, so
// this fixture is built by hand instead of via BuildForm(interim, true), which
// no longer produces the shape EffectiveOps expects. See Task 1's report for
// why that's out of this task's scope.
func TestEffectiveOps(t *testing.T) {
	base := sampleItems()
	original := stateByKey(base)

	// Simulates a chain flow that already flipped the gate off in an earlier
	// step: its Default (rowDisabled) differs from the pre-chain original
	// (true) — exactly the case EffectiveOps must carry forward.
	fields := []Field{
		{Key: "file:AGENTS.md", Rel: "AGENTS.md", Default: rowEnabled},
		{Key: "file:.claude/skills/a.md", Rel: ".claude/skills/a.md", Default: rowEnabled},
		{Key: "file:.claude/skills/b.md", Rel: ".claude/skills/b.md", Default: rowDisabled},
		{Key: "gate:smoke", Rel: "smoke", IsGate: true, Default: rowDisabled},
	}

	t.Run("untouched content, default differs from original -> op emitted", func(t *testing.T) {
		ops := EffectiveOps(fields, map[string]any{}, original)
		if len(ops) != 1 || !ops[0].IsGate || ops[0].Rel != "smoke" || ops[0].On {
			t.Fatalf("ops = %+v, want one gate smoke -> off", ops)
		}
	})

	t.Run("submitted value overrides default back to original -> no op", func(t *testing.T) {
		ops := EffectiveOps(fields, map[string]any{"gate:smoke": rowEnabled}, original)
		if len(ops) != 0 {
			t.Errorf("ops = %+v, want none (submission restores original)", ops)
		}
	})

	t.Run("junk-valued submission falls back to default", func(t *testing.T) {
		ops := EffectiveOps(fields, map[string]any{"gate:smoke": "banana"}, original)
		if len(ops) != 1 || !ops[0].IsGate || ops[0].Rel != "smoke" || ops[0].On {
			t.Fatalf("ops = %+v, want default (off) to win over junk submission", ops)
		}
	})

	t.Run("junk-typed submission falls back to default", func(t *testing.T) {
		ops := EffectiveOps(fields, map[string]any{"gate:smoke": true}, original)
		if len(ops) != 1 || !ops[0].IsGate || ops[0].Rel != "smoke" || ops[0].On {
			t.Fatalf("ops = %+v, want default (off) to win over wrong-typed submission", ops)
		}
	})

	t.Run("field missing from original is skipped", func(t *testing.T) {
		extra := append([]Field{{Key: "file:new.md", Rel: "new.md", Default: rowEnabled}}, fields...)
		ops := EffectiveOps(extra, map[string]any{}, original)
		for _, op := range ops {
			if op.Rel == "new.md" {
				t.Errorf("op emitted for a field missing from original: %+v", op)
			}
		}
	})
}

// BuildTriageForm flags only what needs attention: the mixed group's one off
// child gets its own row (the on sibling gets none), the fully-on file and
// fully-on gate get no rows either, the two synthetic rows land last, and
// the non-toggleable tracked row is absent — same rule as BuildForm.
func TestBuildTriageForm(t *testing.T) {
	fields, schema, flagged, err := BuildTriageForm(sampleItems())
	if err != nil {
		t.Fatalf("BuildTriageForm: %v", err)
	}
	if flagged != 1 {
		t.Fatalf("flagged = %d, want 1: %+v", flagged, fields)
	}
	if len(fields) != 3 {
		t.Fatalf("fields = %d, want 3 (1 flagged + bulk + open): %+v", len(fields), fields)
	}
	if fields[0].Key != "file:.claude/skills/b.md" {
		t.Errorf("fields[0].Key = %q, want the off child's file row", fields[0].Key)
	}
	if fields[1].Key != keyBulk || fields[2].Key != keyOpenFull {
		t.Errorf("synthetic fields = %+v, want bulk then open, in that order and last", fields[1:])
	}
	s := string(schema)
	for _, want := range []string{`"` + bulkNone + `"`, `"` + bulkAllOn + `"`, `"` + bulkAllOff + `"`} {
		if !strings.Contains(s, want) {
			t.Errorf("schema missing bulk choice %s:\n%s", want, s)
		}
	}
	if strings.Contains(s, "tracked.md") {
		t.Errorf("schema contains non-toggleable row: %s", s)
	}
	iFlag := strings.Index(s, `"file:.claude/skills/b.md"`)
	iBulk := strings.Index(s, `"`+keyBulk+`"`)
	iOpen := strings.Index(s, `"`+keyOpenFull+`"`)
	if iFlag < 0 || iBulk < 0 || iOpen < 0 || !(iFlag < iBulk && iBulk < iOpen) {
		t.Errorf("schema order wrong (flag=%d bulk=%d open=%d):\n%s", iFlag, iBulk, iOpen, s)
	}
}

// A fully-enabled repo has nothing to flag: only the two synthetic rows
// survive.
func TestBuildTriageFormAllOnRepo(t *testing.T) {
	items := []tui.Item{
		{Label: "AGENTS.md", Rel: "AGENTS.md", Stage: tui.StageSessionStart, Toggleable: true, Enabled: true},
		{Label: ".claude/skills/", Rel: ".claude/skills", Stage: tui.StageOnDemand, Group: true, Toggleable: true,
			Children: []tui.ChildRef{{Rel: ".claude/skills/a.md", Enabled: true}, {Rel: ".claude/skills/b.md", Enabled: true}},
			Enabled:  true, Count: 2},
		{Label: "smoke", Rel: "smoke", Stage: tui.StagePreCommit, IsGate: true, Toggleable: true, Enabled: true},
	}
	fields, _, flagged, err := BuildTriageForm(items)
	if err != nil {
		t.Fatalf("BuildTriageForm: %v", err)
	}
	if flagged != 0 {
		t.Fatalf("flagged = %d, want 0: %+v", flagged, fields)
	}
	if len(fields) != 2 || fields[0].Key != keyBulk || fields[1].Key != keyOpenFull {
		t.Fatalf("fields = %+v, want only bulk + open", fields)
	}
}

// A flagged row turned on, with no bulk change submitted, emits exactly one
// file Op — the same rule Diff applies to any single field.
func TestTriageOpsRowOnly(t *testing.T) {
	items := sampleItems()
	fields, _, _, err := BuildTriageForm(items)
	if err != nil {
		t.Fatalf("BuildTriageForm: %v", err)
	}
	ops, openFull := TriageOps(fields, map[string]any{"file:.claude/skills/b.md": rowEnabled}, items)
	if openFull {
		t.Errorf("openFull = true, want false")
	}
	if len(ops) != 1 || ops[0].Group || ops[0].IsGate || ops[0].Rel != ".claude/skills/b.md" || !ops[0].On {
		t.Fatalf("ops = %+v, want one file Op b.md -> on", ops)
	}
}

// Bulk "everything off" targets every toggleable item, not just the flagged
// ones: the already-on file and gate each get an Op, and the mixed group
// (no row overridden) collapses into one group Op since it has an on child.
func TestTriageOpsBulkAllOff(t *testing.T) {
	items := sampleItems()
	fields, _, _, err := BuildTriageForm(items)
	if err != nil {
		t.Fatalf("BuildTriageForm: %v", err)
	}
	ops, openFull := TriageOps(fields, map[string]any{keyBulk: bulkAllOff}, items)
	if openFull {
		t.Errorf("openFull = true, want false")
	}
	if len(ops) != 3 {
		t.Fatalf("ops = %+v, want 3 (file, group, gate)", ops)
	}
	byRel := map[string]Op{}
	for _, op := range ops {
		byRel[op.Rel] = op
	}
	if op, ok := byRel["AGENTS.md"]; !ok || op.On || op.Group || op.IsGate {
		t.Errorf("AGENTS.md op = %+v, want file -> off", op)
	}
	if op, ok := byRel[".claude/skills"]; !ok || op.On || !op.Group || len(op.Children) != 2 {
		t.Errorf("skills op = %+v, want group -> off with 2 children", op)
	}
	if op, ok := byRel["smoke"]; !ok || op.On || !op.IsGate {
		t.Errorf("smoke op = %+v, want gate -> off", op)
	}
}

// A mixed group's overridden child row can't be folded into a single group
// Op alongside a bulk change, so it dissolves into one file Op per child:
// the overridden child gets its row value, the untouched sibling gets the
// bulk target.
func TestTriageOpsRowOverridesBulk(t *testing.T) {
	items := sampleItems()
	fields, _, _, err := BuildTriageForm(items)
	if err != nil {
		t.Fatalf("BuildTriageForm: %v", err)
	}
	content := map[string]any{keyBulk: bulkAllOff, "file:.claude/skills/b.md": rowEnabled}
	ops, _ := TriageOps(fields, content, items)
	if len(ops) != 4 {
		t.Fatalf("ops = %+v, want 4 (AGENTS.md, a.md, b.md, smoke)", ops)
	}
	byRel := map[string]Op{}
	for _, op := range ops {
		byRel[op.Rel] = op
	}
	if op, ok := byRel[".claude/skills/a.md"]; !ok || op.On || op.Group {
		t.Errorf("a.md op = %+v, want file -> off", op)
	}
	if op, ok := byRel[".claude/skills/b.md"]; !ok || !op.On || op.Group {
		t.Errorf("b.md op = %+v, want file -> on", op)
	}
	if op, ok := byRel["AGENTS.md"]; !ok || op.On {
		t.Errorf("AGENTS.md op = %+v, want off", op)
	}
	if op, ok := byRel["smoke"]; !ok || op.On {
		t.Errorf("smoke op = %+v, want off", op)
	}
}

// open:full=true is returned regardless of what else was submitted, so a
// chain flow can carry the same-call's ops forward as the next form's
// pending defaults.
func TestTriageOpsOpenFull(t *testing.T) {
	items := sampleItems()
	fields, _, _, err := BuildTriageForm(items)
	if err != nil {
		t.Fatalf("BuildTriageForm: %v", err)
	}
	content := map[string]any{keyOpenFull: true, "file:.claude/skills/b.md": rowEnabled}
	ops, openFull := TriageOps(fields, content, items)
	if !openFull {
		t.Fatalf("openFull = false, want true")
	}
	if len(ops) != 1 || ops[0].Rel != ".claude/skills/b.md" || !ops[0].On {
		t.Fatalf("ops = %+v, want one file Op b.md -> on", ops)
	}
}

// BuildPresetForm's whole form is a single string-enum field: the five
// postures in declaration order, defaulting to keep current — the human
// answers one question instead of a per-item checklist.
func TestBuildPresetForm(t *testing.T) {
	fields, schema, err := BuildPresetForm(sampleItems())
	if err != nil {
		t.Fatalf("BuildPresetForm: %v", err)
	}
	if len(fields) != 1 || fields[0].Key != keyPosture {
		t.Fatalf("fields = %+v, want exactly one %q field", fields, keyPosture)
	}
	if d, ok := fields[0].Default.(string); !ok || d != postureKeep {
		t.Errorf("default = %v, want %q", fields[0].Default, postureKeep)
	}
	s := string(schema)
	if !strings.Contains(s, `"title":"posture"`) {
		t.Errorf("schema missing posture title:\n%s", s)
	}
	wantOrder := []string{postureKeep, postureAllOn, postureGuards, postureAllOff, postureCustomize}
	last := -1
	for _, w := range wantOrder {
		i := strings.Index(s, `"`+w+`"`)
		if i < 0 {
			t.Fatalf("schema missing choice %q:\n%s", w, s)
		}
		if i < last {
			t.Errorf("choice %q out of declared order in schema:\n%s", w, s)
		}
		last = i
	}
}

// keep current asks for no change at all: the posture question is purely
// informational in that branch, so PresetOps must emit nothing.
func TestPresetOpsKeepCurrent(t *testing.T) {
	if ops := PresetOps(postureKeep, sampleItems()); ops != nil {
		t.Errorf("ops = %+v, want nil", ops)
	}
}

// guards only keeps consent gates running (already-on smoke gets no op) and
// strips everything else — the standalone file and the mixed group both
// collapse to a single off op each; the non-toggleable tracked row never
// enters the picture.
func TestPresetOpsGuardsOnly(t *testing.T) {
	items := sampleItems()
	ops := PresetOps(postureGuards, items)
	if len(ops) != 2 {
		t.Fatalf("ops = %+v, want 2 (file off + group off)", ops)
	}
	byRel := map[string]Op{}
	for _, op := range ops {
		byRel[op.Rel] = op
	}
	if op, ok := byRel["AGENTS.md"]; !ok || op.On || op.Group || op.IsGate {
		t.Errorf("AGENTS.md op = %+v, want file -> off", op)
	}
	if op, ok := byRel[".claude/skills"]; !ok || op.On || !op.Group || len(op.Children) != 2 {
		t.Errorf("skills op = %+v, want group -> off with 2 children", op)
	}
	if _, ok := byRel["smoke"]; ok {
		t.Errorf("gate smoke got an op, want none (already on)")
	}
	if _, ok := byRel["tracked.md"]; ok {
		t.Errorf("non-toggleable tracked.md got an op, want none")
	}
}

// everything on only touches what isn't already on: AGENTS.md and smoke are
// already on and get no op, and the mixed group collapses to one group-on
// op covering both children.
func TestPresetOpsAllOn(t *testing.T) {
	items := sampleItems()
	ops := PresetOps(postureAllOn, items)
	if len(ops) != 1 {
		t.Fatalf("ops = %+v, want 1 (group on only)", ops)
	}
	op := ops[0]
	if !op.Group || op.Rel != ".claude/skills" || !op.On || len(op.Children) != 2 {
		t.Errorf("op = %+v, want group .claude/skills -> on with 2 children", op)
	}
}

// A choice outside the declared enum (a client that ignores the schema, or
// a stale form reply) is treated the same as keep current: no ops.
func TestPresetOpsJunk(t *testing.T) {
	if ops := PresetOps("banana", sampleItems()); ops != nil {
		t.Errorf("ops = %+v, want nil", ops)
	}
}

// BuildSectionsForm builds the sections variant's first form: one enum field
// per dev-loop stage that has at least one toggleable item, titled with the
// stage's kind (gates vs items), leaf count, and how many of those leaves are
// on — tracked.md (non-toggleable) contributes to no stage's count. Section
// order is dev-loop stage order, the same rule as every other Build*
// function; the two stages with nothing toggleable (during session, pre-push)
// get no field at all.
func TestBuildSectionsForm(t *testing.T) {
	fields, schema, err := BuildSectionsForm(sampleItems())
	if err != nil {
		t.Fatalf("BuildSectionsForm: %v", err)
	}
	if len(fields) != 3 {
		t.Fatalf("fields = %d, want 3: %+v", len(fields), fields)
	}
	wantKeys := []string{"section:session start", "section:on demand", "section:pre-commit"}
	for i, w := range wantKeys {
		if fields[i].Key != w {
			t.Errorf("fields[%d].Key = %q, want %q", i, fields[i].Key, w)
		}
	}
	s := string(schema)
	wantTitles := []string{
		`"[session start] items (1) — 1/1 on"`,
		`"[on demand] items (2) — 1/2 on"`,
		`"[pre-commit] gates (1) — 1/1 on"`,
	}
	for _, w := range wantTitles {
		if !strings.Contains(s, w) {
			t.Errorf("schema missing title %s:\n%s", w, s)
		}
	}
	for _, w := range []string{`"` + sectionKeep + `"`, `"` + sectionAllOn + `"`, `"` + sectionAllOff + `"`, `"` + sectionOpen + `"`} {
		if !strings.Contains(s, w) {
			t.Errorf("schema missing enum choice %s:\n%s", w, s)
		}
	}
	if strings.Contains(s, "tracked.md") {
		t.Errorf("schema contains non-toggleable row: %s", s)
	}
}

// BuildSectionForm builds one stage's expanded-shape boolean rows — the same
// per-child dissolution BuildForm's expand path uses — scoped to just that
// stage's items: the on-demand group's two children, nothing from any other
// stage.
func TestBuildSectionForm(t *testing.T) {
	fields, schema, err := BuildSectionForm(sampleItems(), tui.StageOnDemand)
	if err != nil {
		t.Fatalf("BuildSectionForm: %v", err)
	}
	wantKeys := []string{"file:.claude/skills/a.md", "file:.claude/skills/b.md"}
	if len(fields) != len(wantKeys) {
		t.Fatalf("fields = %d, want %d: %+v", len(fields), len(wantKeys), fields)
	}
	for i, w := range wantKeys {
		if fields[i].Key != w {
			t.Errorf("fields[%d].Key = %q, want %q", i, fields[i].Key, w)
		}
	}
	if d, ok := fields[0].Default.(string); !ok || d != rowEnabled {
		t.Errorf("a.md default = %v, want %q", fields[0].Default, rowEnabled)
	}
	if d, ok := fields[1].Default.(string); !ok || d != rowDisabled {
		t.Errorf("b.md default = %v, want %q", fields[1].Default, rowDisabled)
	}
	s := string(schema)
	if strings.Contains(s, "dir:") || strings.Contains(s, "gate:") || strings.Contains(s, "AGENTS.md") {
		t.Errorf("section form leaked fields from another stage:\n%s", s)
	}
}

// SectionBulkOps targets one stage's toggleable items at a single value: a
// group becomes one group Op (only when some child differs from target), a
// gate or standalone file becomes one Op only when it differs — an
// already-satisfied stage (session start, already all on) gets no ops at
// all.
func TestSectionBulkOps(t *testing.T) {
	items := sampleItems()
	t.Run("on demand all-on", func(t *testing.T) {
		ops := SectionBulkOps(items, tui.StageOnDemand, true)
		if len(ops) != 1 || !ops[0].Group || ops[0].Rel != ".claude/skills" || !ops[0].On || len(ops[0].Children) != 2 {
			t.Errorf("ops = %+v, want one group .claude/skills -> on with 2 children", ops)
		}
	})
	t.Run("pre-commit all-off", func(t *testing.T) {
		ops := SectionBulkOps(items, tui.StagePreCommit, false)
		if len(ops) != 1 || !ops[0].IsGate || ops[0].Rel != "smoke" || ops[0].On {
			t.Errorf("ops = %+v, want one gate smoke -> off", ops)
		}
	})
	t.Run("session start all-on", func(t *testing.T) {
		ops := SectionBulkOps(items, tui.StageSessionStart, true)
		if len(ops) != 0 {
			t.Errorf("ops = %+v, want none (AGENTS.md already on)", ops)
		}
	})
}
