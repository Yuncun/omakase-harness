# Security Policy

## Reporting a vulnerability

Report suspected vulnerabilities through GitHub's private vulnerability reporting on this
repository (Security → Report a vulnerability). Do not open a public issue for a security
report.

## Surface

omakase modifies a repository's local git hook execution and can fetch a binary. The
security-relevant behavior:

- **Hook installation.** `init` installs git hooks through lefthook and refuses to run
  when another hook manager already owns the hooks (husky, pre-commit, a foreign
  `core.hooksPath`). It does not override an incumbent manager.
- **lefthook fetch.** When no lefthook binary is available, `init` downloads a pinned
  version and verifies it against a recorded checksum before use. A checksum mismatch
  aborts the install.
- **Cut-over.** `init --cut-over` stages deletions of tracked files so the harness copy
  can take over. It is guarded and refuses to run without `OMAKASE_CUTOVER_CONFIRM=1`.

Installed files are registered in `.git/info/exclude` and are never staged or committed.
