package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func notice(t *testing.T, repo, session string, args ...string) (stdout string, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	stdin := strings.NewReader(`{"cwd":` + jsonStr(repo) + `,"session_id":` + jsonStr(session) + `}`)
	code = runStopNotice(args, stdin, &out, &errOut)
	return out.String(), errOut.String(), code
}

func TestStopNoticeVerbSpeaksOncePerSession(t *testing.T) {
	repo := harnessRepo(t)

	first, stderr, code := notice(t, repo, "s1")
	if code != 0 || stderr != "" {
		t.Fatalf("first turn: exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(first, `"systemMessage"`) || !strings.Contains(first, "is active ✓") {
		t.Fatalf("first turn: %q", first)
	}
	if second, _, code := notice(t, repo, "s1"); code != 0 || second != "" {
		t.Fatalf("second turn with no change: %q, want silence", second)
	}
	if fresh, _, code := notice(t, repo, "s2"); code != 0 || !strings.Contains(fresh, "is active ✓") {
		t.Fatalf("new session: %q", fresh)
	}
}

func TestStopNoticeVerbCopilotWarnsWithoutBlocking(t *testing.T) {
	repo := harnessRepo(t)

	stdout, stderr, code := notice(t, repo, "s1", "--host", "copilot")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "is active ✓") {
		t.Fatalf("stderr = %q, want the notice", stderr)
	}

	stdout, stderr, code = notice(t, repo, "s1", "--host", "copilot")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("unchanged turn = (stdout=%q stderr=%q exit=%d), want silence and exit 0", stdout, stderr, code)
	}
}

func TestStopNoticeVerbHostSelection(t *testing.T) {
	t.Run("environment fallback", func(t *testing.T) {
		t.Setenv("OMAKASE_HOOK_HOST", "copilot")
		stdout, stderr, code := notice(t, harnessRepo(t), "s1")
		if code != 2 || stdout != "" || !strings.Contains(stderr, "is active ✓") {
			t.Fatalf("environment host = (stdout=%q stderr=%q exit=%d)", stdout, stderr, code)
		}
	})

	t.Run("flag overrides environment", func(t *testing.T) {
		t.Setenv("OMAKASE_HOOK_HOST", "copilot")
		stdout, stderr, code := notice(t, harnessRepo(t), "s1", "--host", "claude")
		if code != 0 || stderr != "" || !strings.Contains(stdout, `"systemMessage"`) {
			t.Fatalf("explicit host = (stdout=%q stderr=%q exit=%d)", stdout, stderr, code)
		}
	})

	t.Run("invalid host is loud", func(t *testing.T) {
		stdout, stderr, code := notice(t, t.TempDir(), "s1", "--host", "other")
		if code != 2 || stdout != "" || !strings.Contains(stderr, `unknown host "other"`) {
			t.Fatalf("invalid host = (stdout=%q stderr=%q exit=%d)", stdout, stderr, code)
		}
	})
}

func TestStopNoticeVerbAnnouncesARun(t *testing.T) {
	repo := harnessRepo(t)
	notice(t, repo, "s1") // settle the marker

	ledger := filepath.Join(repo, ".git", "omakase", "ledger.tsv")
	if err := os.WriteFile(ledger, []byte("2000000000\tsmoke\tpass\tabc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _, code := notice(t, repo, "s1")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(got, "Last run: 1/1 checks at ") {
		t.Fatalf("run announce: %q", got)
	}
	if again, _, code := notice(t, repo, "s1"); code != 0 || again != "" {
		t.Fatalf("same run announced twice: %q", again)
	}
}

func TestStopNoticeVerbAnnouncesAStateChange(t *testing.T) {
	repo := harnessRepo(t)
	notice(t, repo, "s1") // settle the marker

	if err := os.Remove(filepath.Join(repo, ".git", "hooks", "pre-commit")); err != nil {
		t.Fatal(err)
	}
	got, _, code := notice(t, repo, "s1")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(got, "is not active") {
		t.Fatalf("disarm announce: %q", got)
	}
	if again, _, code := notice(t, repo, "s1"); code != 0 || again != "" {
		t.Fatalf("unchanged degraded state announced twice: %q", again)
	}
}

func TestStopNoticeVerbSilentOutsideAHarnessRepo(t *testing.T) {
	dir := t.TempDir()
	if got, stderr, code := notice(t, dir, "s1"); code != 0 || got != "" || stderr != "" {
		t.Fatalf("outside a repo: %q, want silence", got)
	}
}
