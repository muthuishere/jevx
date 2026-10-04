---
title: "Shortcuts: is, pick, filter, rank"
description: Four shortcuts for the four shapes a decision takes in code — an if, a switch, a grep and a sort.
---

Each shortcut is `ask` with one question already shaped. The same saved questions, context, settings and exit codes apply.

```bash title="The four shapes"
jevx is "Is this urgent?" < email.txt                  # an if:     yes 0.92, exit 0 / 1 / 3 / 4
jevx pick "Which team?" web=frontend api=backend billing=money --in "charged twice"   # a switch
jevx filter "Is this an error?" < app.log              # a grep by meaning (-v: the lines that are not)
jevx rank "Is this about refunds?" --top 5 < results.txt   # a sort: every line with P(yes), best first
```

`is` and `pick` print `VERDICT P` on one line. `filter` and `rank` read one input per line from stdin unless you pass `--in`, `--lines` or `--states`.

## `is`: an if

```console
$ echo "Prod is down" | jevx is "Is this urgent?"; echo "exit $?"
yes 0.92
exit 0

$ echo "Weekly newsletter: our new office plants" | jevx is "Is this urgent?"; echo "exit $?"
no 0.05
exit 1
```

Use it straight in shell logic. Exit `1` is a real no; an unreachable endpoint is `4`, never `1`:

```bash title="triage.sh"
if jevx is "Is this urgent?" < "$msg"; then
  notify-oncall "$msg"
fi
```

A saved yes/no question works as the question: `jevx is urgent < email.txt`.

## `pick`: a switch

`pick` needs at least two `key=description` options. The verdict is one key.

```console
$ jevx pick "Which team?" web=frontend api=backend billing=money --in "charged twice"
billing 0.99
```

## `filter`: a grep by meaning

Prints the input lines whose verdict is yes. `-v` inverts it.

```text title="app.log"
INFO  09:12:01 request served in 12ms
ERROR 09:12:04 payment gateway timeout after 30s
INFO  09:12:05 cache warmed
```

```console
$ jevx filter "Is this line an error or failure?" < app.log
ERROR 09:12:04 payment gateway timeout after 30s
```

## `rank`: a sort

Prints every line with its P(yes), highest first. `--top N` keeps the first N.

```text title="results.txt"
How to change your avatar
Refund policy for annual plans
Cancelling a subscription and getting money back
Keyboard shortcuts
Pricing of the enterprise plan
```

```console
$ jevx rank "Is this about refunds?" --top 5 < results.txt
0.98  Refund policy for annual plans
0.93  Cancelling a subscription and getting money back
0.04  Pricing of the enterprise plan
0.02  Keyboard shortcuts
0.01  How to change your avatar
```

The second hit never says "refund", yet it ranks next: a sort by meaning, not by keyword. Every answer on this page is real output from hosted Jev (2026-10-04).

:::note
`filter` and `rank` refuse a saved choice or score question, since they need P(yes).
:::
