// Package block implements `omakase block <item>` / `omakase unblock <item>`:
// per-item consent over the repo's own COMMITTED agent config (issue #193).
// Where `omakase status --disable` toggles files omakase placed, block toggles
// what the repo itself ships — the half the user never chose. This file holds
// the item resolution shared by both verbs; the mechanism that makes a blocked
// item stop steering agents lives in its own file.
package block

import (
	"fmt"
	"sort"
	"strings"
)

// shorthandOwners maps a bare item name (no '/') to the committed paths it
// may address: the three project skill roots both hosts document, plus Claude
// Code's per-file agents and commands. A skill is a directory (the name
// addresses everything under it); agents and commands are single files.
var shorthandOwners = []struct {
	prefix string // rel must equal prefix+name (+file) or live under it (+dir)
	suffix string // "" for a directory item, the file extension otherwise
}{
	{".claude/skills/", ""},
	{".agents/skills/", ""},
	{".github/skills/", ""},
	{".claude/agents/", ".md"},
	{".claude/commands/", ".md"},
}

// Resolve maps the user's argument to one blockable item against the repo's
// committed harness surface (status.CommittedList order). It returns the
// canonical item rel — an exact committed path, or a directory covering
// committed paths — and the committed rows the item covers. A bare name (no
// '/') additionally resolves as a skill/agent/command shorthand; a name owned
// by more than one path is ambiguous and returns an error listing the
// candidates rather than guessing.
func Resolve(committed []string, arg string) (item string, covered []string, err error) {
	norm := strings.TrimSuffix(strings.TrimPrefix(arg, "./"), "/")
	if norm == "" || strings.HasPrefix(norm, "-") {
		return "", nil, fmt.Errorf("%q is not an item name or path", arg)
	}

	for _, rel := range committed {
		if rel == norm {
			return norm, []string{rel}, nil
		}
	}
	prefix := norm + "/"
	for _, rel := range committed {
		if strings.HasPrefix(rel, prefix) {
			covered = append(covered, rel)
		}
	}
	if len(covered) > 0 {
		return norm, covered, nil
	}

	if !strings.Contains(norm, "/") {
		return resolveShorthand(committed, norm)
	}
	return "", nil, notCommittedErr(norm)
}

// resolveShorthand resolves a bare name against shorthandOwners. Exactly one
// owning item wins; zero is unknown, several is ambiguous.
func resolveShorthand(committed []string, name string) (string, []string, error) {
	found := map[string][]string{}
	for _, o := range shorthandOwners {
		var itm string
		if o.suffix == "" {
			itm = o.prefix + name
		} else {
			itm = o.prefix + name + o.suffix
		}
		for _, rel := range committed {
			if rel == itm || (o.suffix == "" && strings.HasPrefix(rel, itm+"/")) {
				found[itm] = append(found[itm], rel)
			}
		}
	}
	switch len(found) {
	case 0:
		return "", nil, notCommittedErr(name)
	case 1:
		for itm, covered := range found {
			return itm, covered, nil
		}
	}
	items := make([]string, 0, len(found))
	for itm := range found {
		items = append(items, itm)
	}
	sort.Strings(items)
	return "", nil, fmt.Errorf("%q matches more than one committed item — use the path: %s", name, strings.Join(items, ", "))
}

func notCommittedErr(name string) error {
	return fmt.Errorf("%s is not part of this repo's committed agent config (the list: omakase status)", name)
}
