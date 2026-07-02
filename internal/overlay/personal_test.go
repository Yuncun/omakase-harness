package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/state"
)

// These are the `omakase personal` verb tests (Task 5). personal is a NEW v2
// verb with NO bin/*.sh original and NO v1 byte oracle, so every expected string
// is specified verbatim by the Phase 3 plan (Task 5) and pinned here directly —
// there is nothing to bash-capture. Path-bearing expectations are CONSTRUCTED
// from known inputs (the source path, repo.OMK) at test time, matching the rest
// of the suite. Tests share process cwd + env via chdir/t.Setenv and must not run
// in parallel.
//
// Shared helpers (initRepo, stubLefthook, srcTestEnv, useBasePayloadDir,
// newSourceRepo, newPersonalSource, setPersonalConfig, isolatePersonalConfig,
// commitAll, writeFile, readFileT, eq, chdir, sha256hex) live in init_test.go /
// init_layers_test.go / source_test.go / overlay_test.go.

// personalCfgPath isolates XDG_CONFIG_HOME to a fresh temp dir (so no real
// ~/.config is touched) and returns the personal config file path the verb will
// read/write.
func personalCfgPath(t *testing.T) string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	return filepath.Join(cfg, "omakase", "personal")
}

// ---------------------------------------------------------------- show arm

func TestPersonalShowUnset(t *testing.T) {
	isolatePersonalConfig(t) // empty XDG_CONFIG_HOME

	var stdout, stderr strings.Builder
	if code := RunPersonal(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), "personal harness: (none)\n")
	eq(t, "stderr", stderr.String(), "")
}

func TestPersonalShowSet(t *testing.T) {
	setPersonalConfig(t, "you/my-harness#v2")

	var stdout, stderr strings.Builder
	if code := RunPersonal(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), "personal harness: you/my-harness#v2\n")
	eq(t, "stderr", stderr.String(), "")
}

// ---------------------------------------------------------------- set arm

// TestPersonalSetHappyUninitialized: a valid source, set from a NON-repo cwd.
// The config file gets the resolved source (one line), the set line prints, and —
// because there is no initialized repo — NOTHING else prints (no apply, no
// validation "cached at" chatter).
func TestPersonalSetHappyUninitialized(t *testing.T) {
	srcTestEnv(t)
	cfg := personalCfgPath(t)
	psrc := newPersonalSource(t, map[string]string{"AGENTS.md": "doc\n"})
	chdir(t, t.TempDir()) // a plain dir: state.Discover fails, so no apply

	var stdout, stderr strings.Builder
	if code := RunPersonal([]string{"--source", psrc}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(),
		"omakase: personal harness set to "+psrc+" — layered on every omakase init from now on.\n")
	eq(t, "stderr", stderr.String(), "")
	eq(t, "config bytes", readFileT(t, cfg), psrc+"\n")
}

// TestPersonalSetRoundTripThroughInit: the config a `personal` set writes is read
// back by a fresh init and layered (plan Task 5 "config-file round-trip").
func TestPersonalSetRoundTripThroughInit(t *testing.T) {
	srcTestEnv(t)
	isolatePersonalConfig(t) // stable XDG_CONFIG_HOME across the set + the init
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "gates", "base.sh"), "base gate\n")
	t.Setenv("OMAKASE_PAYLOAD", base) // base-as-bottom resolves via OMAKASE_PAYLOAD
	psrc := newPersonalSource(t, map[string]string{"AGENTS.md": "personal doctrine\n"})

	// (1) set from a non-repo cwd — writes the config, no apply.
	chdir(t, t.TempDir())
	var so, se strings.Builder
	if code := RunPersonal([]string{"--source", psrc}, &so, &se); code != 0 {
		t.Fatalf("set exit = %d; stderr=%q", code, se.String())
	}

	// (2) init a fresh repo — the remembered personal config layers in.
	dir, repo := initRepo(t)
	stubLefthook(t)
	var io2, ie2 strings.Builder
	if code := RunInit(nil, &io2, &ie2); code != 0 {
		t.Fatalf("init exit = %d; stderr=%q", code, ie2.String())
	}
	eq(t, "personal CLAUDE.local.md layered", readFileT(t, filepath.Join(dir, "CLAUDE.local.md")), "personal doctrine\n")
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 1 || rows[0].Layer != "personal" || rows[0].Source != psrc {
		t.Fatalf("sources.tsv = %+v, want one personal %s row", rows, psrc)
	}
}

// TestPersonalSetAppliesToInitializedRepo: setting a source in an initialized
// repo announces the apply and re-runs the layering engine (bare init), so the
// personal layer lands immediately.
func TestPersonalSetAppliesToInitializedRepo(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t) // empty base folded under the project
	isolatePersonalConfig(t)

	proj := newSourceRepo(t)
	writeFile(t, filepath.Join(proj, "omakase.manifest"), "name: proj\n")
	writeFile(t, filepath.Join(proj, "payload", ".claude", "rules", "r.md"), "proj rule\n")
	commitAll(t, proj, "proj")

	// Install the project first (no personal yet).
	var oi, ei strings.Builder
	if code := RunInit([]string{"--source", proj}, &oi, &ei); code != 0 {
		t.Fatalf("project init exit = %d; stderr=%q", code, ei.String())
	}

	// Now set a personal source — it applies to this repo immediately.
	psrc := newPersonalSource(t, map[string]string{"AGENTS.md": "personal doctrine\n"})
	var stdout, stderr strings.Builder
	if code := RunPersonal([]string{"--source", psrc}, &stdout, &stderr); code != 0 {
		t.Fatalf("personal set exit = %d; stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	setLine := "omakase: personal harness set to " + psrc + " — layered on every omakase init from now on.\n"
	applyLine := "omakase: applying to this repo now (bare init).\n"
	si, ai := strings.Index(out, setLine), strings.Index(out, applyLine)
	if si < 0 || ai < 0 || si > ai {
		t.Fatalf("set line must precede the apply line:\n%s", out)
	}
	// The personal layer actually landed.
	eq(t, "CLAUDE.local.md applied", readFileT(t, filepath.Join(dir, "CLAUDE.local.md")), "personal doctrine\n")
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 2 || rows[0].Layer != "project" || rows[1].Layer != "personal" || rows[1].Source != psrc {
		t.Fatalf("sources.tsv = %+v, want project then personal(%s)", rows, psrc)
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

// TestPersonalSetOffRowNotApplied: a repo carrying the persisted off-row records
// the setting globally but does NOT apply it here — printing the not-applied line
// instead of the apply line.
func TestPersonalSetOffRowNotApplied(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t)
	cfg := personalCfgPath(t)

	proj := newSourceRepo(t)
	writeFile(t, filepath.Join(proj, "omakase.manifest"), "name: proj\n")
	writeFile(t, filepath.Join(proj, "payload", ".claude", "rules", "r.md"), "proj rule\n")
	commitAll(t, proj, "proj")

	// Install with --no-personal so a persisted off-row lands.
	var oi, ei strings.Builder
	if code := RunInit([]string{"--source", proj, "--no-personal"}, &oi, &ei); code != 0 {
		t.Fatalf("init --no-personal exit = %d; stderr=%q", code, ei.String())
	}

	psrc := newPersonalSource(t, map[string]string{"AGENTS.md": "personal doctrine\n"})
	var stdout, stderr strings.Builder
	if code := RunPersonal([]string{"--source", psrc}, &stdout, &stderr); code != 0 {
		t.Fatalf("personal set exit = %d; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(),
		"omakase: personal harness set to "+psrc+" — layered on every omakase init from now on.\n"+
			"omakase: this repo has personal layering off (init --no-personal); not applied here.\n")
	eq(t, "stderr", stderr.String(), "")
	// Setting was still recorded globally.
	eq(t, "config bytes", readFileT(t, cfg), psrc+"\n")
	// No personal layer applied here.
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.local.md")); err == nil {
		t.Error("personal layer applied despite the off-row")
	}
	// sources.tsv still carries the off-row, unchanged.
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if !state.PersonalOff(rows) {
		t.Errorf("off-row lost: %+v", rows)
	}
}

// TestPersonalSetFailClosed: a broken personal source (no manifest) fails closed
// with the BYTE-IDENTICAL message init's arms print, exit 1, and writes NOTHING
// (no config file).
func TestPersonalSetFailClosed(t *testing.T) {
	srcTestEnv(t)
	cfg := personalCfgPath(t)

	psrc := newSourceRepo(t) // a git repo with a payload but NO manifest
	writeFile(t, filepath.Join(psrc, "payload", "rule.md"), "a rule\n")
	commitAll(t, psrc, "no-manifest")

	var stdout, stderr strings.Builder
	code := RunPersonal([]string{"--source", psrc}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), "")
	if !strings.Contains(stderr.String(), "omakase: source '"+psrc+"' has no omakase.manifest at its root — not an omakase source\n") {
		t.Errorf("refusal not byte-identical to init's arm:\n%s", stderr.String())
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Error("wrote the config despite a fail-closed refusal")
	}
}

// TestPersonalSetTabRejected: a tab in the source is rejected before any fetch or
// write (TSV safety — the resolved source becomes a sources.tsv field later).
func TestPersonalSetTabRejected(t *testing.T) {
	cfg := personalCfgPath(t)
	var stdout, stderr strings.Builder
	code := RunPersonal([]string{"a\tb"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	eq(t, "stderr", stderr.String(), "omakase: --source must not contain a tab or newline\n")
	if _, err := os.Stat(cfg); err == nil {
		t.Error("wrote the config for a tab-bearing source")
	}
}

// ---------------------------------------------------------------- off arm

// TestPersonalOffGlobalOnly: `off` outside an initialized repo clears the global
// setting and stops there.
func TestPersonalOffGlobalOnly(t *testing.T) {
	cfg := personalCfgPath(t)
	writeFile(t, cfg, "you/harness\n")
	chdir(t, t.TempDir()) // not a git repo

	var stdout, stderr strings.Builder
	if code := RunPersonal([]string{"off"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), "omakase: personal harness cleared.\n")
	eq(t, "stderr", stderr.String(), "")
	if _, err := os.Stat(cfg); err == nil {
		t.Error("global config not cleared")
	}
}

// TestPersonalOffUnlayer is the restore matrix: a project+personal stack, then
// `personal off`. A personal-won path with a project copy below is RESTORED
// byte-exact (row rewritten to the project label + hash); a sole-personal clean
// path is DELETED (untracked + hash-match) — including the rerouted CLAUDE.local.md;
// a sole-personal EDITED path is warned and KEPT. The snapshot + placed.tsv +
// exclude are healed to the post-unlayer merged view (no personal files remain),
// the personal row + $OMK/layers/personal are gone.
func TestPersonalOffUnlayer(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	useBasePayloadDir(t) // empty base folded under the project

	proj := newSourceRepo(t)
	writeFile(t, filepath.Join(proj, "omakase.manifest"), "name: proj\n")
	writeFile(t, filepath.Join(proj, "payload", ".claude", "rules", "r.md"), "proj rule\n")
	writeFile(t, filepath.Join(proj, "payload", ".omakase", "gates", "shared.sh"), "PROJECT\n")
	commitAll(t, proj, "proj")

	psrc := newPersonalSource(t, map[string]string{
		"AGENTS.md":                "personal doctrine\n", // -> CLAUDE.local.md, sole-personal clean
		".omakase/gates/shared.sh": "PERSONAL\n",          // overlaps project -> personal wins (restore)
		".omakase/gates/ponly.sh":  "P ONLY\n",            // sole-personal clean (delete)
		".omakase/gates/pedit.sh":  "P EDIT\n",            // sole-personal, will be edited (keep)
	})
	setPersonalConfig(t, psrc)

	// Install the full stack.
	var oi, ei strings.Builder
	if code := RunInit([]string{"--source", proj}, &oi, &ei); code != 0 {
		t.Fatalf("stack init exit = %d; stderr=%q", code, ei.String())
	}
	// Sanity: personal won shared.sh, and the personal-only files are present.
	eq(t, "personal won shared.sh", readFileT(t, filepath.Join(dir, ".omakase", "gates", "shared.sh")), "PERSONAL\n")
	eq(t, "CLAUDE.local.md present", readFileT(t, filepath.Join(dir, "CLAUDE.local.md")), "personal doctrine\n")

	// The user edits a sole-personal file before unlayering.
	writeFile(t, filepath.Join(dir, ".omakase", "gates", "pedit.sh"), "MY EDIT\n")

	var stdout, stderr strings.Builder
	if code := RunPersonal([]string{"off"}, &stdout, &stderr); code != 0 {
		t.Fatalf("off exit = %d; stderr=%q", code, stderr.String())
	}

	// ---- output: cleared + removed (restored 1, deleted 2) ----
	eq(t, "stdout", stdout.String(),
		"omakase: personal harness cleared.\n"+
			"omakase: personal layer removed from this repo (restored 1 file(s), deleted 2).\n")
	wantWarn := "omakase: WARNING — '.omakase/gates/pedit.sh' was placed by your personal layer, has no lower-layer copy to restore, and differs from what omakase placed (a local edit?). Leaving it; delete it yourself if unwanted.\n"
	eq(t, "stderr", stderr.String(), wantWarn)

	// ---- working tree ----
	eq(t, "shared.sh restored to project copy", readFileT(t, filepath.Join(dir, ".omakase", "gates", "shared.sh")), "PROJECT\n")
	eq(t, "edited sole-personal kept", readFileT(t, filepath.Join(dir, ".omakase", "gates", "pedit.sh")), "MY EDIT\n")
	if _, err := os.Lstat(filepath.Join(dir, ".omakase", "gates", "ponly.sh")); !os.IsNotExist(err) {
		t.Error("sole-personal ponly.sh not deleted")
	}
	if _, err := os.Lstat(filepath.Join(dir, "CLAUDE.local.md")); !os.IsNotExist(err) {
		t.Error("rerouted CLAUDE.local.md not deleted")
	}
	eq(t, "project file untouched", readFileT(t, filepath.Join(dir, ".claude", "rules", "r.md")), "proj rule\n")

	// ---- placed.tsv rewritten to the project view (label + hash) ----
	col := map[string]state.PlacedRow{}
	for _, r := range state.ReadPlaced(filepath.Join(repo.OMK, "placed.tsv")) {
		col[r.Rel] = r
	}
	if r, ok := col[".omakase/gates/shared.sh"]; !ok || r.Src != proj || r.Hash != sha256hex([]byte("PROJECT\n")) {
		t.Errorf("shared.sh row = %+v, want {src=%s hash=sha256(PROJECT)}", r, proj)
	}
	for _, gone := range []string{"CLAUDE.local.md", ".omakase/gates/ponly.sh", ".omakase/gates/pedit.sh"} {
		if _, ok := col[gone]; ok {
			t.Errorf("placed.tsv still lists a personal path %q", gone)
		}
	}

	// ---- snapshot healed: NO personal files, canonical project bytes ----
	snap := filepath.Join(repo.OMK, "payload-snapshot")
	eq(t, "snapshot shared.sh canonical", readFileT(t, filepath.Join(snap, ".omakase", "gates", "shared.sh")), "PROJECT\n")
	for _, gone := range []string{"CLAUDE.local.md", ".omakase/gates/ponly.sh", ".omakase/gates/pedit.sh"} {
		if _, err := os.Lstat(filepath.Join(snap, gone)); !os.IsNotExist(err) {
			t.Errorf("snapshot still holds a personal file %q", gone)
		}
	}

	// ---- sources.tsv drops the personal row; layers/personal gone ----
	rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))
	if len(rows) != 1 || rows[0].Layer != "project" || rows[0].Source != proj {
		t.Errorf("sources.tsv = %+v, want one project row", rows)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "personal")); !os.IsNotExist(err) {
		t.Error("layers/personal not removed")
	}

	// ---- exclude block dropped the personal-only CLAUDE.local.md entry ----
	excl := readFileT(t, filepath.Join(repo.CommonDir, "info", "exclude"))
	if strings.Contains(excl, "CLAUDE.local.md") {
		t.Errorf("exclude still lists CLAUDE.local.md after unlayer:\n%s", excl)
	}
	if out := gitStdout(dir, "status", "--porcelain"); out != "" {
		t.Errorf("git status not clean: %q", out)
	}
}

// TestPersonalOffStaleSeam: the Task-4 seam — the global config was deleted
// WITHOUT `personal off`, so a later bare init swept the personal FILES but the
// faithful sources.tsv rewrite kept a stale personal ROW (and layers/personal
// lingers). `personal off` heals it gracefully: no personal-won rows survive, so
// restored/deleted are 0, the stale row + store are dropped, and it never crashes
// on the missing layer files.
func TestPersonalOffStaleSeam(t *testing.T) {
	dir, repo := initRepo(t)
	srcTestEnv(t)
	stubLefthook(t)
	base := useBasePayloadDir(t)
	writeFile(t, filepath.Join(base, ".omakase", "gates", "base.sh"), "base gate\n")
	t.Setenv("OMAKASE_PAYLOAD", base)

	psrc := newPersonalSource(t, map[string]string{"AGENTS.md": "personal doctrine\n"})
	setPersonalConfig(t, psrc)

	// (1) personal-only install.
	var o1, e1 strings.Builder
	if code := RunInit(nil, &o1, &e1); code != 0 {
		t.Fatalf("personal-only init exit = %d; stderr=%q", code, e1.String())
	}
	eq(t, "CLAUDE.local.md placed", readFileT(t, filepath.Join(dir, "CLAUDE.local.md")), "personal doctrine\n")

	// (2) the global config is deleted WITHOUT `personal off`.
	if err := os.Remove(personalConfigPath()); err != nil {
		t.Fatal(err)
	}

	// (3) a bare re-init: no personal config -> base-only path. It sweeps the
	// personal CLAUDE.local.md but faithfully rewrites the stale personal ROW.
	var o2, e2 strings.Builder
	if code := RunInit(nil, &o2, &e2); code != 0 {
		t.Fatalf("bare re-init exit = %d; stderr=%q", code, e2.String())
	}
	if _, err := os.Lstat(filepath.Join(dir, "CLAUDE.local.md")); !os.IsNotExist(err) {
		t.Fatal("bare re-init did not sweep the personal file (seam precondition unmet)")
	}
	if !hasStalePersonalRow(state.ReadSources(filepath.Join(repo.OMK, "sources.tsv"))) {
		t.Fatalf("stale personal row not present after the seam re-init (precondition unmet)")
	}

	// (4) `personal off` heals: graceful restored 0 / deleted 0, row + store gone.
	var stdout, stderr strings.Builder
	if code := RunPersonal([]string{"off"}, &stdout, &stderr); code != 0 {
		t.Fatalf("off exit = %d; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(),
		"omakase: personal harness cleared.\n"+
			"omakase: personal layer removed from this repo (restored 0 file(s), deleted 0).\n")
	eq(t, "stderr", stderr.String(), "")
	if rows := state.ReadSources(filepath.Join(repo.OMK, "sources.tsv")); len(rows) != 0 {
		t.Errorf("sources.tsv still has rows after healing the seam: %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(repo.OMK, "layers", "personal")); !os.IsNotExist(err) {
		t.Error("stale layers/personal not removed")
	}
	eq(t, "base gate intact", readFileT(t, filepath.Join(dir, ".omakase", "gates", "base.sh")), "base gate\n")
}

// hasStalePersonalRow reports whether rows carry a real personal row (not the
// off sentinel) — the seam's signature.
func hasStalePersonalRow(rows []state.SourceRow) bool {
	for _, r := range rows {
		if r.Layer == "personal" && r.Source != "off" {
			return true
		}
	}
	return false
}

// TestPersonalOffGC8Refusal: `off` in an INITIALIZED repo that predates layers
// (no $OMK/layers/) refuses with the GC8 bytes, exit 1 — AFTER the global clear
// (per the plan's ordering).
func TestPersonalOffGC8Refusal(t *testing.T) {
	_, repo := initRepo(t)
	cfg := personalCfgPath(t)
	writeFile(t, cfg, "you/harness\n")
	// Hand-build a v1-era $OMK: placed.tsv + source, NO layers/, NO sources.tsv.
	if err := os.MkdirAll(repo.OMK, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo.OMK, "placed.tsv"),
		".omakase/gates/example.sh\tgate\tpayload\t"+sha256hex([]byte(gateContent))+"\t1\n")
	writeFile(t, filepath.Join(repo.OMK, "source"), "you/harness\n")

	var stdout, stderr strings.Builder
	code := RunPersonal([]string{"off"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	eq(t, "stdout", stdout.String(), "omakase: personal harness cleared.\n")
	eq(t, "stderr", stderr.String(), "omakase: this repo predates layered state — run omakase init once first\n")
	// The global clear still happened (the refusal is only the per-repo half).
	if _, err := os.Stat(cfg); err == nil {
		t.Error("global config not cleared before the GC8 refusal")
	}
}

// ---------------------------------------------------------------- usage arm

func TestPersonalUsageArm(t *testing.T) {
	cases := [][]string{
		{"off", "extra"},         // off takes no argument
		{"a/b", "c/d"},           // two positionals
		{"--source"},             // --source with no value
		{"--source", "a/b", "x"}, // extra after the value
		{"--bogus"},              // unknown flag
		{"-h"},                   // no special help handling
	}
	for _, argv := range cases {
		t.Run(strings.Join(argv, "_"), func(t *testing.T) {
			var stdout, stderr strings.Builder
			code := RunPersonal(argv, &stdout, &stderr)
			if code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
			eq(t, "stdout", stdout.String(), "")
			eq(t, "stderr", stderr.String(), personalUsageText)
		})
	}
}
