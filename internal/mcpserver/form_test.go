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

// Only toggleable items become fields, with the exact key formats from the
// spec; the tracked row is absent.
func TestBuildFormFieldsAndKeys(t *testing.T) {
	fields, schema, err := BuildForm(sampleItems(), false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	if len(fields) != 3 {
		t.Fatalf("fields = %d, want 3 (tracked row excluded): %+v", len(fields), fields)
	}
	wantKeys := []string{"file:AGENTS.md", "dir:.claude/skills", "gate:smoke"}
	for i, w := range wantKeys {
		if fields[i].Key != w {
			t.Errorf("fields[%d].Key = %q, want %q", i, fields[i].Key, w)
		}
	}
	if s := string(schema); strings.Contains(s, "tracked.md") {
		t.Errorf("schema contains non-toggleable row: %s", s)
	}
}

// The raw schema keeps section order: file before group before gate, because
// hosts render properties in declaration order.
func TestBuildFormPreservesOrder(t *testing.T) {
	_, schema, err := BuildForm(sampleItems(), false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	s := string(schema)
	iFile := strings.Index(s, `"file:AGENTS.md"`)
	iDir := strings.Index(s, `"dir:.claude/skills"`)
	iGate := strings.Index(s, `"gate:smoke"`)
	if iFile < 0 || iDir < 0 || iGate < 0 || !(iFile < iDir && iDir < iGate) {
		t.Errorf("schema property order wrong (file=%d dir=%d gate=%d):\n%s", iFile, iDir, iGate, s)
	}
	var v map[string]any
	if err := json.Unmarshal(schema, &v); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
}

// Booleans default to the current state; a partially-off group is a 3-value
// enum defaulting to "keep as-is".
func TestBuildFormDefaults(t *testing.T) {
	fields, schema, err := BuildForm(sampleItems(), false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	if d, ok := fields[0].Default.(bool); !ok || !d {
		t.Errorf("file default = %v, want true", fields[0].Default)
	}
	if d, ok := fields[1].Default.(string); !ok || d != "keep as-is" {
		t.Errorf("partial group default = %v, want %q", fields[1].Default, "keep as-is")
	}
	s := string(schema)
	for _, want := range []string{`"keep as-is"`, `"all on"`, `"all off"`, `"default":true`} {
		if !strings.Contains(s, want) {
			t.Errorf("schema missing %s:\n%s", want, s)
		}
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

// A fully-on group is a plain boolean field, and turning it off diffs to one
// group Op.
func TestBuildFormWholeGroupBoolean(t *testing.T) {
	items := []tui.Item{{
		Label: ".claude/skills/", Rel: ".claude/skills", Stage: tui.StageOnDemand, Group: true, Toggleable: true,
		Children: []tui.ChildRef{{Rel: ".claude/skills/a.md", Enabled: true}, {Rel: ".claude/skills/b.md", Enabled: true}},
		Enabled:  true, Count: 2,
	}}
	fields, _, err := BuildForm(items, false)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	if d, ok := fields[0].Default.(bool); !ok || !d {
		t.Fatalf("whole group default = %v, want boolean true", fields[0].Default)
	}
	ops := Diff(fields, map[string]any{"dir:.claude/skills": false})
	if len(ops) != 1 || !ops[0].Group || ops[0].On {
		t.Errorf("ops = %+v, want one group off Op", ops)
	}
}

// With expand, groups dissolve into one file field per member — the full
// per-file view — keeping section order, per-child defaults, and no enum
// (each file is a plain boolean, even in a mixed group).
func TestBuildFormExpanded(t *testing.T) {
	fields, schema, err := BuildForm(sampleItems(), true)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	wantKeys := []string{"file:AGENTS.md", "file:.claude/skills/a.md", "file:.claude/skills/b.md", "gate:smoke"}
	if len(fields) != len(wantKeys) {
		t.Fatalf("fields = %d, want %d: %+v", len(fields), len(wantKeys), fields)
	}
	for i, w := range wantKeys {
		if fields[i].Key != w {
			t.Errorf("fields[%d].Key = %q, want %q", i, fields[i].Key, w)
		}
	}
	if d, ok := fields[2].Default.(bool); !ok || d {
		t.Errorf("disabled child default = %v, want boolean false", fields[2].Default)
	}
	s := string(schema)
	if strings.Contains(s, "dir:") || strings.Contains(s, keepAsIs) {
		t.Errorf("expanded schema still has group fields or the mixed-group enum:\n%s", s)
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
func TestEffectiveOps(t *testing.T) {
	base := sampleItems()
	original := stateByKey(base)

	// Simulate a chain flow that already flipped the gate off in an earlier
	// step: the fields built from that interim state default to false where
	// the pre-chain original was true.
	interim := ApplyOps(base, []Op{{IsGate: true, Rel: "smoke", On: false}})
	fields, _, err := BuildForm(interim, true)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}

	t.Run("untouched content, default differs from original -> op emitted", func(t *testing.T) {
		ops := EffectiveOps(fields, map[string]any{}, original)
		if len(ops) != 1 || !ops[0].IsGate || ops[0].Rel != "smoke" || ops[0].On {
			t.Fatalf("ops = %+v, want one gate smoke -> off", ops)
		}
	})

	t.Run("submitted value overrides default back to original -> no op", func(t *testing.T) {
		ops := EffectiveOps(fields, map[string]any{"gate:smoke": true}, original)
		if len(ops) != 0 {
			t.Errorf("ops = %+v, want none (submission restores original)", ops)
		}
	})

	t.Run("junk-typed submission falls back to default", func(t *testing.T) {
		ops := EffectiveOps(fields, map[string]any{"gate:smoke": "banana"}, original)
		if len(ops) != 1 || !ops[0].IsGate || ops[0].Rel != "smoke" || ops[0].On {
			t.Fatalf("ops = %+v, want default (off) to win over junk submission", ops)
		}
	})

	t.Run("field missing from original is skipped", func(t *testing.T) {
		extra := append([]Field{{Key: "file:new.md", Rel: "new.md", Default: true}}, fields...)
		ops := EffectiveOps(extra, map[string]any{}, original)
		for _, op := range ops {
			if op.Rel == "new.md" {
				t.Errorf("op emitted for a field missing from original: %+v", op)
			}
		}
	})
}
