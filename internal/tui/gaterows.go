// gaterows.go turns the declared gates (internal/gate, read from the snapshot
// manifest) into GateRow values the interactive status screen itemizes. Every
// declared gate is a consent-tracked gate — declaration IS wiring — so the old
// "gate vs. plain lefthook job" distinction is gone; GateRow.Gate is always
// true and kept only so BuildItems and the MCP menu keep their existing shape.
package tui

import (
	"github.com/Yuncun/omakase-harness/internal/gate"
)

// GateRow is one declared gate: the hook stage it runs at and its name. Gate is
// always true (kept for BuildItems / mcpserver, which branch on it).
type GateRow struct {
	Hook string
	Name string
	Gate bool
}

// GateRows loads the declared gates from the snapshot manifest under omk, in
// manifest order — nil when none are declared or the manifest is unreadable.
func GateRows(omk string) []GateRow {
	gates, err := gate.Load(omk)
	if err != nil || len(gates) == 0 {
		return nil
	}
	rows := make([]GateRow, 0, len(gates))
	for _, g := range gates {
		rows = append(rows, GateRow{Hook: g.Hook, Name: g.Name, Gate: true})
	}
	return rows
}
