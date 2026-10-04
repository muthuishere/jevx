---
title: "Privacy: what leaves your machine"
description: Exactly what is sent to the endpoint, what is never sent, how keys are handled, and what the content-free ledger stores.
---

## What is sent, and what is not

| sent to the endpoint | never sent |
| --- | --- |
| the question text and options | your config file or profiles |
| the input (`--in`, stdin, each line of a batch) | anything from files you did not name |
| the merged `## Jev` context and `--context` text | for plugins: the full transcript (the state is a short description of the event) |
| for `judge`: the request, the proposal, the `--action` lines | |
| for plugins: a description of the event (the command, the fetched text, the prompt) | |

The endpoint is whatever the profile says. A hosted service sees the input and keeps whatever its terms say it keeps; a local model on `127.0.0.1` sees the same and keeps nothing you did not configure. jevx makes no other network calls: no telemetry, no update check.

:::note[Redaction before a hosted call]
When the profile's endpoint is **hosted** (anything that is not loopback, a private, link-local or CGNAT address, or a `*.local` / `*.internal` name), jevx scrubs the input, context and question text before sending:

- the live value of every environment variable whose name looks secret (`KEY`, `TOKEN`, `SECRET`, `PASSWORD`, `_PW`)
- well-known token formats (API keys, GitHub, Slack, AWS and Google keys, JWTs), private-key blocks and `user:pass@` URLs
- email addresses and phone numbers

Each becomes a `[REDACTED:kind]` marker, and jevx prints one stderr line naming what it removed by category (`jevx: redacted 1 password (in a URL), 1 email-shaped (user@host) ...`), never the values. The email rule also matches the `user@host` part of a connection URL, which is why that category says email-shaped. A local endpoint gets the text unchanged. The ledger records how many items were redacted, never what. Redaction is pattern-based, so the skill still tells agents never to put secrets into `--in` or `--proposal`.
:::

## Keys

A header like `Authorization: Bearer $JEV_API_KEY` is stored with the `$VAR` literal and expanded from the environment at request time. `profile list` prints the literal, not the value. If the variable is unset the call fails with its name (exit `4`) rather than sending an empty header.

## The answer cache

With `cache` true (the default), jevx keeps what the model said so the same call is not paid for twice. Each answer is one small file in `~/.cache/jevx/` (`cache_dir` in config.json), named by a SHA-256 hash of the endpoint URL, model, input and question. The input and the question are never written to disk, only the model's answer and when it was stored, and headers are not part of the hash, so rotating a key keeps the cache. A stored answer is used for `cache_ttl_days` (default 7); `--fresh` asks again and overwrites it; `jevx cache clear` deletes everything; `jevx cache disable` turns it off (`enable` turns it back on). Failed calls are never stored. Thresholds are applied after the lookup, so changing `--yes` or `--min` reuses the same answers. Calls answered from the cache send nothing and add nothing to the ledger.

## The ledger

With `ledger` true (the default), every call appends one line to `~/.local/share/jevx/calls.jsonl`: the profile, the caller (the calling agent's working directory, or `JEVX_CALLER`), the host, whether it is hosted, the model, number of questions, a 12-hex-character hash of the question set, latency, token usage, how many items were redacted and an error message if any. It never holds the input, the question text or a header. `JEVX_LEDGER=off` disables it for a shell.

```json title="~/.local/share/jevx/calls.jsonl (one line)"
{"host":"api.typesafe.ai","model":"jev-1.13.0","ms":673,"qhash":"b4838021a792","questions":1,"ts":"2026-09-27T16:30:24Z","usage":{"input_tokens":281,"output_tokens":20}}
```

`jevx stats` summarises it per day and host (`--days N`, default 7):

```console
$ jevx stats
day  host                                 calls errors  tokens in      out   p50 ms
2026-09-27  api.typesafe.ai                  51      0      16612     1524      577
```

That line is one machine, one day, while the earlier docs were being written: 51 calls to the hosted endpoint, no errors, median 577 ms. Your numbers will differ with the endpoint and the network.

## Hooks and plugins

- Installed disabled. `hook status` says what is in `settings.json` and whether anything ran.
- Shadow mode scores in a detached process and cannot delay the agent.
- `act` mode fails open: an endpoint error lets the tool call or the turn proceed.
- `uninstall --hooks` removes only jevx's entries and backs up the settings file first.
