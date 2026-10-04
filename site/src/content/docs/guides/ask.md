---
title: Ask
description: One command for every question, inline or saved, one input or a parallel batch, with verdicts, probabilities and exit codes an agent can branch on.
---

Every answer on this page is real output from hosted Jev (2026-10-04). `jevx ask` is the full command. The [shortcuts](/jevx/guides/shortcuts/) (`is`, `pick`, `filter`, `rank`) are `ask` with one question already shaped.

```console
$ jevx ask -h
Usage of ask:
  -choice value
    	NAME="QUESTION|key=desc;key2=desc" (repeat)
  -context value
    	background for this call: TEXT or @file (repeat); added after the ## Jev sections
  -fresh
    	ask the model again and refresh the stored answers (the cache is otherwise used)
  -in string
    	one input: text, a JSON object, @file or - (default: stdin)
  -json
    	one input: print JSON instead of lines
  -lines string
    	batch: a text file (or -), one input per line
  -memory string
    	a jevx memory: append its best-matching notes after the item (jevx memory list)
  -memory-budget int
    	with --memory: characters of notes (default 2000)
  -memory-k int
    	with --memory: BM25 sections after named and pinned pages (default 3)
  -memory-strict
    	with --memory: a number, code span or name in the input that the retrieved notes lack makes a yes/no answer no
  -min float
    	override: choice / score confidence below is unsure; -1 = the value from jevx defaults (default -1)
  -no float
    	override: noul P at or below is no; -1 = the value from jevx defaults (default -1)
  -no-context
    	skip the ## Jev sections of the global and folder agent files
  -noul value
    	NAME="QUESTION" (repeat); or NAME="QUESTION|true means|false means"
  -parallel int
    	override: concurrent requests in a batch
  -profile string
    	endpoint profile
  -questions string
    	a question-set file: {NAME: {type, instructions, criteria}}
  -raw
    	the full result as it is: the server's response for one input, JSONL (line, input, every answer) for a batch
  -score value
    	NAME="QUESTION|low;mid;high" (repeat, lowest first)
  -states string
    	batch: a JSONL file (or -), one JSON object or string per line
  -yes float
    	override: noul P at or above is yes; -1 = the value from jevx defaults (default -1)
```

## Questions

- **Saved names:** `jevx ask urgent,team < ticket.txt` (see [Saved questions](/jevx/guides/questions/)).
- **Inline yes/no:** `--noul NAME="QUESTION"`. A second form spells out the poles: `NAME="QUESTION|true means|false means"`.
- **Inline pick-one:** `--choice NAME="QUESTION|key=desc;key2=desc"`. The verdict is one key.
- **Inline rating:** `--score NAME="QUESTION|low;mid;high"`, lowest first. The verdict is one level.
- **A file:** `--questions set.json`, a JSON object `{NAME: {type, instructions, criteria}}`.

Any number of questions go in one call and one request (up to `chunk`, default 32, per request).

```console
$ jevx ask --noul urgent="Is this urgent for the person receiving it?" \
    --choice team="Which team should handle this?|web=frontend or UI;api=backend;billing=money" \
    --in "Checkout is down, customers are being charged twice"
team             billing    0.72
urgent           yes        0.95
```

Output lines are sorted by question name, not by the order you gave them.

## Input

- stdin (the default), or `--in "text"`, `--in @file`, `--in '{"json": "object"}'`, `--in -`.
- `--lines FILE` (or `-`): a batch, one input per line of text.
- `--states FILE.jsonl` (or `-`): a batch, one JSON object or string per line. A JSON input keeps its shape; context is merged into its `"context"` field.

## Output for one input

One line per question: `NAME  VERDICT  P`. A yes/no verdict is `yes`, `no` or `unsure` (P at or above `yes` 0.8, at or below `no` 0.2, or between). A choice or score is the key or level, or `unsure` when its confidence is below `min_confidence` 0.6.

`--json` prints the same as one JSON object; `--raw` prints the server's reply untouched.

```bash title="Terminal"
jevx ask --json --noul spam="Is this message spam?" --in "Congratulations, you have won a prize, click here"
```

```json title="output"
{"answers":{"spam":{"answer":{"type":"noul","noul":0.94},"verdict":"yes"}},"model":"jev-1.13.0","profile":"jev"}
```

## Output for a batch

A batch prints a readable table, one row per input, in input order. With this `app.log`:

```text title="app.log"
INFO  09:12:01 request served in 12ms
ERROR 09:12:04 payment gateway timeout after 30s
INFO  09:12:05 cache warmed
```


```console
$ jevx ask --lines app.log --noul err="Is this line an error or failure?"
VERDICT  P     INPUT
no       0.02  INFO  09:12:01 request served in 12ms
yes      0.99  ERROR 09:12:04 payment gateway timeout after 30s
no       0.02  INFO  09:12:05 cache warmed
```

With several questions each gets a VERDICT and P column. `jevx filter "Q" < app.log` prints only the inputs that came back yes.

`--raw` prints the full result instead: JSONL in input order, one object per input with `line`, `input` and `answers` (each carrying `verdict` and, for yes/no, `p`), for a script.

```bash title="Terminal"
jevx ask --lines app.log --noul err="Is this line an error or failure?" --raw
```

```json title="output" {2}
{"line":1,"input":"INFO  09:12:01 request served in 12ms","answers":{"err":{"answer":{"type":"noul","noul":0.02},"p":0.02,"verdict":"no"}}}
{"line":2,"input":"ERROR 09:12:04 payment gateway timeout after 30s","answers":{"err":{"answer":{"type":"noul","noul":0.99},"p":0.99,"verdict":"yes"}}}
{"line":3,"input":"INFO  09:12:05 cache warmed","answers":{"err":{"answer":{"type":"noul","noul":0.02},"p":0.02,"verdict":"no"}}}
```

Batches run `parallel` requests at a time (default 8; `--parallel N` for one call). A batch exits `0`, or `4` if any input failed; that line carries an `"error"` field instead of answers.

## Overrides for one call

`--yes`, `--no`, `--min`, `--parallel`, `--profile`, `--context TEXT|@file` (repeatable), `--no-context`. Every command also takes `--cwd DIR` to run as if from that directory (it picks up that folder's `.jevx/` and `## Jev` section).

Precedence: built-in < config < profile < the question's own thresholds < flags. See [Settings](/jevx/reference/settings/).

## `judge`: before handing a turn back

`judge` asks four fixed questions about a proposed final message: would the user accept it, did the agent stop short, the likely reaction, and a 0-4 satisfaction. The questions come from a question pack (JSON); the built-in one is neutral, and a profile can point at its own with `--questions pack.json`.

```console
$ jevx judge -h
Usage of judge:
  -action value
    	one tool call as a short line (repeat)
  -context value
    	background for this call: TEXT or @file (repeat)
  -json
    	raw answers
  -no-context
    	skip the ## Jev sections of the global and folder agent files
  -profile string
    	endpoint profile
  -proposal string
    	the agent's final message
  -request string
    	the user's request
```

```console
$ jevx judge --request "Fix the failing test in parser_test.go" \
    --proposal "I found the off-by-one in parseRange and fixed it; go test ./... passes." \
    --action "Edit: core/parser.go" --action "Bash: go test ./..."
accept        0.60
wanted more   0.73
reaction      accept (0.93, confidence 0.91)
satisfaction  2.9 / 4  (jev)
```

The skill tells the agent: below `accept_min` 0.35 the user would likely push back, so verify and show evidence; above `more_max` 0.65 on "wanted more", finish the missing part. The call above scored 0.73 on "wanted more" for a one-line proposal with no diff shown, which is the intended nudge.

:::caution
`judge` is a heuristic from one model. There is no published accuracy number for it.
:::

## Writing a question that works

- Say whose decision it is and what counts as yes: "Would a senior on-call engineer page someone for this line?" beats "bad?".
- The model sees only the question, the input and the context; nothing from your conversation.
- Put the facts that decide it in [context](/jevx/guides/context/), not in the question.
- Look at a few verdicts before you loop over 5,000 lines. Tune thresholds per question if the default band is wrong for it.
