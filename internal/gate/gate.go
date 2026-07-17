// Package gate owns omakase's own concept of "a gate": a check declared in
// omakase.manifest and run by the omakase binary at a git hook. It replaces
// the third-party runner (lefthook) and the omakase-gate.sh wrapper — no part
// of the product knows any runner's file format anymore.
//
// Everything here is a direct port of the verified semantics of the deleted
// payload/.omakase/bin/omakase-gate.sh (163 lines of sh): per-gate audited
// skip env, the menu disabled-gates file, glob scoping with the
// no-base-runs-unscoped and unrelated-history fallbacks, cache-by-HEAD-sha,
// running the check via `sh -c` from the repo root, and the append-only ledger
// row `epoch \t name \t verdict \t sha`. The ledger format is byte-identical to
// the script's; internal/probe and internal/state parse it unchanged.
package gate

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Gate is one declared check: a name, the hook stage it runs at, the command
// line, optional glob scope, and whether a recorded PASS for the exact HEAD
// short-circuits it.
type Gate struct {
	Name      string   // ledger/scorecard name and the OMAKASE_SKIP_<NAME> name
	Hook      string   // "pre-commit" or "pre-push"
	Run       string   // command line, executed via `sh -c` from the repo root
	Glob      []string // space-split case patterns; nil = always in scope
	Cacheable bool     // a recorded PASS for the exact HEAD sha short-circuits
}

// reGateName is the gate-name charset: [A-Za-z0-9._-]+.
var reGateName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// blockKeys are the keys allowed inside a gate block; any other indented key
// refuses the whole harness at init.
var blockKeys = map[string]bool{"hook": true, "run": true, "glob": true, "cacheable": true}

// Parse reads the gate: blocks out of a flat omakase.manifest. A `gate: <name>`
// line at column 0 opens a block; indented `key: value` lines belong to it
// until the next column-0 line. Top-level non-gate lines (name:, version:,
// recommends:, blanks, comments) are the manifest header and are ignored.
//
// It enforces the schema fully — a bad gate name, a duplicate name, an unknown
// key inside a block, a missing required key (hook/run), a bad hook stage, or a
// bad cacheable value returns an error (init turns that into the whole-harness
// refusal; hook time treats a corrupt snapshot as fail-closed). The run: first
// token is NOT checked here — that check needs the payload dir and lives in
// ValidateRunnable.
func Parse(content []byte) ([]Gate, error) {
	var gates []Gate
	seen := map[string]bool{}
	cur := -1 // index into gates of the open block, or -1 for none

	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		raw := sc.Text()
		// Blank lines and full-line comments end nothing and declare nothing.
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		indented := raw[0] == ' ' || raw[0] == '\t'
		key, val, ok := splitKV(raw)
		if !indented {
			// A column-0 line closes any open block.
			if !ok {
				cur = -1
				continue
			}
			if key != "gate" {
				cur = -1 // header line (name/version/recommends/…): ignored
				continue
			}
			name := val
			if !reGateName.MatchString(name) {
				return nil, fmt.Errorf("gate name %q is not [A-Za-z0-9._-]+", name)
			}
			if seen[name] {
				return nil, fmt.Errorf("duplicate gate %q", name)
			}
			seen[name] = true
			gates = append(gates, Gate{Name: name})
			cur = len(gates) - 1
			continue
		}
		// Indented line: a key inside the current block.
		if cur < 0 {
			return nil, fmt.Errorf("indented line %q outside any gate block", strings.TrimSpace(raw))
		}
		if !ok {
			return nil, fmt.Errorf("gate %q: malformed line %q (want key: value)", gates[cur].Name, strings.TrimSpace(raw))
		}
		if !blockKeys[key] {
			return nil, fmt.Errorf("gate %q: unknown key %q", gates[cur].Name, key)
		}
		switch key {
		case "hook":
			gates[cur].Hook = val
		case "run":
			gates[cur].Run = val
		case "glob":
			gates[cur].Glob = strings.Fields(val)
		case "cacheable":
			switch val {
			case "true":
				gates[cur].Cacheable = true
			case "false":
				gates[cur].Cacheable = false
			default:
				return nil, fmt.Errorf("gate %q: cacheable must be true or false, got %q", gates[cur].Name, val)
			}
		}
	}

	for _, g := range gates {
		if g.Hook != "pre-commit" && g.Hook != "pre-push" {
			if g.Hook == "" {
				return nil, fmt.Errorf("gate %q: missing required key hook:", g.Name)
			}
			return nil, fmt.Errorf("gate %q: hook: must be pre-commit or pre-push, got %q", g.Name, g.Hook)
		}
		if g.Run == "" {
			return nil, fmt.Errorf("gate %q: missing required key run:", g.Name)
		}
	}
	return gates, nil
}

// splitKV splits a "key: value" line into its key and value. The key is
// everything before the first colon, trimmed of surrounding whitespace; the
// value is everything after, trimmed of surrounding whitespace. ok is false
// when there is no colon.
func splitKV(line string) (key, val string, ok bool) {
	i := strings.IndexByte(line, ':')
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
}

// ValidateRunnable is the "nothing runs undeclared" check, moved from the old
// yml scan to the manifest (init only). When a gate's run: first token is a
// path inside the harness (a gates/… or .omakase/… path), that file must exist
// in payloadDir and be executable — otherwise the gate would fail at commit
// time with exit 127. A first token that is not a payload path (e.g. `go`) is
// the author's own command and is accepted as-is.
func ValidateRunnable(gates []Gate, payloadDir string) error {
	for _, g := range gates {
		tok := firstToken(g.Run)
		if !isPayloadPath(tok) {
			continue
		}
		full := filepath.Join(payloadDir, filepath.FromSlash(tok))
		info, err := os.Stat(full)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("gate %q: run references %q, which the payload does not ship", g.Name, tok)
		}
		if info.Mode()&0o111 == 0 {
			return fmt.Errorf("gate %q: run references %q, which is not executable in the payload", g.Name, tok)
		}
	}
	return nil
}

// firstToken returns the first whitespace-separated token of a command line.
func firstToken(run string) string {
	f := strings.Fields(run)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// isPayloadPath reports whether tok names a file inside the harness payload —
// a gates/… or .omakase/… path (with or without a leading ./). Anything else
// is the author's command, resolved from PATH.
func isPayloadPath(tok string) bool {
	tok = strings.TrimPrefix(tok, "./")
	return strings.HasPrefix(tok, "gates/") || strings.HasPrefix(tok, ".omakase/")
}

// snapshotManifest is the placed manifest's snapshot copy — the one-writer
// wiring source. init copies the payload's omakase.manifest here; hook time
// reads gates only from here, never from the (editable) working copy.
func snapshotManifest(omk string) string {
	return filepath.Join(omk, "payload-snapshot", "omakase.manifest")
}

// Load parses the gate blocks from the snapshot manifest in the shared zone.
// A missing manifest means no declared gates (nil, nil), not an error.
func Load(omk string) ([]Gate, error) {
	content, err := os.ReadFile(snapshotManifest(omk))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return Parse(content)
}

// ForHook returns the gates declared for one hook stage, in manifest order.
func ForHook(gates []Gate, hook string) []Gate {
	var out []Gate
	for _, g := range gates {
		if g.Hook == hook {
			out = append(out, g)
		}
	}
	return out
}

// snapshotHasLefthookMarker reports whether the init-written snapshot still
// carries a lefthook-era artifact — payload-snapshot/lefthook-local.yml or the
// deleted payload-snapshot/.omakase/bin/omakase-gate.sh. Its presence is the
// fingerprint of a repo initialized before the gate module: that snapshot
// declares no gate blocks, so the upgraded binary would run zero gates here
// while every other proof still reads green (the #72 status-lie).
func snapshotHasLefthookMarker(omk string) bool {
	snap := filepath.Join(omk, "payload-snapshot")
	for _, rel := range []string{
		"lefthook-local.yml",
		filepath.Join(".omakase", "bin", "omakase-gate.sh"),
	} {
		if _, err := os.Stat(filepath.Join(snap, rel)); err == nil {
			return true
		}
	}
	return false
}

// StaleLefthookSnapshot reports whether omk's snapshot is a pre-gate-module
// (lefthook-era) snapshot: it still carries a lefthook-era marker AND declares
// no gate blocks. Both the hook (fail closed, run.go) and the probe (Problem,
// internal/probe) key on this one predicate, so status and commit time agree.
// A genuinely gate-less current harness — a manifest present with no gate
// blocks and no lefthook marker in the snapshot — is not stale and still
// passes.
func StaleLefthookSnapshot(omk string) (bool, error) {
	if !snapshotHasLefthookMarker(omk) {
		return false, nil
	}
	gates, err := Load(omk)
	if err != nil {
		return false, err
	}
	return len(gates) == 0, nil
}
