#!/usr/bin/env bash
# Write a deferred-gate record that deferred-check.sh later reads at a git hook.
# A JOB calls this - a review skill, a render runner, a human sign-off script - so
# it never hand-formats JSON. The hook never runs the check; it reads this record.
#
# Usage:
#   omakase-record.sh --check <name> --verdict pass|fail [--reason "..."]
#                     [--original-verdict pass|fail]
#
#   --reason            audit note. REQUIRED when waiving (a pass over a judged fail).
#   --original-verdict  the job's raw verdict before any waiver.
#
# The record is keyed to the current commit sha and written atomically into the
# per-clone git dir (git rev-parse --git-path omakase), so it is never committed
# and never ships to another clone or to CI where its sha key would be meaningless.
set -euo pipefail

CHECK="" VERDICT="" REASON="" ORIGINAL=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --check)            CHECK="${2:-}"; shift 2;;
    --verdict)          VERDICT="${2:-}"; shift 2;;
    --reason)           REASON="${2:-}"; shift 2;;
    --original-verdict) ORIGINAL="${2:-}"; shift 2;;
    *) echo "omakase-record: unknown arg: $1" >&2; exit 2;;
  esac
done

[[ -n "$CHECK" ]] || { echo "omakase-record: --check required" >&2; exit 2; }
case "$VERDICT" in
  pass|fail) ;;
  *) echo "omakase-record: --verdict must be pass|fail" >&2; exit 2;;
esac
# Waiving a FAIL (recording pass while the job said fail) must carry a reason -
# the reason is the audit trail the gate surfaces at push time.
if [[ "$VERDICT" == "pass" && "$ORIGINAL" == "fail" && -z "$REASON" ]]; then
  echo "omakase-record: waiving a FAIL requires --reason" >&2; exit 2
fi

# Single-line JSON-safe: strip control chars, escape backslash then quote.
json_escape() { printf '%s' "$1" | tr '\n\r\t' '   ' | sed 's/\\/\\\\/g; s/"/\\"/g'; }
json_str_or_null() { [[ -n "$1" ]] && printf '"%s"' "$(json_escape "$1")" || printf 'null'; }

KEY="$(git rev-parse HEAD)"
DIR="$(git rev-parse --git-path omakase)/deferred"
mkdir -p "$DIR"
OUT="$DIR/$CHECK.json"
TMP="$(mktemp "$DIR/.${CHECK}.XXXXXX")"

printf '{"check":"%s","key":"%s","verdict":"%s","reason":%s,"original_verdict":%s}\n' \
  "$CHECK" "$KEY" "$VERDICT" \
  "$(json_str_or_null "$REASON")" \
  "$(json_str_or_null "$ORIGINAL")" \
  > "$TMP"
mv -f "$TMP" "$OUT"

echo "omakase-record: $CHECK = $VERDICT (key ${KEY:0:8}) -> $OUT"
