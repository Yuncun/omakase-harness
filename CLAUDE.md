# omakase-harness — instructions for coding agents

## Comments are documentation, not conversation

Before writing a comment, ask: does it state a behavior fact or constraint the
code cannot show? If not, don't write it.

- Godoc style: an exported symbol gets one factual sentence starting with its
  name; a second sentence only for a real constraint.
- Never narrate history or process: no "ported from", "matches X byte-for-byte",
  no task/plan/review references, no citing what another file thinks.
- No narrative connectives ("note that", "on purpose", "exactly as"), no CAPS
  emphasis outside genuine warnings, no stacked parentheticals.
- Self-explanatory code gets no comment.
- Never comment your own change ("now handles X correctly") — that belongs in
  the commit message.

## Scope discipline

- Match the surrounding code's idiom; be conservative about new files,
  abstractions, and dependencies — bloat is a real cost.
- Comments you didn't need to touch: leave them alone.
