// Package basepayload embeds the base harness payload — the support files
// every install layers under a custom harness (the .omakase machinery
// scripts and the base manifest) — so a binary installed alone (brew, a
// release tarball, go install) can run init without a payload/ directory
// shipped alongside it (issue #168). The payload/ tree at the repo root
// stays the source of truth; this is its build-time copy, extracted to the
// machine cache by overlay.ensureBasePayload when no on-disk copy exists.
//
// This file lives at the repo root because go:embed cannot reach outside
// its own directory tree ("../payload" is not embeddable from internal/).
package basepayload

import "embed"

// FS holds payload/** verbatim. The all: prefix is load-bearing: without
// it go:embed skips dot-paths, which would silently drop the entire
// payload/.omakase/ machinery tree — basepayload_test.go pins the contents.
//
//go:embed all:payload
var FS embed.FS
