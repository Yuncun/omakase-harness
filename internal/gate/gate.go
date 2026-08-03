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
	Purpose   string   // author-written "what this enforces" (status display only)
}

// Advisory is one declared session-start check (#218): a name, the command
// line, and an optional purpose. Unlike a gate it never blocks anything — its
// exit code is ignored, its output passes through, and the stage is fixed
// (session start), so there is no hook/glob/cacheable to declare.
type Advisory struct {
	Name    string // display name; shares the gate namespace
	Run     string // command line, executed via `sh -c` from the repo root
	Purpose string // author-written "what this watches" (status display only)
}

// reGateName is the gate-name charset: [A-Za-z0-9._-]+.
var reGateName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// blockKeys are the keys allowed inside a gate block; any other indented key
// refuses the whole harness at init.
var blockKeys = map[string]bool{"hook": true, "run": true, "glob": true, "cacheable": true, "purpose": true}

// advisoryKeys are the keys allowed inside an advisory block — no hook (the
// stage is fixed), no glob/cacheable (nothing to scope or cache: it always
// runs and never blocks).
var advisoryKeys = map[string]bool{"run": true, "purpose": true}

// Parse reads the gate: and advisory: blocks out of a flat omakase.manifest.
// A `gate: <name>` or `advisory: <name>` line at column 0 opens a block;
// indented `key: value` lines belong to it until the next column-0 line.
// Top-level lines with any other key (name:, version:, recommends:, blanks,
// comments) are the manifest header and are ignored.
//
// It enforces the schema fully — a bad name, a duplicate name (gates and
// advisories share one namespace), an unknown key inside a block, a missing
// required key, a bad hook stage, or a bad cacheable value returns an error
// (init turns that into the whole-harness refusal; hook time treats a corrupt
// snapshot as fail-closed for gates and as silence for advisories). The run:
// first token is NOT checked here — that check needs the payload dir and
// lives in ValidateRunnable.
func Parse(content []byte) ([]Gate, []Advisory, error) {
	var gates []Gate
	var advisories []Advisory
	seen := map[string]bool{}
	cur := -1           // index into the open block's slice, or -1 for none
	inAdvisory := false // whether cur indexes advisories rather than gates

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
			if !ok || (key != "gate" && key != "advisory") {
				cur = -1 // header line (name/version/recommends/…): ignored
				continue
			}
			name := val
			if !reGateName.MatchString(name) {
				return nil, nil, fmt.Errorf("%s name %q is not [A-Za-z0-9._-]+", key, name)
			}
			if seen[name] {
				return nil, nil, fmt.Errorf("duplicate %s %q", key, name)
			}
			seen[name] = true
			if key == "advisory" {
				advisories = append(advisories, Advisory{Name: name})
				cur, inAdvisory = len(advisories)-1, true
			} else {
				gates = append(gates, Gate{Name: name})
				cur, inAdvisory = len(gates)-1, false
			}
			continue
		}
		// Indented line: a key inside the current block.
		if cur < 0 {
			return nil, nil, fmt.Errorf("indented line %q outside any gate block", strings.TrimSpace(raw))
		}
		if inAdvisory {
			a := &advisories[cur]
			if !ok {
				return nil, nil, fmt.Errorf("advisory %q: malformed line %q (want key: value)", a.Name, strings.TrimSpace(raw))
			}
			if !advisoryKeys[key] {
				return nil, nil, fmt.Errorf("advisory %q: unknown key %q", a.Name, key)
			}
			if key == "run" {
				a.Run = val
			} else {
				a.Purpose = val
			}
			continue
		}
		if !ok {
			return nil, nil, fmt.Errorf("gate %q: malformed line %q (want key: value)", gates[cur].Name, strings.TrimSpace(raw))
		}
		if !blockKeys[key] {
			return nil, nil, fmt.Errorf("gate %q: unknown key %q", gates[cur].Name, key)
		}
		switch key {
		case "hook":
			gates[cur].Hook = val
		case "run":
			gates[cur].Run = val
		case "glob":
			gates[cur].Glob = strings.Fields(val)
		case "purpose":
			gates[cur].Purpose = val
		case "cacheable":
			switch val {
			case "true":
				gates[cur].Cacheable = true
			case "false":
				gates[cur].Cacheable = false
			default:
				return nil, nil, fmt.Errorf("gate %q: cacheable must be true or false, got %q", gates[cur].Name, val)
			}
		}
	}

	for _, g := range gates {
		if g.Hook != "pre-commit" && g.Hook != "pre-push" {
			if g.Hook == "" {
				return nil, nil, fmt.Errorf("gate %q: missing required key hook:", g.Name)
			}
			return nil, nil, fmt.Errorf("gate %q: hook: must be pre-commit or pre-push, got %q", g.Name, g.Hook)
		}
		if g.Run == "" {
			return nil, nil, fmt.Errorf("gate %q: missing required key run:", g.Name)
		}
	}
	for _, a := range advisories {
		if a.Run == "" {
			return nil, nil, fmt.Errorf("advisory %q: missing required key run:", a.Name)
		}
	}
	return gates, advisories, nil
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
// yml scan to the manifest (init only). When a gate's or advisory's run: first
// token is a path inside the harness (a gates/… or .omakase/… path), that file
// must exist in payloadDir and be executable — otherwise the check would fail
// at fire time with exit 127. A first token that is not a payload path (e.g.
// `go`) is the author's own command and is accepted as-is.
func ValidateRunnable(gates []Gate, advisories []Advisory, payloadDir string) error {
	for _, g := range gates {
		if err := runnable("gate", g.Name, g.Run, payloadDir); err != nil {
			return err
		}
	}
	for _, a := range advisories {
		if err := runnable("advisory", a.Name, a.Run, payloadDir); err != nil {
			return err
		}
	}
	return nil
}

// runnable checks one run: line against the payload dir.
func runnable(kind, name, run, payloadDir string) error {
	tok := firstToken(run)
	if !isPayloadPath(tok) {
		return nil
	}
	full := filepath.Join(payloadDir, filepath.FromSlash(tok))
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%s %q: run references %q, which the payload does not ship", kind, name, tok)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("%s %q: run references %q, which is not executable in the payload", kind, name, tok)
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
	gates, _, perr := Parse(content)
	return gates, perr
}

// LoadAdvisories parses the advisory blocks from the snapshot manifest.
// Advisory, so fail-open where Load's gate callers fail closed: a missing or
// unparseable manifest means no advisories, never an error — nothing here may
// disturb a session start.
func LoadAdvisories(omk string) []Advisory {
	content, err := os.ReadFile(snapshotManifest(omk))
	if err != nil {
		return nil
	}
	_, advisories, perr := Parse(content)
	if perr != nil {
		return nil
	}
	return advisories
}

// DeclaredCount is Load distinguishing absence: the number of gate blocks
// in the snapshot manifest, or -1 when the manifest is missing or
// unparseable. Load's missing-means-zero collapse is right for the runner
// (nothing to run either way) but not for the probe, which must not read a
// legacy manifest-less install as a deliberate steering-only one (#149).
func DeclaredCount(omk string) int {
	content, err := os.ReadFile(snapshotManifest(omk))
	if err != nil {
		return -1
	}
	gates, _, perr := Parse(content)
	if perr != nil {
		return -1
	}
	return len(gates)
}

// LoadName returns the manifest header's `name:` value from the snapshot
// manifest in the shared zone — the harness's declared identity. "" when the
// manifest is missing or declares no name. Only column-0 lines before any
// gate block are header lines; a `name:` inside a gate block is that block's
// (refused) key, never the harness name.
func LoadName(omk string) string {
	content, err := os.ReadFile(snapshotManifest(omk))
	if err != nil {
		return ""
	}
	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		raw := sc.Text()
		if raw == "" || raw[0] == ' ' || raw[0] == '\t' || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		key, val, ok := splitKV(raw)
		if !ok {
			continue
		}
		if key == "gate" || key == "advisory" {
			return "" // declaration blocks begin; no header name declared
		}
		if key == "name" {
			return val
		}
	}
	return ""
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
