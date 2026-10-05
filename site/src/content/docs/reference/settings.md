---
title: Settings
description: Every knob has a default, is visible in one command, and follows one precedence — built-in < config < profile < question < flag.
---

## See everything

```console
$ jevx defaults
yes              0.8      default
no               0.2      default
min_confidence   0.6      default
parallel         8        default
retries          3        default
timeout_s        60       default
chunk            32       default
ledger           true     default
accept_min       0.35     default
more_max         0.65     default
cache            true     default
cache_ttl_days   7        default
```

The third column says where the value comes from: `default`, `config`, or `profile NAME` when you pass `--profile`. This is the whole list; there are no hidden knobs. A question's own thresholds and a call's flags apply on top and are not shown here.

| key | default | meaning |
| --- | --- | --- |
| `yes` / `no` | 0.8 / 0.2 | a yes/no P at or above `yes` is yes, at or below `no` is no, in between is unsure |
| `min_confidence` | 0.6 | a choice or score with lower confidence is unsure |
| `parallel` | 8 | concurrent requests in a batch |
| `retries` | 3 | tries on HTTP 429, 5xx and network errors, with backoff |
| `timeout_s` | 60 | seconds per request |
| `chunk` | 32 | questions per request; more are split across requests |
| `ledger` | true | append one content-free line per call to `~/.local/share/jevx/calls.jsonl` |
| `cache` | true | reuse the stored answer when the endpoint, model, input and question are the same; `--fresh` asks again, `jevx cache enable\|disable` switches it ([the cache](/jevx/reference/privacy/#the-answer-cache)) |
| `cache_ttl_days` | 7 | a stored answer older than this is asked again |
| `accept_min` / `more_max` | 0.35 / 0.65 | the `judge` thresholds the skill quotes (the `stop-judge` plugin carries its own condition) |

## Change one

```bash title="Terminal"
jevx defaults set yes 0.85
jevx defaults set parallel 4 --profile openjevx  # only when that profile is used
jevx defaults unset yes
```

```console
$ jevx defaults x
jevx: usage: jevx defaults [show] | set KEY VALUE | unset KEY  [--profile P]   keys: yes, no, min_confidence, parallel, retries, timeout_s, chunk, ledger, accept_min, more_max, cache, cache_ttl_days
```

## Precedence

built-in < config < profile < the question's own thresholds (`question add --yes/--no/--min`) < flags on the call (`--yes`, `--no`, `--min`, `--parallel`, `--profile`). The most specific wins.

## Profiles

A profile is `url`, `model`, `headers`, and optionally `questions` (a question pack for `judge`). `$VAR` / `${VAR}` in any field is expanded from the environment when the request is made and never written back; a missing variable fails with its name, exit `4`. Legacy `endpoint` / `key_env` fields are still read.

```bash title="Terminal"
jevx profile add hosted https://example.com/v1/systemone --model some-model --header "Authorization: Bearer $JEV_API_KEY"
jevx profile add local 'http://$JEV_HOST/v1/systemone' --model my-model
jevx profile use local
jevx profile list
jevx profile show local
jevx profile remove hosted
```

```console
$ jevx profile add -h
jevx: usage: jevx profile add NAME URL [--model M] [--header 'K: V' ...] [--questions FILE]
```

## Files and environment

| what | where |
| --- | --- |
| config (profiles, questions, plugins, settings) | `~/.config/jevx/config.json`, or `JEVX_CONFIG` |
| repo questions / plugins | `.jevx/questions.json`, `.jevx/plugins.json` (nearest, from cwd up) |
| call ledger | `~/.local/share/jevx/calls.jsonl`; `JEVX_LEDGER=off` disables it |
| answer cache | `~/.cache/jevx/`, or `cache_dir` in config.json |
| plugin verdict log | `~/.local/share/jevx/verdicts.jsonl` |
| installer knobs | `JEVX_VERSION`, `JEVX_BIN`, `JEVX_NO_HOOK` |

See [Config file](/jevx/reference/config/) for the file's shape.
