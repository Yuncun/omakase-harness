package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yuncun/omakase-harness/internal/hook"
	"github.com/Yuncun/omakase-harness/internal/state"
)

// harnessRepo builds a temp git repo with a minimal installed, hooked, clean
// overlay: one enabled placed file whose ledger hash matches, the gate-hook
// dispatchers in the shared hooks dir, and a stable binary copy behind them
// (isolated XDG_CACHE_HOME).
func harnessRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	git("config", "commit.gpgsign", "false")

	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".omakase/VERSION", "0.18.1\n")
	for _, h := range []string{"pre-commit", "pre-push"} {
		if err := hook.Write(filepath.Join(dir, ".git", "hooks"), h); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stable := hook.StableBinPath()
	if err := os.MkdirAll(filepath.Dir(stable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	omk := filepath.Join(dir, ".git", "omakase")
	if err := os.MkdirAll(omk, 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []state.PlacedRow{{
		Rel: ".omakase/VERSION", Kind: "other", Src: "payload",
		Hash: state.HashOf(filepath.Join(dir, ".omakase/VERSION")), Enabled: "1",
	}}
	if err := state.WritePlaced(filepath.Join(omk, "placed.tsv"), rows); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStatuslineVerbRendersFromClaudeStdin(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	repo := harnessRepo(t)
	stdin := strings.NewReader(`{"model":{"display_name":"x"},"workspace":{"current_dir":` + jsonStr(repo) + `}}`)
	var out bytes.Buffer
	if code := runStatusline(stdin, &out); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	got := out.String()
	if !strings.Contains(got, "🥡 "+filepath.Base(repo)) || !strings.Contains(got, "⎇main") || !strings.Contains(got, "✓") {
		t.Fatalf("segment = %q", got)
	}
}

func TestStatuslineVerbTopLevelCwdKey(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	repo := harnessRepo(t)
	var out bytes.Buffer
	runStatusline(strings.NewReader(`{"cwd":`+jsonStr(repo)+`}`), &out)
	if !strings.Contains(out.String(), filepath.Base(repo)) {
		t.Fatalf("segment = %q", out.String())
	}
}

func TestStatuslineVerbDarkOutsideARepo(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if code := runStatusline(strings.NewReader(`{"cwd":`+jsonStr(dir)+`}`), &out); code != 0 || out.Len() != 0 {
		t.Fatalf("outside a repo: exit=%d out=%q, want 0 and empty", code, out.String())
	}
}

func TestStatuslineVerbGarbageStdinFallsBackToGetwd(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	repo := harnessRepo(t)
	t.Chdir(repo)
	var out bytes.Buffer
	if code := runStatusline(strings.NewReader("not json at all"), &out); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), filepath.Base(repo)) {
		t.Fatalf("segment = %q", out.String())
	}
}

// jsonStr quotes s as a JSON string (paths may contain backslashes on some
// platforms; keep the fixtures honest).
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
