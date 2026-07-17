package gate

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// appendRow appends one ledger row `epoch \t name \t verdict \t sha` to
// omk/ledger.tsv, byte-identical to the deleted omakase-gate.sh's append: the
// whole row is built and written in a single O_APPEND write so concurrent
// appends do not tear, and a tab or newline in the name or sha is folded to a
// space so a hostile value cannot shift columns. omk is created if missing.
func appendRow(omk, name, verdict, sha string) error {
	if err := os.MkdirAll(omk, 0o755); err != nil {
		return err
	}
	row := now() + "\t" + sanitizeField(name) + "\t" + verdict + "\t" + sanitizeField(sha) + "\n"
	f, err := os.OpenFile(filepath.Join(omk, "ledger.tsv"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(row); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// sanitizeField folds a tab or newline to a space so a hostile gate name or a
// mangled sha keeps the TSV columns intact.
func sanitizeField(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.ReplaceAll(s, "\n", " ")
}

// now is the ledger epoch: OMAKASE_NOW when set (the test hook that pins the
// clock), else the current unix time.
func now() string {
	if v := os.Getenv("OMAKASE_NOW"); v != "" {
		return v
	}
	return strconv.FormatInt(time.Now().Unix(), 10)
}

// disabledSet reads the menu bypass file (omk/disabled-gates, one gate name per
// line, written by `omakase status`) into a set. A missing file is an empty
// set. The match is exact-line, like the sh `grep -Fxq`.
func disabledSet(omk string) map[string]bool {
	set := map[string]bool{}
	f, err := os.Open(filepath.Join(omk, "disabled-gates"))
	if err != nil {
		return set
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			set[line] = true
		}
	}
	return set
}

// headSHA is `git rev-parse HEAD` from root, "" on any failure (an unborn
// HEAD, or no git). It tags every ledger row with the commit being
// committed/pushed and is the cache key.
func headSHA(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// resolveBase resolves the base ref a glob range diffs against, trying
// origin/HEAD's target, then origin/master, then origin/main — the first that
// names a real commit. ok is false when none resolves (a repo with no remote),
// which the caller treats as "run unscoped".
func resolveBase(root string) (string, bool) {
	var cands []string
	if out, err := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "origin/HEAD").Output(); err == nil {
		if s := strings.TrimRight(string(out), "\n"); s != "" {
			cands = append(cands, s)
		}
	}
	cands = append(cands, "origin/master", "origin/main")
	for _, c := range cands {
		if exec.Command("git", "-C", root, "rev-parse", "--verify", "--quiet", c+"^{commit}").Run() == nil {
			return c, true
		}
	}
	return "", false
}

// changedMatches reports whether any file changed in base...HEAD matches one of
// the glob patterns. The range is merge-base-bounded (three-dot); a two-dot
// fallback covers unrelated histories, where three-dot is fatal and a range
// error must never masquerade as "no changes". A raw git error is never
// surfaced.
func changedMatches(root, base string, patterns []string) bool {
	changed, err := gitDiffNames(root, base+"...HEAD")
	if err != nil {
		changed, _ = gitDiffNames(root, base+"..HEAD")
	}
	for _, file := range changed {
		if file == "" {
			continue
		}
		for _, p := range patterns {
			if matchGlob(p, file) {
				return true
			}
		}
	}
	return false
}

// gitDiffNames runs `git diff --name-only -z <range>` from root and returns the
// changed paths. `-z` is load-bearing: without it git octal-quotes a non-ASCII
// path (core.quotePath defaults on), so a raw UTF-8 filename never reaches the
// glob matcher and its gate silently drops out of scope (fail-open). NUL
// termination also keeps a path containing a newline one record.
func gitDiffNames(root, rng string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "diff", "--name-only", "-z", rng).Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(out), "\x00"), "\x00"), nil
}

// hasFreshPass reports whether omk/ledger.tsv already carries a `pass` row for
// this gate at this exact HEAD sha — the cache hit. Mirrors the sh awk
// `$2==n && $4==s && $3=="pass"`. A fail row never satisfies the cache.
func hasFreshPass(omk, name, sha string) bool {
	f, err := os.Open(filepath.Join(omk, "ledger.tsv"))
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) >= 4 && fields[1] == name && fields[3] == sha && fields[2] == "pass" {
			return true
		}
	}
	return false
}

// matchGlob reports whether name matches a single sh `case` pattern, where a
// `*` spans directory separators (the sh dialect, unlike filepath.Match). The
// pattern is anchored to the whole path, matching sh `case "$file" in $pat)`.
func matchGlob(pattern, name string) bool {
	re := globToRegexp(pattern)
	return re.MatchString(name)
}

// globCache memoizes compiled glob patterns.
var globCache = map[string]*regexp.Regexp{}

// globToRegexp translates one sh-case glob into an anchored regexp: `*` -> `.*`
// (spans `/`), `?` -> `.`, a `[...]` bracket expression carried across (with
// `[!` -> `[^`), and every other RUNE escaped literally. Iterating runes (not
// bytes) keeps a multibyte UTF-8 literal intact: a byte-wise `string(byte)`
// re-encodes a lead byte as its own code point, so `café*` would compile to a
// regexp that no longer matches the real `café.go` filename bytes (fail-open).
func globToRegexp(pattern string) *regexp.Regexp {
	if re, ok := globCache[pattern]; ok {
		return re
	}
	rs := []rune(pattern)
	var b strings.Builder
	b.WriteString(`^`)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch c {
		case '*':
			b.WriteString(`.*`)
		case '?':
			b.WriteString(`.`)
		case '[':
			// Carry a bracket expression across verbatim, translating a leading
			// '!' negation to '^'. An unterminated '[' is a literal '['.
			j := i + 1
			if j < len(rs) && (rs[j] == '!' || rs[j] == '^') {
				j++
			}
			if j < len(rs) && rs[j] == ']' { // a ']' right after the (negated) open is a literal
				j++
			}
			for j < len(rs) && rs[j] != ']' {
				j++
			}
			if j >= len(rs) {
				b.WriteString(regexp.QuoteMeta("["))
				continue
			}
			cls := string(rs[i : j+1])
			cls = strings.Replace(cls, "[!", "[^", 1)
			b.WriteString(cls)
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(`$`)
	re := regexp.MustCompile(b.String())
	globCache[pattern] = re
	return re
}

// short8 is the sh `${sha:0:8}` used in the "(cached)" notice.
func short8(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// exitCode extracts a child's exit code from an *exec.ExitError. A normal exit
// returns its code; a signal-killed child (ExitCode() == -1) returns
// 128+signal — the sh convention the deleted omakase-gate.sh passed through
// (`sh -c "$STEP"; exit $?`), so an OOM SIGKILL or a timeout SIGTERM surfaces
// 137/143, not a flattened 1. Any other run error defaults to 1.
func exitCode(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		if code := ee.ExitCode(); code > 0 {
			return code
		}
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
	}
	return 1
}
