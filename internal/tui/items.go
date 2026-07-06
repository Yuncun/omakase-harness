// items.go builds the Item list the interactive status screen (Task 7)
// renders: the placed ledger and gate rows grouped and staged into the
// dev-loop sections from spec §Screen. Pure data — no terminal I/O.
package tui

import (
	"sort"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/harness"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// Stage buckets an Item into the dev-loop section it belongs on the
// interactive status screen (spec §Screen). Declaration order IS display
// order.
type Stage int

const (
	StageSessionStart Stage = iota
	StageOnDemand
	StageDuringSession
	StagePreCommit
	StagePrePush
	StageOther
	StageMachinery
)

// StageTitle is the section header text for s (spec §Screen).
func StageTitle(s Stage) string {
	switch s {
	case StageSessionStart:
		return "SESSION START — always in your agent's context"
	case StageOnDemand:
		return "ON DEMAND — loads when invoked"
	case StageDuringSession:
		return "DURING SESSION — agent hooks + config"
	case StagePreCommit:
		return "PRE-COMMIT — gates at every commit"
	case StagePrePush:
		return "PRE-PUSH — gates at every push"
	case StageOther:
		return "OTHER — placed files"
	default:
		return ""
	}
}

// ChildRef is one member of a grouped Item, carrying its own enabled state
// for expanded-row rendering and per-child toggling in Task 7. Amendment to
// the Task 6 brief's `Children []string` (task-6-brief.md's "ONE amendment"),
// adopted now so Task 7 doesn't have to churn these tests.
type ChildRef struct {
	Rel     string
	Enabled bool
}

// Item is one row (or, when Group, one collapsed group of rows) on the
// interactive status screen.
type Item struct {
	Label      string     // display: gate name, rel path, or group dir + "/"
	Rel        string     // file items: ledger rel; group items: dir prefix; gates: gate name
	Stage      Stage      // dev-loop section this item belongs to
	IsGate     bool       // true for consent-tracked (omakase-gate.sh) gate rows
	Group      bool       // true when this Item collapses >= 2 sibling files
	Children   []ChildRef // group items: members in ledger order
	Enabled    bool       // group: true when ALL children enabled; else this item's own state
	PartialOff bool       // group: some but not all children off
	Toggleable bool       // false: tracked files, non-gate lefthook jobs
	Count      int        // group size
}

// stageOf classifies rel (with its ledger kind) into a Stage, and reports
// whether it is machinery (counted by BuildItems, never itemized). Verbatim
// from the Task 6 brief — every case is load-bearing, do not edit.
func stageOf(rel, kind string) (Stage, bool) {
	switch {
	case strings.HasPrefix(rel, ".omakase/"),
		rel == "lefthook.yml", rel == "lefthook-local.yml", rel == ".worktreeinclude",
		strings.HasPrefix(rel, ".lefthook/"), strings.HasPrefix(rel, ".husky/"),
		strings.HasPrefix(rel, ".githooks/"):
		return StageOther, true
	}
	switch kind {
	case "rule", "doc":
		return StageSessionStart, false
	case "skill", "command", "agent", "prompt":
		return StageOnDemand, false
	case "config":
		return StageDuringSession, false
	case "gate": // reachable only for .claude/hooks/* and .github/hooks/* after the machinery switch above
		return StageDuringSession, false
	default:
		return StageOther, false
	}
}

// groupKey returns the first two `/`-separated segments of rel — the
// grouping key for any rel with strings.Count(rel, "/") >= 2 (brief step 3).
func groupKey(rel string) string {
	parts := strings.SplitN(rel, "/", 3)
	return parts[0] + "/" + parts[1]
}

// BuildItems turns the placed ledger (rows), the resolved gate rows, the
// disabled set (gate name -> disabled), and the repo's tracked-but-not-placed
// harness paths (trackedHarness) into the Item list Task 7 renders, plus a
// count of machinery entries (never itemized, only counted — spec §Screen).
func BuildItems(rows []state.PlacedRow, gates []GateRow, disabled map[string]bool, trackedHarness []string) (items []Item, machinery int) {
	// Bucket grouping candidates (>= 2 path segments) by their first-two-segment
	// key, preserving ledger order, so each bucket's final size is known before
	// any Item is emitted from the second pass below.
	buckets := map[string][]state.PlacedRow{}
	placed := map[string]bool{}
	for _, row := range rows {
		placed[row.Rel] = true
		if strings.Count(row.Rel, "/") >= 2 {
			key := groupKey(row.Rel)
			buckets[key] = append(buckets[key], row)
		}
	}

	emittedGroup := map[string]bool{}
	for _, row := range rows {
		stage, mach := stageOf(row.Rel, row.Kind)
		if mach {
			machinery++
			continue
		}

		if strings.Count(row.Rel, "/") >= 2 {
			key := groupKey(row.Rel)
			if members := buckets[key]; len(members) >= 2 {
				if emittedGroup[key] {
					continue // this key's one Item was already emitted at its first member
				}
				emittedGroup[key] = true
				items = append(items, buildGroupItem(key, stage, members))
				continue
			}
		}

		items = append(items, Item{
			Label:      row.Rel,
			Rel:        row.Rel,
			Stage:      stage,
			Toggleable: true,
			Enabled:    row.Enabled == "1",
		})
	}

	for _, g := range gates {
		if g.Hook != "pre-commit" && g.Hook != "pre-push" {
			machinery++ // Task 6 brief: hooks other than pre-commit/pre-push are machinery, not items
			continue
		}
		stage := StagePreCommit
		if g.Hook == "pre-push" {
			stage = StagePrePush
		}
		it := Item{
			Label:      g.Name,
			Rel:        g.Name,
			Stage:      stage,
			IsGate:     g.Gate,
			Toggleable: g.Gate, // non-gate lefthook jobs are view-only (brief: "Toggleable false")
		}
		if g.Gate {
			it.Enabled = !disabled[g.Name]
		} else {
			it.Enabled = true // view-only job: always runs, no consent toggle exists
		}
		items = append(items, it)
	}

	for _, rel := range trackedHarness {
		if placed[rel] {
			continue // already represented by a ledger row above
		}
		kind := harness.KindOf(rel)
		stage, mach := stageOf(rel, kind)
		if mach {
			machinery++
			continue
		}
		items = append(items, Item{
			Label:      rel,
			Rel:        rel,
			Stage:      stage,
			Toggleable: false,
			Enabled:    true, // tracked, not consent-gated: always considered "on"
		})
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].Stage < items[j].Stage })
	return items, machinery
}

// buildGroupItem collapses a >= 2-member bucket into one Group Item: Enabled
// is true only when every child is enabled, PartialOff when some but not all
// are off (brief: "Group Enabled = all children enabled; PartialOff = some
// but not all off").
func buildGroupItem(key string, stage Stage, members []state.PlacedRow) Item {
	children := make([]ChildRef, len(members))
	onCount := 0
	for i, m := range members {
		on := m.Enabled == "1"
		children[i] = ChildRef{Rel: m.Rel, Enabled: on}
		if on {
			onCount++
		}
	}
	return Item{
		Label:      key + "/",
		Rel:        key,
		Stage:      stage,
		Group:      true,
		Children:   children,
		Enabled:    onCount == len(children),
		PartialOff: onCount > 0 && onCount < len(children),
		Toggleable: true,
		Count:      len(children),
	}
}
