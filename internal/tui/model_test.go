package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeRepo is both the Toggler and the reload source: its methods record calls
// and mutate the item state so reload() shows what a real toggle would produce.
type fakeRepo struct {
	items     []Item
	machinery int
	calls     []string
	fail      map[string]error // rel/gate name -> error to return (refusal)
}

func (f *fakeRepo) GateOff(n string) error { return f.op("GateOff", n, false) }
func (f *fakeRepo) GateOn(n string) error  { return f.op("GateOn", n, true) }
func (f *fakeRepo) FileOff(r string) error { return f.op("FileOff", r, false) }
func (f *fakeRepo) FileOn(r string) error  { return f.op("FileOn", r, true) }

func (f *fakeRepo) op(kind, target string, on bool) error {
	f.calls = append(f.calls, kind+":"+target)
	if err := f.fail[target]; err != nil {
		return err
	}
	f.setEnabled(target, on)
	return nil
}

// setEnabled flips the enabled state of the standalone item, gate, or group
// child named target, recomputing the parent group's aggregate when a child
// changes — so reload() reflects the toggle.
func (f *fakeRepo) setEnabled(target string, on bool) {
	for i := range f.items {
		it := &f.items[i]
		if it.Rel == target && !it.Group {
			it.Enabled = on
			return
		}
		if it.Group {
			for c := range it.Children {
				if it.Children[c].Rel != target {
					continue
				}
				it.Children[c].Enabled = on
				onCount := 0
				for _, ch := range it.Children {
					if ch.Enabled {
						onCount++
					}
				}
				it.Enabled = onCount == len(it.Children)
				it.PartialOff = onCount > 0 && onCount < len(it.Children)
				return
			}
		}
	}
}

func (f *fakeRepo) reload() ([]Item, int) { return cloneItems(f.items), f.machinery }

func cloneItems(src []Item) []Item {
	out := make([]Item, len(src))
	for i, it := range src {
		out[i] = it
		if it.Children != nil {
			ch := make([]ChildRef, len(it.Children))
			copy(ch, it.Children)
			out[i].Children = ch
		}
	}
	return out
}

// newTestModel wires a Model to a fakeRepo, giving the Model its own deep copy
// of the items so toggles route through the Toggler + reload rather than
// aliasing the fakeRepo's slice.
func newTestModel(f *fakeRepo) Model {
	return NewModel("HEADER", "footprint line", cloneItems(f.items), f.machinery, f, f.reload)
}

func keyMsg(t KeyType) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyType(t)} }

// KeyType is a local alias so the helpers below read cleanly.
type KeyType = tea.KeyType

func runeMsg(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func send(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// lineWith returns the first line of view containing sub, or "" if none.
func lineWith(view, sub string) string {
	for _, ln := range strings.Split(view, "\n") {
		if strings.Contains(ln, sub) {
			return ln
		}
	}
	return ""
}

func navItems() []Item {
	return []Item{
		{Label: "AGENTS.md", Rel: "AGENTS.md", Stage: StageSessionStart, Toggleable: true, Enabled: true},
		{Label: "adr", Rel: "adr", Stage: StagePreCommit, IsGate: true, Toggleable: true, Enabled: true},
		{Label: "lint", Rel: "lint", Stage: StagePrePush, IsGate: false, Toggleable: false, Enabled: true},
	}
}

func TestModelNavigationMovesCursorAndClamps(t *testing.T) {
	f := &fakeRepo{items: navItems()}
	m := newTestModel(f)

	// Starts on the first row.
	if ln := lineWith(m.View(), "AGENTS.md"); !strings.Contains(ln, "▸") {
		t.Fatalf("cursor should start on AGENTS.md, got line %q", ln)
	}

	// Down (arrow) then down (j) walks to the last row and clamps there.
	m, _ = send(m, keyMsg(tea.KeyDown))
	if ln := lineWith(m.View(), "adr"); !strings.Contains(ln, "▸") {
		t.Fatalf("after down, cursor should be on adr, got %q", ln)
	}
	m, _ = send(m, runeMsg('j'))
	m, _ = send(m, runeMsg('j')) // clamp at bottom
	if ln := lineWith(m.View(), "lint"); !strings.Contains(ln, "▸") {
		t.Fatalf("after down*2, cursor should clamp on lint, got %q", ln)
	}
	if ln := lineWith(m.View(), "AGENTS.md"); strings.Contains(ln, "▸") {
		t.Fatalf("AGENTS.md should no longer hold the cursor, got %q", ln)
	}

	// Up (k then arrow) walks back and clamps at the top.
	m, _ = send(m, runeMsg('k'))
	m, _ = send(m, keyMsg(tea.KeyUp))
	m, _ = send(m, keyMsg(tea.KeyUp)) // clamp at top
	if ln := lineWith(m.View(), "AGENTS.md"); !strings.Contains(ln, "▸") {
		t.Fatalf("after up, cursor should clamp on AGENTS.md, got %q", ln)
	}
}

func groupItems() []Item {
	return []Item{
		{
			Label: ".claude/rules/", Rel: ".claude/rules", Stage: StageSessionStart,
			Group: true, Toggleable: true, Count: 2, Enabled: true,
			Children: []ChildRef{
				{Rel: ".claude/rules/a.md", Enabled: true},
				{Rel: ".claude/rules/b.md", Enabled: true},
			},
		},
	}
}

func TestModelExpandCollapse(t *testing.T) {
	f := &fakeRepo{items: groupItems()}
	m := newTestModel(f)

	// Collapsed: children are not shown.
	if strings.Contains(m.View(), ".claude/rules/a.md") {
		t.Fatalf("collapsed group should not show children:\n%s", m.View())
	}

	// Right expands: children appear as rows.
	m, _ = send(m, keyMsg(tea.KeyRight))
	v := m.View()
	if !strings.Contains(v, ".claude/rules/a.md") || !strings.Contains(v, ".claude/rules/b.md") {
		t.Fatalf("expanded group should show both children:\n%s", v)
	}

	// Left collapses again.
	m, _ = send(m, keyMsg(tea.KeyLeft))
	if strings.Contains(m.View(), ".claude/rules/a.md") {
		t.Fatalf("collapsed group should hide children again:\n%s", m.View())
	}
}

func TestModelEnterGateTogglesOffThenOn(t *testing.T) {
	f := &fakeRepo{items: []Item{
		{Label: "adr", Rel: "adr", Stage: StagePreCommit, IsGate: true, Toggleable: true, Enabled: true},
	}}
	m := newTestModel(f)

	m, _ = send(m, keyMsg(tea.KeyEnter))
	if len(f.calls) != 1 || f.calls[0] != "GateOff:adr" {
		t.Fatalf("enter on enabled gate should call GateOff once, calls=%v", f.calls)
	}
	if !strings.Contains(m.feedback, "off") {
		t.Fatalf("feedback should mention off, got %q", m.feedback)
	}
	if ln := lineWith(m.View(), "adr"); !strings.Contains(ln, "[ ]") {
		t.Fatalf("after GateOff, adr should render an empty checkbox, got %q", ln)
	}

	// Enter again undoes it.
	m, _ = send(m, keyMsg(tea.KeyEnter))
	if len(f.calls) != 2 || f.calls[1] != "GateOn:adr" {
		t.Fatalf("second enter should call GateOn, calls=%v", f.calls)
	}
	if ln := lineWith(m.View(), "adr"); !strings.Contains(ln, "[x]") {
		t.Fatalf("after GateOn, adr should render a checked box, got %q", ln)
	}
}

func TestModelEnterOnGroupTogglesEveryChild(t *testing.T) {
	f := &fakeRepo{items: groupItems()}
	m := newTestModel(f)

	m, _ = send(m, keyMsg(tea.KeyEnter)) // group all-on -> FileOff every child
	want := []string{"FileOff:.claude/rules/a.md", "FileOff:.claude/rules/b.md"}
	if strings.Join(f.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("group enter should FileOff each child once, calls=%v", f.calls)
	}
}

func TestModelEnterOnChildTogglesOnlyThatChild(t *testing.T) {
	f := &fakeRepo{items: groupItems()}
	m := newTestModel(f)

	m, _ = send(m, keyMsg(tea.KeyRight)) // expand
	m, _ = send(m, keyMsg(tea.KeyDown))  // move onto first child
	m, _ = send(m, keyMsg(tea.KeyEnter)) // toggle just that child

	if len(f.calls) != 1 || f.calls[0] != "FileOff:.claude/rules/a.md" {
		t.Fatalf("enter on a child should FileOff only that child, calls=%v", f.calls)
	}
}

func TestModelEnterOnViewOnlyRowExplainsAndDoesNotToggle(t *testing.T) {
	f := &fakeRepo{items: []Item{
		{Label: "CLAUDE.md", Rel: "CLAUDE.md", Stage: StageSessionStart, Toggleable: false, Enabled: true},
	}}
	m := newTestModel(f)

	// A view-only row never renders a checkbox — only the `·` marker.
	ln := lineWith(m.View(), "CLAUDE.md")
	if strings.Contains(ln, "[x]") || strings.Contains(ln, "[ ]") {
		t.Fatalf("view-only row must not render a checkbox, got %q", ln)
	}
	if !strings.Contains(ln, "·") {
		t.Fatalf("view-only row should render the · marker, got %q", ln)
	}

	m, _ = send(m, keyMsg(tea.KeyEnter))
	if len(f.calls) != 0 {
		t.Fatalf("enter on a view-only row must not call the Toggler, calls=%v", f.calls)
	}
	if !strings.Contains(m.feedback, "tracked by the repo — omakase never deletes committed files") {
		t.Fatalf("view-only feedback should explain why, got %q", m.feedback)
	}
}

func TestModelTogglerRefusalLeavesStateUnchanged(t *testing.T) {
	f := &fakeRepo{
		items: []Item{
			{Label: "docs/x.md", Rel: "docs/x.md", Stage: StageOther, Toggleable: true, Enabled: true},
		},
		fail: map[string]error{"docs/x.md": errors.New("differs from what init placed (local edits?) — refusing to delete")},
	}
	m := newTestModel(f)

	m, _ = send(m, keyMsg(tea.KeyEnter))
	if !strings.Contains(m.feedback, "REFUSING") {
		t.Fatalf("refusal feedback should contain REFUSING, got %q", m.feedback)
	}
	if ln := lineWith(m.View(), "docs/x.md"); !strings.Contains(ln, "[x]") {
		t.Fatalf("refused item should stay enabled ([x]), got %q", ln)
	}
}

func TestModelGroupPartialRefusalSummarizes(t *testing.T) {
	f := &fakeRepo{
		items: []Item{
			{
				Label: ".claude/rules/", Rel: ".claude/rules", Stage: StageSessionStart,
				Group: true, Toggleable: true, Count: 3, Enabled: false,
				Children: []ChildRef{
					{Rel: ".claude/rules/a.md", Enabled: false},
					{Rel: ".claude/rules/b.md", Enabled: false},
					{Rel: ".claude/rules/c.md", Enabled: false},
				},
			},
		},
		fail: map[string]error{".claude/rules/c.md": errors.New("tracked by git — omakase never deletes committed files")},
	}
	m := newTestModel(f)

	m, _ = send(m, keyMsg(tea.KeyEnter)) // group all-off -> FileOn each; c refuses
	if len(f.calls) != 3 {
		t.Fatalf("group enter should attempt every child, calls=%v", f.calls)
	}
	if !strings.Contains(m.feedback, "2 restored") || !strings.Contains(m.feedback, "1 refused") {
		t.Fatalf("mixed group feedback should summarize successes and refusals, got %q", m.feedback)
	}
	// The two that succeeded are now enabled.
	m, _ = send(m, keyMsg(tea.KeyRight))
	v := m.View()
	if !strings.Contains(lineWith(v, ".claude/rules/a.md"), "[x]") {
		t.Fatalf("a.md should be restored:\n%s", v)
	}
	if !strings.Contains(lineWith(v, ".claude/rules/c.md"), "[ ]") {
		t.Fatalf("c.md should stay off:\n%s", v)
	}
}

func TestModelQuitKeys(t *testing.T) {
	f := &fakeRepo{items: navItems()}
	for _, msg := range []tea.Msg{runeMsg('q'), keyMsg(tea.KeyEsc), keyMsg(tea.KeyCtrlC)} {
		m := newTestModel(f)
		_, cmd := send(m, msg)
		if cmd == nil {
			t.Fatalf("%v should return a quit command", msg)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%v should return tea.Quit, got %T", msg, cmd())
		}
	}
}

func TestModelViewSectionsMachineryAndHints(t *testing.T) {
	f := &fakeRepo{
		items: []Item{
			{Label: "AGENTS.md", Rel: "AGENTS.md", Stage: StageSessionStart, Toggleable: true, Enabled: true},
			{Label: "adr", Rel: "adr", Stage: StagePreCommit, IsGate: true, Toggleable: true, Enabled: true},
		},
		machinery: 3,
	}
	m := newTestModel(f)
	v := m.View()

	if !strings.Contains(v, StageTitle(StageSessionStart)) {
		t.Errorf("view should contain the SESSION START header:\n%s", v)
	}
	if !strings.Contains(v, StageTitle(StagePreCommit)) {
		t.Errorf("view should contain the PRE-COMMIT header:\n%s", v)
	}
	if strings.Contains(v, StageTitle(StageOnDemand)) {
		t.Errorf("view should omit empty-section headers (ON DEMAND):\n%s", v)
	}
	if !strings.Contains(v, "machinery") {
		t.Errorf("view should show the machinery line when machinery > 0:\n%s", v)
	}
	if !strings.Contains(v, "↑↓ move · →← expand · enter/space toggle · q quit") {
		t.Errorf("view should show the key-hint footer:\n%s", v)
	}
}
