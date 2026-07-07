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
