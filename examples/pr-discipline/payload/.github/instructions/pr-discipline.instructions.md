---
applyTo: "**"
---
# PR discipline — prefer consolidated PRs

Default to **one coherent pull request per unit of work** rather than a tower of
small, dependent PRs.

When work breaks into parts, resist splitting it into a stack that only makes sense
read in order. A single self-contained PR a reviewer can understand and merge on its
own usually beats three that must land in sequence.

Why:
- A stack multiplies review cost — each PR re-loads the same context.
- Stacked PRs couple merge order: one stalled review blocks the chain.
- Splitting fragments the story; the reviewer sees pieces, not the whole change.

Still split when there is a real reason:
- A piece is independently useful and reviewable (e.g. a standalone refactor landed first).
- Risk isolation — keep a genuinely risky change separate so it can be reverted alone.
- The change is so large it truly impedes review, and the seams are clean.

The test: would the *reviewer* rather see one PR or several? Default to one; split only
when it makes their job easier, not just yours.
