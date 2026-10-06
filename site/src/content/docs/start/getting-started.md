---
title: Getting started
description: Install jevx, set your key, and ask your first question. About two minutes.
---

## 1. Install

```bash title="npm (macOS, Linux, Windows)"
npm i -g @muthuishere/jevx
```

```bash title="macOS / Linux"
curl -fsSL https://muthuishere.github.io/jevx/install.sh | sh
```

```bat title="Windows"
curl -fsSLo install.cmd https://muthuishere.github.io/jevx/install.cmd && install.cmd
```

Each one puts the `jevx` binary on your PATH, copies the agent skill into `~/.claude/skills`, `~/.agents/skills` and `~/.codex/skills` (if that folder exists), and writes no Claude Code hook: nothing runs on agent events until you enable a plugin, which adds its own entry. `JEVX_NO_HOOK=1` skips the hook step entirely. Details in [Install](/jevx/start/install/).

## 2. Set your key

jevx talks to hosted Jev out of the box. There is nothing to configure: create a key in the
[TypeSafe console](https://console.typesafe.ai/keys), put it in the environment and you are done.

```bash title="~/.zshrc or ~/.bashrc"
export TYPESAFE_API_KEY=...        # from console.typesafe.ai/keys
```

jevx reads the variable when a request is sent and never writes it anywhere. Without it, every call stops with a clear
message and exit code `4` (never a fake "no"):

```console
$ echo "Prod is down" | jevx is "Is this urgent?"
jevx: set your Jev API key: export TYPESAFE_API_KEY=... (create one at https://console.typesafe.ai/keys). Or use another endpoint: jevx profile add NAME URL --model M --header 'Authorization: Bearer $YOUR_VAR' && jevx profile use NAME
```

### Optional: your own endpoint

Only if you run a different System One endpoint (a self-hosted model, a personal model on your machine, another
provider), add a **profile**: a URL, a model and headers. `$VAR` in any of them is expanded at request time, so keys
stay out of the config file.

```bash title="Terminal"
jevx profile add openjevx http://127.0.0.1:21160/v1/systemone --model openjevx   # a local openjevx (its default port since v0.5.11): no key needed
jevx profile add acme https://jev.acme.dev/v1/systemone --model jev-latest --header "Authorization: Bearer $ACME_KEY"
```

```console
$ jevx profile list
* jev        https://api.typesafe.ai/v1/systemone  model=jev-latest  Authorization: Bearer $TYPESAFE_API_KEY  (built in)
  openjevx   http://127.0.0.1:21160/v1/systemone  model=openjevx
(* = default; change with: jevx profile use NAME)
```

The built-in `jev` stays the default. Use a custom one for a single call with `--profile openjevx`, or make it the default
with `jevx profile use openjevx`.

## 3. Ask something

```console
$ echo "Prod is down" | jevx is "Is this urgent?"; echo "exit $?"
yes 0.92
exit 0
```

The word is the verdict, the number is P(yes). Exit `0` means yes, `1` no, `3` unsure, `4` an error. See [Ask](/jevx/guides/ask/) for every form.

### Batches and repeat calls

A batch prints a readable table, one row per input, in order. Asking again is free: jevx keeps the model's answer, so a repeat returns in milliseconds and costs nothing.

```console
$ printf '%s\n' '2026-09-27 10:03:12 INFO  request served /checkout 200 in 84ms' \
    '2026-09-27 10:03:40 ERROR payment gateway timeout after 30s (order 4021)' > app.log
$ jevx ask --lines app.log --noul err="Is this an error?"
VERDICT  P     INPUT
no       0.02  2026-09-27 10:03:12 INFO  request served /checkout 200 in 84ms
yes      0.99  2026-09-27 10:03:40 ERROR payment gateway timeout after 30s (order 4021)

$ jevx ask --lines app.log --noul err="Is this an error?" --fresh    # ask the model again
$ jevx cache disable                                                  # or: enable · ttl DAYS · dir PATH · clear
```

`--raw` prints the full JSONL for a script. See [Ask](/jevx/guides/ask/) and [Privacy](/jevx/reference/privacy/#the-answer-cache).

## 4. Let the agent use it

Nothing more to do. The skill is installed, and Claude Code or Codex loads it when a task matches its description (classify, triage, filter, rank, "is this X", "which of these"). [Using it from an agent](/jevx/guides/agents/) shows what the agent is told.

## Where to go next

- [Shortcuts](/jevx/guides/shortcuts/): `is`, `pick`, `filter`, `rank`.
- [Saved questions](/jevx/guides/questions/): write a question once, reuse it by name.
- [Context](/jevx/guides/context/): tell the model whose decision it is.
- [Memory](/jevx/guides/memory/): judge claims against your own docs, with `--memory-strict` for changed numbers.
- [The answer cache](/jevx/reference/privacy/#the-answer-cache): repeat calls are free; `--fresh`, `jevx cache`.
- [CLI reference](/jevx/reference/cli/): every command on one page.
