<!-- Absolutely no AI slop. AI is allowed to change install steps and fix text or styling or license information, links, port in new graphics, and stuff like that but it should not write new explanations that are meant for humans to read. Consider this page to be a letter from Eric, the creator, to his audience, and in that sense do not impersonate eric. Fine if eric gives agent the content to write and agent is putty it into the page.  -->

<h1 align="center">omakase</h1>

<p align="center">
  <a href="https://github.com/Yuncun/omakase-harness/actions/workflows/tests.yml"><img src="https://github.com/Yuncun/omakase-harness/actions/workflows/tests.yml/badge.svg" alt="tests"></a>
  <a href="https://github.com/Yuncun/omakase-harness/releases"><img src="https://img.shields.io/github/v/release/Yuncun/omakase-harness" alt="release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/Yuncun/omakase-harness" alt="license"></a>
</p>

```
your harness repo                    any project you work in
┌──────────────────────┐             ┌──────────────────────────┐
│ CLAUDE.md, rules,    │  omakase    │ files appear on disk,    │
│ skills               │  ────────►  │ agents & hooks use them  │
│ gates: lint, test,   │   init      │                          │
│ secrets              │             │ git never sees them      │
└──────────────────────┘             └──────────────────────────┘
```



<!-- demo.gif slot — VHS tape to live at docs/tapes/demo.tape.
     Storyboard: init → status page, disable one gate → a commit trips a gate
     → git status: clean. The transcript below is real v0.23 output (trimmed) and the
     tape replaces it. -->


## Install

macOS / Linux:

```
brew install yuncun/tap/omakase
```

Windows 

```
tbd
```

Or grab a binary from [releases](https://github.com/Yuncun/omakase-harness/releases)
(checksums published), or build from source:

```
go install github.com/Yuncun/omakase-harness/cmd/omakase@latest
```

## Use

Three commands — see, get, undo:

```
omakase status            what's steering agents in this repo — committed, placed, gates,
                          hooks live or not. Works in any repo, harness or none
omakase init you/harness  install that harness here: files in, gates wired, nothing committed.
                          Bare `omakase init` refreshes from the remembered source
omakase remove            delete everything omakase placed, exactly
```


## License

MIT. See [LICENSE](LICENSE).
