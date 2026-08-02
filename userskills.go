// The agent-facing skill files, embedded next to the base payload for the
// same reason (#168): a binary installed alone (brew, a release tarball,
// go install) must carry everything it ships. Since the plugin fold (#211)
// these skills reach the hosts through `omakase init`, which installs them
// into the user-level skill folders both hosts read — there is no plugin
// channel anymore.
package basepayload

import "embed"

// SkillsFS holds skills/** verbatim. The all: prefix is load-bearing for
// the same reason as FS: without it go:embed skips dot-paths, silently
// dropping files — userskills_test.go pins the contents.
//
//go:embed all:skills
var SkillsFS embed.FS
