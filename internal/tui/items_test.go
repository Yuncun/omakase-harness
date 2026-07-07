package tui

import (
	"reflect"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

func TestStageTitle(t *testing.T) {
	cases := []struct {
		stage Stage
		want  string
	}{
		{StageSessionStart, "SESSION START — always in your agent's context"},
		{StageOnDemand, "ON DEMAND — loads when invoked"},
		{StageDuringSession, "DURING SESSION — agent hooks + config"},
		{StagePreCommit, "PRE-COMMIT — gates at every commit"},
		{StagePrePush, "PRE-PUSH — gates at every push"},
		{StageOther, "OTHER — placed files"},
	}
	for _, c := range cases {
		if got := StageTitle(c.stage); got != c.want {
			t.Errorf("StageTitle(%v) = %q, want %q", c.stage, got, c.want)
		}
	}
}

// TestBuildItems is table-driven over BuildItems's grouping, staging, gate,
// machinery, and tracked-harness rules (Task 6 brief). stageOf itself has no
// exported surface, so its cases are exercised only through the Stage each
// resulting Item carries.
func TestBuildItems(t *testing.T) {
	cases := []struct {
		name     string
		rows     []state.PlacedRow
		gates    []GateRow
		disabled map[string]bool
		tracked  []string
		check    func(t *testing.T, items []Item, machinery int)
	}{
		{
			name: "sibling rule files group into one partial-off item",
			rows: []state.PlacedRow{
				{Rel: ".claude/rules/a.md", Kind: "rule", Enabled: "1"},
				{Rel: ".claude/rules/b.md", Kind: "rule", Enabled: "0"},
			},
			check: func(t *testing.T, items []Item, machinery int) {
				if len(items) != 1 {
					t.Fatalf("len(items) = %d, want 1: %#v", len(items), items)
				}
				it := items[0]
				want := Item{
					Label: ".claude/rules/", Rel: ".claude/rules", Stage: StageSessionStart,
					Group: true,
					Children: []ChildRef{
						{Rel: ".claude/rules/a.md", Enabled: true},
						{Rel: ".claude/rules/b.md", Enabled: false},
					},
					Enabled: false, PartialOff: true, Toggleable: true, Count: 2,
				}
				if !reflect.DeepEqual(it, want) {
					t.Errorf("group item = %#v, want %#v", it, want)
				}
				if machinery != 0 {
					t.Errorf("machinery = %d, want 0", machinery)
				}
			},
		},
		{
			name: "standalone doc, grouped skills, standalone config and hook",
			rows: []state.PlacedRow{
				{Rel: "AGENTS.md", Kind: "doc", Enabled: "1"},
				{Rel: ".claude/skills/x/SKILL.md", Kind: "skill", Enabled: "1"},
				{Rel: ".claude/skills/y/SKILL.md", Kind: "skill", Enabled: "1"},
				{Rel: ".claude/settings.json", Kind: "config", Enabled: "1"},
				{Rel: ".claude/hooks/h.sh", Kind: "gate", Enabled: "1"},
			},
			check: func(t *testing.T, items []Item, machinery int) {
				if len(items) != 4 {
					t.Fatalf("len(items) = %d, want 4: %#v", len(items), items)
				}
				byRel := map[string]Item{}
				for _, it := range items {
					byRel[it.Rel] = it
				}
				doc, ok := byRel["AGENTS.md"]
				if !ok || doc.Group || doc.Stage != StageSessionStart {
					t.Errorf("AGENTS.md item = %#v, ok=%v", doc, ok)
				}
				skills, ok := byRel[".claude/skills"]
				if !ok {
					t.Fatalf("no group item for .claude/skills: %#v", items)
				}
				wantSkills := Item{
					Label: ".claude/skills/", Rel: ".claude/skills", Stage: StageOnDemand,
					Group: true,
					Children: []ChildRef{
						{Rel: ".claude/skills/x/SKILL.md", Enabled: true},
						{Rel: ".claude/skills/y/SKILL.md", Enabled: true},
					},
					Enabled: true, PartialOff: false, Toggleable: true, Count: 2,
				}
				if !reflect.DeepEqual(skills, wantSkills) {
					t.Errorf("skills group = %#v, want %#v", skills, wantSkills)
				}
				cfg, ok := byRel[".claude/settings.json"]
				if !ok || cfg.Group || cfg.Stage != StageDuringSession {
					t.Errorf(".claude/settings.json item = %#v, ok=%v", cfg, ok)
				}
				hook, ok := byRel[".claude/hooks/h.sh"]
				if !ok || hook.Group || hook.Stage != StageDuringSession {
					t.Errorf(".claude/hooks/h.sh item = %#v, ok=%v", hook, ok)
				}
				if machinery != 0 {
					t.Errorf("machinery = %d, want 0", machinery)
				}
			},
		},
		{
			name: "machinery paths are counted, never itemized",
			rows: []state.PlacedRow{
				{Rel: ".omakase/gates/g.sh", Kind: "gate", Enabled: "1"},
				{Rel: "lefthook.yml", Kind: "gate", Enabled: "1"},
				{Rel: ".worktreeinclude", Kind: "other", Enabled: "1"},
			},
			check: func(t *testing.T, items []Item, machinery int) {
				if len(items) != 0 {
					t.Errorf("len(items) = %d, want 0: %#v", len(items), items)
				}
				if machinery != 3 {
					t.Errorf("machinery = %d, want 3", machinery)
				}
			},
		},
		{
			name: "disabled consent gate and a view-only non-gate row",
			gates: []GateRow{
				{Hook: "pre-commit", Name: "adr", Gate: true},
				{Hook: "pre-push", Name: "lint-job", Gate: false},
			},
			disabled: map[string]bool{"adr": true},
			check: func(t *testing.T, items []Item, machinery int) {
				if len(items) != 2 {
					t.Fatalf("len(items) = %d, want 2: %#v", len(items), items)
				}
				adr := items[0]
				wantAdr := Item{Label: "adr", Rel: "adr", Stage: StagePreCommit, IsGate: true, Toggleable: true, Enabled: false}
				if !reflect.DeepEqual(adr, wantAdr) {
					t.Errorf("adr gate item = %#v, want %#v", adr, wantAdr)
				}
				lint := items[1]
				wantLint := Item{Label: "lint-job", Rel: "lint-job", Stage: StagePrePush, IsGate: false, Toggleable: false, Enabled: true}
				if !reflect.DeepEqual(lint, wantLint) {
					t.Errorf("lint-job view-only item = %#v, want %#v", lint, wantLint)
				}
				if machinery != 0 {
					t.Errorf("machinery = %d, want 0", machinery)
				}
			},
		},
		{
			name: "gate rows outside pre-commit/pre-push count as machinery",
			gates: []GateRow{
				{Hook: "post-checkout", Name: "heal", Gate: false},
			},
			check: func(t *testing.T, items []Item, machinery int) {
				if len(items) != 0 {
					t.Errorf("len(items) = %d, want 0: %#v", len(items), items)
				}
				if machinery != 1 {
					t.Errorf("machinery = %d, want 1", machinery)
				}
			},
		},
		{
			name: "tracked harness paths become non-toggleable items, skipping already-placed rels",
			rows: []state.PlacedRow{
				{Rel: "AGENTS.md", Kind: "doc", Enabled: "1"},
			},
			tracked: []string{"CLAUDE.md", "AGENTS.md"},
			check: func(t *testing.T, items []Item, machinery int) {
				if len(items) != 2 {
					t.Fatalf("len(items) = %d, want 2 (AGENTS.md placed + CLAUDE.md tracked, not duplicated): %#v", len(items), items)
				}
				byRel := map[string]Item{}
				for _, it := range items {
					byRel[it.Rel] = it
				}
				agents, ok := byRel["AGENTS.md"]
				if !ok || !agents.Toggleable {
					t.Errorf("AGENTS.md (placed) item = %#v, ok=%v, want Toggleable=true", agents, ok)
				}
				claude, ok := byRel["CLAUDE.md"]
				wantClaude := Item{Label: "CLAUDE.md", Rel: "CLAUDE.md", Stage: StageSessionStart, Toggleable: false, Enabled: true}
				if !ok || !reflect.DeepEqual(claude, wantClaude) {
					t.Errorf("CLAUDE.md (tracked) item = %#v, ok=%v, want %#v", claude, ok, wantClaude)
				}
				if machinery != 0 {
					t.Errorf("machinery = %d, want 0", machinery)
				}
			},
		},
		{
			name: "overall stage ordering: session-start < on-demand < during-session < pre-commit < pre-push < other",
			rows: []state.PlacedRow{
				{Rel: ".claude/rules/only.md", Kind: "rule", Enabled: "1"},
				{Rel: ".claude/skills/only/SKILL.md", Kind: "skill", Enabled: "1"},
				{Rel: ".claude/settings.json", Kind: "config", Enabled: "1"},
				{Rel: "docs/notes.md", Kind: "other", Enabled: "1"},
			},
			gates: []GateRow{
				{Hook: "pre-commit", Name: "g1", Gate: true},
				{Hook: "pre-push", Name: "g2", Gate: true},
			},
			check: func(t *testing.T, items []Item, machinery int) {
				var gotStages []Stage
				for _, it := range items {
					gotStages = append(gotStages, it.Stage)
				}
				wantStages := []Stage{
					StageSessionStart, StageOnDemand, StageDuringSession,
					StagePreCommit, StagePrePush, StageOther,
				}
				if !reflect.DeepEqual(gotStages, wantStages) {
					t.Errorf("stage order = %v, want %v (items: %#v)", gotStages, wantStages, items)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items, machinery := BuildItems(c.rows, c.gates, c.disabled, c.tracked)
			c.check(t, items, machinery)
		})
	}
}
