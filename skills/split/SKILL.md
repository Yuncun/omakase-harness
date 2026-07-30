---
name: split
description: Split an existing repo into a portable harness and the project it was written for — deciding which agent files (instructions, rules, skills, process docs, CI) travel and which stay. Use when asked to "split out the harness", "extract my agent setup from this repo", "pull the steering files into a harness", "which of these files belong in a harness", "make this repo's process reusable elsewhere", or when someone wants another repo's harness and has to decide what to take. Produces the payload/ tree the author skill publishes. Building a harness from scratch instead → author.
---

# /omakase:split — separate a harness from its project

One repo, two things tangled together: **the product**, and **the process that built it**.
This skill decides which files are which — and, harder, which process files still work once
moved.

Output is a `payload/` tree; [`author`](../author/SKILL.md) publishes it.

## Step 1 — reject the ownership rule

The rule that suggests itself is *"who has to be able to change this?"* — mine is harness,
the team's is project. It is a fact about **history**, not **structure**, and it predicts
portability not at all. A file only you have ever touched can name the repo in every
paragraph; a file the whole team ratifies can be pure generic prose.

Ownership decides what you are *allowed* to take. Step 2 decides what will *work*.

## Step 2 — the Two-Build Test

Two independent questions, both asked of every candidate.

| | Question | Fails when |
|---|---|---|
| **T1** | Does the product still build with this file deleted? | it is load-bearing product input — a config, a script the build shells out to, a generated type |
| **T2** | Does it still make sense installed on a bare repo? | it names files, packages, orgs, or CI that exist only here |

- **Fails T1** → project. Stays. Not negotiable.
- **Passes both** → harness. Travels unmodified.
- **Passes T1, fails T2** → the seam. Parameterize it or leave it, and say which.

Get a first cut mechanically, before reading anything:

```bash
skills/split/coupling-scan.sh -r '<this repo's proper nouns>' $(git ls-files '.claude/**' 'docs/**')
```

Sort by `repo` descending. The top is the seam, and it **clusters on one or two product
surfaces** — a design system, a data warehouse, one host app — rather than smearing evenly.
Find the cluster and you have found the parameterization boundary.

**Done when:** every candidate file sits in exactly one bucket, and the seam files each have
a written decision (parameterized, or left behind and why).

## Step 3 — do not trust a zero score

Two things the scan cannot see. Both have been measured to matter more than the scan itself.

**Semantic coupling scores zero.** "PRs are at most ten files." "We are trunk-based." "Run
the Storybook before committing." None name the repo; all three are false somewhere else.

**Line ratio flatters.** A skill can be 95% generic prose and still be *entirely about* a
thing the target lacks — a browser heap profile, a Figma file, a native build. Judge each
file by **what it is for**, not what fraction of lines mention the toolchain. In one measured
split the two disagreed by 15x — 4% by line, ~65% by concept — and the concept number was
right.

**Done when:** every file the scan called clean has also been judged by purpose.

## Step 4 — accept the forge ceiling

A forge runs workflows only from **committed** refs. An omakase overlay is deliberately kept
out of git. So **the CI half of a harness cannot be delivered as an overlay** — not a bug, a
construction.

That is typically 30–40% of a mature harness: workflows, bots, required checks, IaC. Ship it
if you want the record complete, but say in the README that it is inert.
**Authoring travels; shipping does not.**

Expect a corollary while you work: **gated artifacts are clean, ungated artifacts have
rotted.** Whatever CI validated still works; whatever it did not has accumulated dead links
and stale paths. Step 6 is where you find them.

## Step 5 — fidelity or curation, declared

Two different products. You cannot ship both in one payload.

**Default to verbatim when the harness is someone else's** — take everything, edit nothing,
dangling references and all. It stays auditable and re-extractable when the source moves,
and fidelity is the only claim you can honestly make about work you do not own.

**Curate when it is yours** — take only what earns its place, and list every edited file
individually in the README. Smaller and actually usable, at the cost of a diff against
upstream you now maintain.

**Done when:** the README names which one this is, and, if curated, which files were edited.

## Step 6 — three checks before publishing

None of these break a build, so no CI catches them, and all three are load-bearing for an
agent. Run them over `payload/`.

```bash
# 1. symlinks whose target does not resolve
find payload -type l ! -exec test -e {} \; -print

# 2. skills an agent silently cannot load: no frontmatter, or description over 1024 chars
find payload -name SKILL.md -exec awk 'NR==1 && !/^---/ {print FILENAME": no frontmatter"}
  /^description:/ {if (length($0) > 1034) print FILENAME": description "length($0)-13" chars (max 1024)"}' {} +

# 3. markdown refs that dangle INTO A DIRECTORY THE PAYLOAD ITSELF SHIPS
owned=" $(cd payload && ls -d */ .[!.]*/ 2>/dev/null | tr -d / | tr '\n' ' ')"
grep -rhoE '\]\((\.\./)*[^):#]+\)' payload --include='*.md' |
  sed -E 's/^\]\(//; s/\)$//; s|^(\.\./)+||' | sort -u |
  while read -r t; do
    case "$owned" in *" ${t%%/*} "*) [ -e "payload/$t" ] || echo "dangling: $t" ;; esac
  done
```

The `owned` filter in check 3 is the whole trick. A harness legitimately points at the
adopting project's own code, and flagging those buries the real hits — that one rule took a
real scan from 151 findings to 18, all genuine.

**Done when:** all three return clean, or each remaining hit is recorded in the README as a
known limit.

## Step 7 — prove it with a vessel, not an argument

Everything above is a hypothesis. The only honest test is a **test vessel**: a bare scaffold,
a fresh agent with no prior context, one prompt, a real task.

Run it **twice** — once with the harness, once with **no harness at all**, same prompt, same
model. Without that control arm you cannot separate "the harness worked" from "the model was
strong enough anyway," and that is the only claim anyone actually cares about. In the one
run this skill is built on, the control arm met every functional requirement in a quarter of
the time; what it lacked was any mechanism to be *challenged*.

Then ask the agent which files it actually opened. Expect a brutal ratio — a large harness
commonly has low-single-digit percent of its files doing the work. That answer is also the
best possible composition for a smaller second harness.

Record the verbatim prompt and every human intervention next to the result. Interventions are
the real cost of borrowing a harness and the first thing to vanish from the retelling.

**Done when:** both arms have run, you have verified the outputs yourself rather than trusting
either agent's self-report, and the prompt is written down.

## Step 8 — hand off

Lay the survivors out as `payload/` at verbatim paths and continue in
[`author`](../author/SKILL.md) from its Step 2. Gates are [`add-gate`](../add-gate/SKILL.md)'s
job — and expect the source repo's own gates not to travel, since they shell out to project
infrastructure the target does not have.
