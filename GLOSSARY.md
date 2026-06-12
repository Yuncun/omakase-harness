# Glossary of harness test concepts

Every check that shows up in a harness — a formatter, a Playwright suite, an
LLM judging screenshots, a worktree guard — gets **one of twelve recognizable
labels**. Use the label to name the tool in a harness manifest. The appendix
explains the five underlying axes: the theory of why the buckets carve where
they do, and the tiebreaker when a tool doesn't fit cleanly.

## The twelve categories

### Reads the code — nothing runs

| Category | What it is | Examples |
| --- | --- | --- |
| **Formatter** | Rewrites code style automatically; never argues, just fixes | prettier, black, gofmt |
| **Linter** | Scans source for bad patterns against a rulebook | eslint, ruff, stylelint, ast-grep |
| **Type checker** | Proves the types line up, without running anything | tsc / vue-tsc, mypy |
| **Security scan** | Looks for known vulnerabilities and leaked secrets | npm audit, gitleaks |

### Runs the code

| Category | What it is | Examples |
| --- | --- | --- |
| **Unit tests** | Thousands of fast checks of small pieces in isolation | vitest, pytest, jest |
| **E2E tests** | Drives the real assembled app like a user, with scripted assertions | playwright, cypress, espresso, maestro |
| **Smoke test** | Quick, shallow check that the system boots and responds at all | `make smoke` |
| **Visual diff** | Compares screenshots pixel-by-pixel against a saved golden copy | chromatic, percy, paparazzi |
| **AI judge** | An LLM looks at the output and rules pass/fail | `/visual-verify` |
| **Budget check** | A measured number must stay on the right side of a threshold | bundle size, Lighthouse score, coverage % |

### Checks the project, not the code

| Category | What it is | Examples |
| --- | --- | --- |
| **Consistency check** | Project-specific script asserting X matches Y | exports validator, doc-link checker, rot scan, OpenAPI diff |
| **Process guard** | Enforces how the *agent* works, not what it wrote | worktree discipline, ADR-required, Stop-hook done checklist |

A category names **what the tool is** — deliberately not **when it fires**.
Keep the trigger (pre-commit, pre-push, CI, at "done") as a separate column in
a harness manifest: the same linter can run scoped at pre-commit and complete
at pre-push, and that's one tool, not two.

## Worked example: the pixterm harness, labeled

| Gate | Category | Fires at |
| --- | --- | --- |
| prettier (staged files) | Formatter | pre-commit |
| stylelint | Linter | pre-commit (staged) · pre-push (all) |
| worktree + ADR guards | Process guard | pre-commit |
| `make typecheck` | Type checker | pre-push · CI |
| `make lint` | Linter | pre-push · CI |
| `make test-frontend` | Unit tests | pre-push · CI |
| the 4 validators (incl. rot scan) | Consistency check | pre-push |
| `/visual-verify` | AI judge | at "done" · verdict enforced pre-push |
| `make smoke` | Smoke test | CI |
| `make ui-regression` | E2E tests | CI (gate #4) |

---

## Appendix: the five axes

Why do the buckets carve where they do? Because testing terms sit on five
independent axes, and a given check has a value on *each* axis. A bucket is
just a memorable point in that space. When two buckets fight over a tool,
answer the five questions and the argument settles itself.

"UI-regression test" is not a third thing next to "unit test" — it is a
compound label built from values on several axes.

| Axis | The question it answers | Possible values |
| --- | --- | --- |
| **Level** | How much of the system runs during the test? | unit · integration · end-to-end |
| **Purpose** | Why does this test exist? | regression · smoke · acceptance · exploratory |
| **Interface** | What surface does the test drive? | UI · API · CLI · (none — code is read, not driven) |
| **Technique** | Does the code under test execute at all? | static · dynamic |
| **Oracle** | How is pass/fail decided? | assertion · snapshot diff · human · LLM-as-judge |

### Axis 1 — Level: how much of the system runs?

- **Unit test** — runs one small piece (a function, a class, a component) in
  isolation, with everything around it faked. Fast (milliseconds), so projects
  have thousands.
- **Integration test** — runs a few real pieces together (e.g. your code plus
  a real database) to check they cooperate.
- **End-to-end (e2e) test** — runs the whole assembled system the way a user
  would meet it: real server, real browser, real clicks. Slow (seconds each),
  so projects keep these few and precious.

### Axis 2 — Purpose: why does this test exist?

- **Regression test** — a *regression* is when something that used to work
  breaks after a change (the software "goes backward"). A regression test is
  any test you keep and re-run forever specifically to catch that. Most tests
  in a codebase are regression tests, whatever their level.
- **Smoke test** — a quick, shallow check that the system even starts and
  responds at all ("does smoke come out when we turn it on?"). Run first; if
  it fails, nothing else is worth running.
- **Acceptance test** — checks that a feature meets its stated requirement,
  phrased in user terms ("the user can reset their password").
- **Exploratory test** — no script. A tester (human or AI) freely pokes at
  the system looking for anything wrong. Finds bug *categories* that scripted
  tests can't, because nobody thought to write an assertion for them.

### Axis 3 — Interface: what surface does the test drive?

- **UI test** — drives the graphical interface: clicks buttons, fills forms,
  reads what is on screen.
- **API test** — sends requests to the program's HTTP (or similar) interface
  and checks responses.
- **CLI test** — invokes the program's command line and checks output and
  exit codes.
- **(none)** — some checks never drive the running program at all; see
  static, next axis.

### Axis 4 — Technique: does the code execute?

- **Static analysis** — the tool reads the source code without running it.
  Type checkers, linters, dead-code finders, formatters. Catches whole bug
  classes cheaply, but only the classes it was built to see.
- **Dynamic** — the code actually runs and its behavior is observed. Every
  "test" in the everyday sense is dynamic.

### Axis 5 — Oracle: how is pass/fail decided?

"Oracle" is the standard term for *the thing that decides whether the
observed behavior is correct*.

- **Assertion** — a hand-written expected value: `expect(total).toBe(42)`.
  Precise, but only checks what someone thought to write down.
- **Snapshot / golden-file diff** — the first run's output (a screenshot, a
  rendered file) is saved as the "golden" copy; later runs must match it
  byte-for-byte or pixel-for-pixel. Catches *any* change — including
  intentional ones, which then need the snapshot updated.
- **Human** — a person looks and judges. Catches the most, scales the least.
- **LLM-as-judge** — a language model looks at the output (often a
  screenshot plus a description of what *should* be there) and judges
  pass/fail. A cheap, scalable stand-in for the human oracle; best-effort
  rather than precise.

### The axes in action (pixterm)

| Tool | Level | Purpose | Interface | Technique | Oracle |
| --- | --- | --- | --- | --- | --- |
| `make typecheck` (vue-tsc) | — | regression | none | static | assertion (type rules) |
| `make lint` (eslint, ruff, …) | — | regression | none | static | assertion (lint rules) |
| `make test-frontend` (Vitest) | unit | regression | none (calls code directly) | dynamic | assertion |
| `make smoke` | end-to-end | smoke | API | dynamic | assertion |
| `make ui-regression` (Playwright) | end-to-end | regression | UI | dynamic | assertion |
| `/visual-verify` | end-to-end | exploratory | UI | dynamic | LLM-as-judge |

Read across a row and the compound names explain themselves:
"ui-regression" = end-to-end **level** + **UI** interface + **regression**
purpose, decided by assertions. `/visual-verify` sits at the *same* level and
interface but differs on purpose and oracle — which is exactly why both exist
and neither replaces the other.

## Where the test pyramid fits

The famous **test pyramid** is not a sixth axis — it is advice about *one*
axis. It takes the Level axis and adds a rule of thumb about counts: tests
get slower, costlier, and flakier as you go up the levels, so keep a huge
base of unit tests, a modest middle of integration tests, and only a few
end-to-end tests at the top.

```
        few   end-to-end   (seconds each — slow, fragile, realistic)
       some   integration
       many   unit         (milliseconds each — fast, precise, cheap)
```

In short: the five axes **classify** a check ("what is this?"); the pyramid
**prescribes proportions** along the Level axis ("how many should I have?").
It says nothing about the other four axes — static analysis isn't on the
pyramid at all, and it predates LLM-as-judge entirely.

## Sources

- [ISTQB Glossary](https://glossary.istqb.org/) — the closest thing to an
  official dictionary of testing terms.
- [Martin Fowler — The Practical Test Pyramid](https://martinfowler.com/articles/practical-test-pyramid.html)
  — the level axis (unit/integration/e2e) and why the counts should be
  pyramid-shaped, with examples.
- [Martin Fowler — Test Pyramid (short bliki)](https://martinfowler.com/bliki/TestPyramid.html)
  — the two-minute version.
- [Software Engineering at Google, ch. 11: Testing Overview](https://abseil.io/resources/swe-book/html/ch11.html)
  — replaces unit/integration/e2e with *small/medium/large* (classified by
  resource footprint); the strongest argument that the level boundaries are
  fuzzy.
- [Wikipedia — Test oracle](https://en.wikipedia.org/wiki/Test_oracle) — the
  oracle axis; LLM-as-judge is its newest member (the term comes from LLM
  evaluation literature, not classical testing).
