// model.go is the interactive `omakase status` screen: a Bubble Tea model over
// the Item list built in items.go. It owns navigation (cursor over item/child
// rows, never section headers), group expand/collapse, per-item toggling
// (immediate apply on Enter, Enter again undoes), a one-line feedback message,
// and the styled render. All toggle side effects go through the Toggler
// interface (Task 8 wires the live repo); after any successful toggle the model
// re-derives its items via reload and clamps the cursor.
package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Toggler is the side-effecting half of the screen: the four consent
// operations Enter dispatches. Task 8 implements it over the live overlay; the
// model only cares that each returns nil on success or a refusal/error to
// surface. Gate ops take a gate name; File ops take a placed rel.
type Toggler interface {
	GateOff(name string) error
	GateOn(name string) error
	FileOff(rel string) error
	FileOn(rel string) error
}

// keyHint is the fixed footer line (spec §Screen).
const keyHint = "↑↓ move · →← expand · enter/space toggle · q quit"

// machineryLine is the fixed, non-selectable summary shown when machinery > 0.
const machineryLine = "· machinery: .omakase/ + omakase.manifest (omakase remove)"

// Styles. AdaptiveColor lets lipgloss pick the light/dark variant and degrade
// to plain text when the terminal has no color — never hardcode raw ANSI.
var (
	styHeader  = lipgloss.NewStyle().Bold(true)
	styFoot    = lipgloss.NewStyle().Faint(true)
	stySection = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "236", Dark: "252"})
	styOK      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "22", Dark: "42"})
	styErr     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "124", Dark: "203"})
)

// Model is the interactive status screen. It implements tea.Model.
type Model struct {
	header    string
	footprint string
	items     []Item
	machinery int
	toggler   Toggler
	reload    func() ([]Item, int)

	cursor   int             // index into visible()
	expanded map[string]bool // group Rel -> expanded
	feedback string          // one-line message above the key hints
	fbErr    bool            // feedback is a refusal/error (render red)
	fbView   bool            // feedback is a neutral view-only note (render faint)

	width  int
	height int
}

// NewModel builds the interactive status Model. reload re-derives the items and
// machinery count after a successful toggle (Task 8 passes a closure over the
// live repo). width/height default to a sensible 80x24 until the first
// WindowSizeMsg arrives.
func NewModel(header, footprint string, items []Item, machinery int, t Toggler, reload func() ([]Item, int)) Model {
	return Model{
		header:    header,
		footprint: footprint,
		items:     items,
		machinery: machinery,
		toggler:   t,
		reload:    reload,
		expanded:  map[string]bool{},
		width:     80,
		height:    24,
	}
}

// vrow is one selectable line: an item row (child == -1) or an expanded group's
// child row (child >= 0).
type vrow struct {
	item  int
	child int
}

// visible flattens items (in Stage order) into the selectable rows the cursor
// walks. Expanded groups contribute one row per child right after their own
// row. Section headers and the machinery line are NOT rows — they are never
// cursor targets.
func (m Model) visible() []vrow {
	var rows []vrow
	for s := StageSessionStart; s <= StageOther; s++ {
		for i := range m.items {
			it := m.items[i]
			if it.Stage != s {
				continue
			}
			rows = append(rows, vrow{item: i, child: -1})
			if it.Group && m.expanded[it.Rel] {
				for c := range it.Children {
					rows = append(rows, vrow{item: i, child: c})
				}
			}
		}
	}
	return rows
}

func (m Model) Init() tea.Cmd { return nil }

// Update handles window resizes and key presses; all other messages are no-ops.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		return m.moveCursor(-1), nil
	case tea.KeyDown:
		return m.moveCursor(1), nil
	case tea.KeyRight:
		return m.expandCurrent(), nil
	case tea.KeyLeft:
		return m.collapseCurrent(), nil
	case tea.KeyEnter, tea.KeySpace:
		return m.toggleCurrent(), nil
	case tea.KeyEsc, tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "k":
			return m.moveCursor(-1), nil
		case "j":
			return m.moveCursor(1), nil
		case "q":
			return m, tea.Quit
		}
	}
	return m, nil
}

// moveCursor shifts the cursor by delta over the visible rows, clamping at both
// ends and clearing any stale feedback.
func (m Model) moveCursor(delta int) Model {
	m.feedback, m.fbErr, m.fbView = "", false, false
	m.cursor += delta
	m.clampCursor()
	return m
}

func (m *Model) clampCursor() {
	n := len(m.visible())
	if n == 0 {
		m.cursor = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= n {
		m.cursor = n - 1
	}
}

// expandCurrent opens the group under the cursor (→). No-op on non-group or
// child rows.
func (m Model) expandCurrent() Model {
	m.feedback, m.fbErr, m.fbView = "", false, false
	rows := m.visible()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return m
	}
	r := rows[m.cursor]
	if r.child == -1 {
		it := m.items[r.item]
		if it.Group {
			m.expanded[it.Rel] = true
		}
	}
	return m
}

// collapseCurrent closes the group under the cursor (←). On a child row it
// collapses the parent and moves the cursor back onto the group row.
func (m Model) collapseCurrent() Model {
	m.feedback, m.fbErr, m.fbView = "", false, false
	rows := m.visible()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return m
	}
	r := rows[m.cursor]
	it := m.items[r.item]
	if it.Group && m.expanded[it.Rel] {
		m.expanded[it.Rel] = false
		m.cursor = m.rowIndexOf(r.item, -1)
		m.clampCursor()
	}
	return m
}

// rowIndexOf returns the visible index of the (item, child) row, or 0 if absent.
func (m Model) rowIndexOf(item, child int) int {
	for i, r := range m.visible() {
		if r.item == item && r.child == child {
			return i
		}
	}
	return 0
}

// toggleCurrent dispatches Enter on the row under the cursor.
func (m Model) toggleCurrent() Model {
	rows := m.visible()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return m
	}
	r := rows[m.cursor]
	it := m.items[r.item]

	if r.child >= 0 { // an expanded group's child: toggle just this child
		ch := it.Children[r.child]
		m.applyFile(ch.Rel, ch.Enabled)
		return m
	}
	if !it.Toggleable { // view-only row: explain, never toggle
		m.feedback, m.fbErr, m.fbView = viewOnlyNote(it), false, true
		return m
	}
	switch {
	case it.IsGate:
		m.applyGate(it)
	case it.Group:
		m.applyGroup(it)
	default:
		m.applyFile(it.Rel, it.Enabled)
	}
	return m
}

func (m *Model) applyGate(it Item) {
	var err error
	if it.Enabled {
		err = m.toggler.GateOff(it.Rel)
	} else {
		err = m.toggler.GateOn(it.Rel)
	}
	if err != nil {
		m.refuse(err)
		return
	}
	if it.Enabled {
		m.success(it.Rel + " off — skipped visibly at commit/push")
	} else {
		m.success(it.Rel + " back on")
	}
	m.afterToggle()
}

func (m *Model) applyFile(rel string, enabled bool) {
	var err error
	if enabled {
		err = m.toggler.FileOff(rel)
	} else {
		err = m.toggler.FileOn(rel)
	}
	if err != nil {
		m.refuse(err)
		return
	}
	if enabled {
		m.success(rel + " removed")
	} else {
		m.success(rel + " restored")
	}
	m.afterToggle()
}

// applyGroup toggles every child of a group in one Enter. An all-on group turns
// off; otherwise all children turn on. Per-child refusals are counted and
// summarized rather than aborting the batch.
func (m *Model) applyGroup(it Item) {
	off := it.Enabled // all children on -> turn the group off
	done, refused := 0, 0
	var firstErr error
	for _, ch := range it.Children {
		var err error
		if off {
			err = m.toggler.FileOff(ch.Rel)
		} else {
			err = m.toggler.FileOn(ch.Rel)
		}
		if err != nil {
			refused++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		done++
	}
	verb := "restored"
	if off {
		verb = "removed"
	}
	switch {
	case refused == 0:
		m.success(fmt.Sprintf("%d %s", done, verb))
	case done == 0:
		m.refuse(firstErr)
		return
	default:
		m.feedback = fmt.Sprintf("✓ %d %s, %d refused: %v", done, verb, refused, firstErr)
		m.fbErr, m.fbView = false, false
	}
	if done > 0 {
		m.afterToggle()
	}
}

func (m *Model) success(msg string) {
	m.feedback, m.fbErr, m.fbView = "✓ "+msg, false, false
}

func (m *Model) refuse(err error) {
	m.feedback, m.fbErr, m.fbView = "REFUSING: "+err.Error(), true, false
}

// afterToggle re-derives items from the live repo and keeps the cursor in range.
func (m *Model) afterToggle() {
	if m.reload != nil {
		m.items, m.machinery = m.reload()
	}
	m.clampCursor()
}

// viewOnlyNote explains why a non-toggleable row can't be toggled: a gate that
// just runs at a hook, or a git-tracked file omakase won't delete.
func viewOnlyNote(it Item) string {
	if it.Stage == StagePreCommit || it.Stage == StagePrePush {
		return "· " + it.Rel + " runs at every commit/push — no omakase consent toggle"
	}
	return "· " + it.Rel + " is tracked by the repo — omakase never deletes committed files"
}

// View renders the screen: a fixed header block, the scrollable body of
// sections/rows/machinery, and a fixed footer block (feedback + key hints).
// When the body is taller than the terminal it scrolls to keep the cursor row
// in view.
func (m Model) View() string {
	top := []string{styHeader.Render(m.header)}
	if m.footprint != "" {
		top = append(top, styFoot.Render(m.footprint))
	}
	top = append(top, "")

	body, cursorLine := m.renderBody()

	bottom := []string{""}
	if m.feedback != "" {
		bottom = append(bottom, m.feedbackStyle().Render(m.feedback))
	}
	bottom = append(bottom, styFoot.Render(keyHint))

	avail := m.height - len(top) - len(bottom)
	if avail < 1 {
		avail = 1
	}
	body = window(body, cursorLine, avail)

	all := make([]string, 0, len(top)+len(body)+len(bottom))
	all = append(all, top...)
	all = append(all, body...)
	all = append(all, bottom...)
	return strings.Join(all, "\n")
}

// renderBody builds the section/row/machinery lines and reports the body-line
// index of the cursor row (-1 if none), walking items in the same Stage/child
// order as visible() so a running counter stays in lockstep with the cursor.
func (m Model) renderBody() (lines []string, cursorLine int) {
	cursorLine = -1
	vi := 0
	for s := StageSessionStart; s <= StageOther; s++ {
		var idxs []int
		for i := range m.items {
			if m.items[i].Stage == s {
				idxs = append(idxs, i)
			}
		}
		if len(idxs) == 0 {
			continue // empty sections don't render
		}
		lines = append(lines, stySection.Render(StageTitle(s)))
		for _, i := range idxs {
			it := m.items[i]
			cur := vi == m.cursor
			if cur {
				cursorLine = len(lines)
			}
			lines = append(lines, m.renderItem(it, cur))
			vi++
			if it.Group && m.expanded[it.Rel] {
				for c := range it.Children {
					cc := vi == m.cursor
					if cc {
						cursorLine = len(lines)
					}
					lines = append(lines, m.renderChild(it.Children[c], cc))
					vi++
				}
			}
		}
		lines = append(lines, "")
	}
	if m.machinery > 0 {
		lines = append(lines, "  "+styFoot.Render(machineryLine))
	}
	return lines, cursorLine
}

func (m Model) renderItem(it Item, cursor bool) string {
	off := (it.Toggleable && !it.Enabled && !it.PartialOff) || !it.Toggleable
	return m.renderLine("", checkboxFor(it), it.Label, cursor, off)
}

func (m Model) renderChild(ch ChildRef, cursor bool) string {
	cb := "[ ]"
	if ch.Enabled {
		cb = "[x]"
	}
	return m.renderLine("  ", cb, ch.Rel, cursor, !ch.Enabled)
}

// renderLine composes one row: a cursor marker, an optional child indent, a
// checkbox column, and the (truncated) label. Off/view-only rows are faint;
// the cursor row reverses its label. Truncation keeps the whole line within
// the terminal width.
func (m Model) renderLine(indent, checkbox, label string, cursor, off bool) string {
	prefix := "  "
	if cursor {
		prefix = "▸ "
	}
	lead := prefix + indent + checkbox + " "

	max := m.width - utf8.RuneCountInString(lead)
	if max < 1 {
		max = 1
	}
	label = truncate(label, max)

	labStyle := lipgloss.NewStyle()
	leadStyle := lipgloss.NewStyle()
	if off {
		labStyle = labStyle.Faint(true)
		leadStyle = leadStyle.Faint(true)
	}
	if cursor {
		labStyle = labStyle.Reverse(true)
	}
	return leadStyle.Render(lead) + labStyle.Render(label)
}

func (m Model) feedbackStyle() lipgloss.Style {
	switch {
	case m.fbErr:
		return styErr
	case m.fbView:
		return styFoot
	default:
		return styOK
	}
}

// checkboxFor returns the 3-column checkbox for an item row: `·` (view-only,
// never a real checkbox), `[~]` (group partially off), `[x]` (on), `[ ]` (off).
func checkboxFor(it Item) string {
	if !it.Toggleable {
		return " · "
	}
	if it.Group && it.PartialOff {
		return "[~]"
	}
	if it.Enabled {
		return "[x]"
	}
	return "[ ]"
}

// truncate shortens s to at most max runes, marking a cut with an ellipsis.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// window slices lines to at most avail entries, keeping cursorLine roughly
// centered when the content overflows.
func window(lines []string, cursorLine, avail int) []string {
	if len(lines) <= avail {
		return lines
	}
	start := 0
	if cursorLine >= 0 {
		start = cursorLine - avail/2
	}
	if start < 0 {
		start = 0
	}
	if start > len(lines)-avail {
		start = len(lines) - avail
	}
	return lines[start : start+avail]
}

var _ tea.Model = Model{}
