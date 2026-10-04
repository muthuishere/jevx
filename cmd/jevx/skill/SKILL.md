---
name: jevx
description: Fast yes/no, pick-one and rating judgements from a Jev-style decision model (any System One endpoint), instead of reasoning them out yourself. Use when you must classify, filter, triage, route, dedupe, rank or check many items (files, lines, tickets, messages, records), when a branch in your work depends on meaning ("is this urgent", "which team owns this"), when you are about to ask the user to pick between options, and before handing back a turn. Trigger: is this X, which of these, triage, classify, filter these, rank these, would the user accept this, what would the user pick, ask jev.
---

# jevx: judgements in milliseconds

jevx asks a decision model questions about an input and gets back a **verdict, never prose**. One call takes about
0.4 s, and batches run in parallel. Use it where you would otherwise spend your own tokens deciding something small:
whether each of 300 log lines is an error, which team owns a ticket, whether two records are the same customer.

It only judges. You still write the code, the reply or the fix.

## Shortcuts for the everyday cases

```bash
if jevx is "Is this urgent?" < email.txt; then ...; fi      # yes 0.95 · exit 0 yes, 1 no, 3 unsure, 4 error
jevx pick "Which team?" web=frontend api=backend billing=money --in "charged twice"   # billing 0.97
jevx filter "Is this an error or failure?" < app.log         # grep by meaning: prints the matching lines (-v: the rest)
jevx rank "Is this about refunds?" --top 5 < results.txt     # every line with P(yes), best first
```
Each is one question through `ask`, with the same saved questions (`jevx is urgent`), context, settings and exit codes.
Other checks are just a well-worded `is` or `pick`: `is "Does the source support this claim: …?"`,
`is "Are A and B the same customer?"`, `pick "Which function handles this?" refund=… invoice=…`.

## The full command: `jevx ask`

```bash
jevx ask --noul urgent="Is this urgent for the person receiving it?" < email.txt
# urgent           yes        0.95

jevx ask urgent,team < ticket.txt                    # named questions, saved once in config (see below)
jevx ask --lines app.log --noul err="Is this line an error?"        # batch: a table, one row per input
jevx ask --lines app.log --noul err="Is this line an error?" --raw  # batch: the full JSONL, for a script
jevx ask --states tickets.jsonl team,sev --parallel 8                # batch over JSON records
```

- **Questions**, as many as you like in one call:
  - A saved name: `urgent`, `urgent,team`.
  - Inline: `--noul NAME="Q"` (yes/no), `--choice NAME="Q|key=desc;key=desc"` (pick one) or `--score NAME="Q|low;mid;high"`
    (rating).
  - A file: `--questions set.json`.
- **Input**: stdin, `--in "text" | @file | '{json}'`, `--lines FILE` (one input per line) or `--states FILE.jsonl` (one JSON
  object or string per line).
- **Output for one input**: one line per question, `NAME  VERDICT  P`. The verdict is `yes` / `no` / `unsure` for a
  noul, the key or `unsure` for a choice, the level or `unsure` for a score. `--json` gives the same as JSON, and `--raw`
  gives the server's full reply.
- **Output for a batch**: a readable table, in input order: `VERDICT  P  INPUT` (one VERDICT/P pair per question when
  you ask several). `--raw` gives the full JSONL instead: `{"line":2,"input":"...","answers":{"err":{"verdict":"yes",
  "p":0.98,...}}}`. `jevx filter "Q" < file` prints only the yes inputs.
- **Exit codes** (one input): **0** all yes or decided, **1** a "no", **3** an unsure, **4** an error (network, auth,
  invalid reply). An error is never a "no". On 4, say the call failed; on 3, check it yourself or ask the user. A batch
  exits 0, or 4 if any input failed (that line carries `"error"`).

## Judge against the user's notes: `--memory`

When the question is "is this right per our docs?", point it at a memory (a folder of cited markdown the user
registered). jevx sends the best-matching sections **after** the item (BM25, no embeddings) and returns the verdict.

```bash
jevx memory list                                      # what exists; never create one unasked
jevx memory show docs "does v2 drop the 512-token limit?"   # what would be retrieved, with page#heading and lines
jevx ask --memory docs --memory-strict --lines claims.txt --noul true="Is this claim correct per the notes?"
```
- Measured on 143 held-out claims (the largest run): notes took a 0.4B local model from AUC 0.48 to 0.76. The weak
  spot is a changed number (50 of 72 caught); `--memory-strict` fails a yes/no claim whose number, `code` or name is
  missing from the notes: it misses 2 of 72 changed numbers (3%) but wrongly rejects 25 of 71 true claims (35%).
  A strict "no" flags the claim for a person (say so in your report); it never decides alone.
- It adds facts, not reasoning. The strict rule checks presence, not negation ("not MIT" contains MIT).
- `jevx memory check NAME` marks pages whose cited lines changed as stale; run it (and `index`) after docs change.

## Repeat calls are free: the answer cache

jevx keeps what the model said, so asking the same thing about the same input again costs nothing and returns in
milliseconds (about 7 ms against 350 ms). It is **on by default**. The key is a hash of the endpoint, model, input and
question; the input and question text are never written to disk, only the answer. Thresholds are applied after the
lookup, so `--yes` / `--min` changes reuse the stored answers. Failed calls are never stored.

```bash
jevx ask urgent --fresh < msg.txt     # ask the model again and refresh the stored answer (any ask, is, pick, filter, rank)
jevx cache                            # on/off, folder, how many answers, ttl
jevx cache disable                    # or: enable · ttl DAYS · dir PATH · clear   (same as: jevx config cache …)
```
Use `--fresh` when the answer must reflect a changed model or endpoint, or when a stored answer looks wrong. An answer
older than `cache_ttl_days` (default 7) is asked again on its own. The folder is `cache_dir` in
`~/.config/jevx/config.json` (default `~/.cache/jevx`). If a result is suspiciously identical between runs, that is
the cache; say so in your report.

## Context: what the model should know

The model sees only the question and the input, so give it the background. jevx **reads** context and never writes it.
The user keeps it by hand in a `## Jev` section of the agent files they already have:

| layer | where | who edits |
|---|---|---|
| global | `## Jev` in `~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, `~/.agents/AGENTS.md` and `~/AGENTS.md` (merged, repeats dropped) | the user |
| folder | `## Jev` in the nearest folder's `AGENTS.md` and/or `CLAUDE.md`, from the working directory up (merged, repeats dropped) | the user |
| call | `--context "TEXT"` or `--context @file` (repeatable) | you, per call |

```bash
jevx ask urgent --context "The board meeting starts in 20 minutes." < msg.txt
jevx is "Is this a critical bug?" --cwd ~/repos/payments < report.txt   # read that repo's ## Jev section
jevx is "Is this spam?" --no-context < msg.txt                          # skip the ## Jev sections for this call
jevx context                                                           # show what is sent, and from which file
```
The same message scored 0.84 with no context, 0.94 with "the meeting is in 20 minutes" and 0.64 (unsure) with "the
meeting is in three months". Pass the facts that decide it. Any heading level works (`#jev`, `## Jev`, `### Jev notes`). The section ends at the next heading of the same or a
higher level, and it may be empty. The
heading name and the files can be changed in config (`local_context_section`, `local_context_file`,
`global_context_file`). If the context looks wrong, tell the user which file to edit. Do not edit it yourself.

## Save questions once, reuse everywhere

```bash
jevx question add urgent --noul "Is this urgent for the person receiving it?" --yes 0.85
jevx question add team --choice "Which team should handle this?|web=frontend or UI;api=backend;billing=money"
jevx question add sev --score "How severe is this?|low;medium;high"
jevx question list
```
Add `--local` to save a question in this folder's `.jevx/questions.json` instead. It applies to every call made inside
the folder, wins over a global question of the same name, and can be committed with the repo. Check `jevx question
list` before writing a question inline: the user may already have one tuned.

## Everything is configurable, with defaults

`jevx defaults` shows every setting and where its value comes from. `jevx defaults set KEY VALUE [--profile P]`
changes one. The order of precedence is built-in < config < profile < the question's own thresholds < flags. Every command runs in the
current directory unless you pass `--cwd DIR`.

| key | default | meaning |
|---|---|---|
| `yes` / `no` | 0.8 / 0.2 | a noul P at or above `yes` is yes, at or below `no` is no, anything between is unsure |
| `min_confidence` | 0.6 | a choice or score below this is unsure |
| `parallel` | 8 | concurrent requests in a batch |
| `retries` / `timeout_s` / `chunk` | 3 / 60 / 32 | tries on 429/5xx, seconds per request, questions per request |
| `ledger` | true | log each call without content to `~/.local/share/jevx/calls.jsonl` (`jevx stats`); a hosted call is scrubbed of secrets, emails and phones first |
| `cache` / `cache_ttl_days` | true / 7 | reuse a stored answer for the same endpoint, model, input and question; `--fresh` on a call asks again; `jevx cache enable\|disable\|clear` |
| `accept_min` / `more_max` | 0.35 / 0.65 | legacy Stop hook thresholds (the `stop-judge` plugin has its own condition) |

Flags override for one call: `--yes`, `--no`, `--min`, `--parallel`, `--profile`, `--fresh`.

## Scenarios with real answers

`references/scenarios.md` (next to this file) has 18 real agent situations: the exact call, the exact answer hosted Jev
gave, and what to do next (log triage, ticket routing, ranking, picking a value, claim checks, "push or ask?", dangerous
commands, prompt injection, which file first, flaky vs real test, review comments, effort routing, secrets, build
verdicts, tool choice, judging a turn), plus how to turn an `unsure` into a clear answer by asking a better question.
Read it the first time you use jevx in a session, or whenever an answer comes back unsure.

## When to reach for it

| you are about to... | do this instead |
|---|---|
| read 300 lines to find the failures | `jevx filter "Is this a failure an on-call engineer would act on?" < log` (or `ask --lines log --noul fail="…"` for a table of every line's verdict and P) |
| sort tickets or messages into buckets | `ask --states t.jsonl --choice team="Which team?\|web=UI;api=backend;ops=infra"` |
| decide if two records are one entity | `printf 'A: %s\nB: %s' "$a" "$b" \| jevx ask --noul same="Are A and B the same customer?"` |
| check a claim against a source | `jevx ask --noul ok="Does the text say payment is due in October?" < invoice.txt` |
| rank search results | `jevx rank "Does this answer: how do I get a refund?" --top 5 < results.txt` |
| ask the user "A or B?" | `ask --in "<the facts>" --choice pick="Which would the user choose?\|a=<A>;b=<B>"`; ask only on exit 3 |
| hand back your turn | `jevx judge --request "<user's request>" --proposal "<your final message>"` |

**Write the question so it stands alone.** The model sees only the question and the input, not your conversation. Say
whose decision it is and what counts as yes: "Would a senior on-call engineer page someone for this line?" works better
than "bad?". When a verdict changed what you did, say so in your report: "jevx flagged 12 of 300 lines as errors".

## Judge your own turn before handing back

```bash
jevx judge --request "<the user's request, verbatim>" --proposal "<your final message>" --action "Bash: go test ./..."
```
If `accept` is below 0.35, the user would likely push back: verify and show evidence. If `wanted more` is above 0.65,
you stopped short: finish the missing part unless it truly needs the user.

## Plugins: judgements at agent events (Claude Code today)

A plugin is a hook behaviour in config: at an agent event, ask questions about what is happening and act on the answer.
The shipped ones are **off**: `stop-judge` (Stop), `bash-guard` (PreToolUse:Bash), `injection-screen` (PostToolUse:
WebFetch), `route` (UserPromptSubmit: simple request → suggest a haiku sub-agent). Turn one on and it runs in **shadow**
mode, logging what it would do; `mode NAME act` lets it act.

```bash
jevx plugin list                                  # what exists, enabled, mode
jevx plugin enable bash-guard                     # shadow: logs only   (enable all / disable all: every plugin at once)
jevx plugin mode bash-guard act                   # deny / ask for real
jevx plugin test bash-guard "git push --force"    # dry run on a sample command: answers + decision, nothing logged
jevx plugin log bash-guard 20                     # what it decided, with the answers
```

**Add one from a description** when the user wants a check at an event. Write or reuse the questions first, then:
```bash
jevx question add secret --noul "Does this file content contain a credential, token or private key?"
jevx plugin add secret-guard --desc "Refuse writing a secret into a file" \
  --on PreToolUse:Write\|Edit --ask secret --deny "secret >= 0.8" --local
```
Fields: `--on EVENT[:Tool regex]`, `--ask q1,q2` (saved or built-in: accepts, wanted_more, destroys, remote, irreversible,
injection, complexity, kind), then one of `--deny` / `--warn` / `--block` / `--context "COND"` where COND is
`name >= 0.8 && other < 0.5 || kind == ops`. `--say TEXT` is what the agent is told (`{{name}}` = that answer).
`--exec CMD` hands the event to any command instead (it gets `{event, payload, state, answers}` on stdin and prints the
agent's decision JSON), so a tool another project built can be plugged in without a jevx change. `--local` saves it
in this repo's `.jevx/plugins.json`. After adding: `jevx install --hooks`, then `plugin enable`.

Plugins follow Claude Code's hook protocol and `install --hooks` writes Claude Code's settings. In other agents the
skill works but plugins do not run yet.

## Setup (once)

```bash
curl -fsSL https://muthuishere.github.io/jevx/install.sh | sh      # this skill for Claude Code, Codex and ~/.agents
export TYPESAFE_API_KEY=...   # the user's key: hosted Jev works with no config at all
jevx profile add local http://127.0.0.1:21131/v1/systemone --model myjev   # only for a custom endpoint
```
If a call exits 4 asking for `TYPESAFE_API_KEY` (or another variable), tell the user which variable to set. Do not
guess one, and never ask them to paste a key into the chat.

## Redaction on hosted endpoints

Before a call to a hosted endpoint, jevx removes secrets, emails and phone numbers from the input and the question.
When it does, it prints one stderr line naming what it removed, by category and never the values, e.g. `jevx: redacted
1 password (in a URL) ...`. Read that line as an answer: the text holds that item, even if the verdict says `unsure`.
`email-shaped (user@host)` can also be the user and host of a connection URL, not only an email address. To have the model judge the raw text, use
a local endpoint (`--profile local`).

## Never
- Use it as a safety gate. Permissions, money, legal and irreversible calls stay with the user.
- Put secrets into `--in` or `--proposal`.
- Train another model on answers from a hosted third-party endpoint unless its terms allow it.
