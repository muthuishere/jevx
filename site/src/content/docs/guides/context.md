---
title: Context
description: Background for the model comes from the ## Jev section of the AGENTS.md / CLAUDE.md you already keep, plus --context on a call. jevx reads it and never writes it.
---

The model sees only the question and the input. Background — whose decision it is, what counts as urgent here — comes from files you already keep. jevx reads them in this order and never edits them.

| layer | where | who edits it |
| --- | --- | --- |
| global | the `## Jev` section of `~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, `~/.agents/AGENTS.md`, `~/AGENTS.md`; all read, merged, repeated lines dropped (so synced copies count once) | you |
| folder | the `## Jev` section of the nearest `AGENTS.md` and/or `CLAUDE.md` from the working directory up; both merged, repeats dropped; `--cwd DIR` picks another start | you |
| call | `--context "TEXT"` or `--context @file`, repeatable | the agent, per call |
| question | `question add … --context` | you, once |

When `CLAUDE_CONFIG_DIR` is set, that directory's `CLAUDE.md` is read too.

## See what would be sent

```console
$ jevx context
global           (none)  add a "## Jev" section to one of: /Users/you/.claude/CLAUDE.md, /Users/you/.codex/AGENTS.md, /Users/you/.agents/AGENTS.md, /Users/you/AGENTS.md
folder           (none)  add a "## Jev" section to this repo's AGENTS.md or CLAUDE.md

order sent: the item first, then global, folder and --context on the call; a saved question's own context goes with that question
```

(Paths shortened to `/Users/you`.) With sections present, each line names the file it came from. `--no-context` skips the global and folder layers for one call; `--context` still applies.

## Writing a section

```md title="AGENTS.md" {1}
## Jev
Decisions are for the payments platform team. Anything touching refunds or double charges is urgent.
On-call pages only for customer-visible failures, not for retries that succeed.
```

- Any heading level works, with or without the space: `#jev`, `## Jev`, `### Jev notes`.
- The section ends at the next heading of the same or a higher level. It may be empty.
- The heading name and file names are configurable: `local_context_section`, `local_context_file`, `global_context_file` in the [config file](/jevx/reference/config/).

## Passing context on a call

```bash title="Terminal"
jevx ask urgent --context "The board meeting starts in 20 minutes." < msg.txt
jevx is "Is this a critical bug?" --cwd ~/repos/payments < report.txt    # that repo's ## Jev section
jevx is "Is this spam?" --no-context < msg.txt                          # skip the sections
```

The item being judged always goes **first** and the context after it (capped at 4000 characters), because a server keeps only its first N tokens: context in front could push the item out of the model's view. A JSON input keeps its shape and gets the context as its last field, `"context"`.

The same message, the saved `urgent` question ("Is this urgent for the person receiving it?") and three contexts, against hosted Jev (2026-10-04, two runs agreed within 0.01):

```console
$ M="Can you send me the Q3 revenue numbers before the board meeting?"
$ echo "$M" | jevx ask urgent
urgent           unsure     0.74
$ echo "$M" | jevx ask urgent --context "The board meeting starts in 20 minutes."
urgent           yes        0.93
$ echo "$M" | jevx ask urgent --context "The board meeting is in three months."
urgent           unsure     0.25
```

The facts that decide it are in the context, not in the message.

## Why the CLI never writes it

Context is the part of a judgement that is yours: what counts as urgent, whose decision it is. Keeping it in the file your agent already reads means one place to edit, a diff you review, and no hidden state the CLI could drift. If a verdict looks wrong, the skill tells the agent to name the file to edit rather than edit it.
