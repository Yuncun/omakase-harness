// The `omakase stop-notice` verb: the end-of-turn status line for the
// developer driving a Claude Code session, emitted as a Stop-hook
// systemMessage. Deterministic — no LLM, no API tokens — and it never
// blocks the turn: every failure path exits 0 silently.
//
// It speaks only when something changed since the last turn: a new session,
// a hook run that finished this turn (the ledger's newest run epoch
// advanced), or a change in the probed state (armed/present/hashes). A
// per-worktree marker file under $OMK remembers what was last announced.
package main

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Yuncun/omakase-harness/internal/probe"
	"github.com/Yuncun/omakase-harness/internal/render"
)

func runStopNotice(stdin io.Reader, stdout io.Writer) int {
	scrubGitEnv()
	host := readHostJSON(stdin)
	cwd := cwdFromHostJSON(host)
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return 0
		}
	}
	session := ""
	if host != nil {
		if s, ok := host["session_id"].(string); ok {
			session = s
		}
	}

	st, err := probe.Collect(cwd)
	if err != nil || !st.Installed {
		return 0
	}

	// The announce signature: any change in the proofs or their counts
	// re-announces, so a state that silently degraded mid-session speaks.
	sig := fmt.Sprintf("a%d f%d h%d m%d d%d", st.Armed, st.FilesPresent, st.HashesMatch, st.Missing, st.Drifted)
	var epoch int64
	if st.LastRun != nil {
		epoch = st.LastRun.Epoch
	}

	marker := filepath.Join(st.OMK, fmt.Sprintf("notice-%d.marker", crc32.ChecksumIEEE([]byte(st.Root))))
	prevSession, prevEpoch, prevSig, hadMarker := readMarker(marker)
	ranThisTurn := epoch > prevEpoch

	speak := !hadMarker || session != prevSession || ranThisTurn || sig != prevSig
	// Best-effort marker write; a read-only OMK just means we announce again.
	os.MkdirAll(st.OMK, 0o755)
	os.WriteFile(marker, []byte(session+"\t"+strconv.FormatInt(epoch, 10)+"\t"+sig+"\n"), 0o644)
	if !speak {
		return 0
	}

	msg := render.StopNotice(st, ranThisTurn)
	if msg == "" {
		return 0
	}
	out, err := json.Marshal(map[string]string{"systemMessage": msg})
	if err != nil {
		return 0
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

// readMarker reads the announce marker: session \t epoch \t signature.
func readMarker(path string) (session string, epoch int64, sig string, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", 0, "", false
	}
	line := strings.TrimRight(string(b), "\n")
	fields := strings.SplitN(line, "\t", 3)
	if len(fields) > 0 {
		session = fields[0]
	}
	if len(fields) > 1 {
		epoch, _ = strconv.ParseInt(fields[1], 10, 64)
	}
	if len(fields) > 2 {
		sig = fields[2]
	}
	return session, epoch, sig, true
}
