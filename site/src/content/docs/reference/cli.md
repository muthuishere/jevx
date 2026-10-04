---
title: CLI reference
description: Every jevx command and subcommand on one page, with the usage lines the binary prints.
---

```console
$ jevx help
usage: jevx ask|is|pick|filter|rank|question|memory|context|defaults|cache|judge|plugin|hook|profile|stats|install|uninstall|skill|version
  jevx COMMAND -h for flags; jevx skill for the full guide; https://muthuishere.github.io/jevx/
```

Subcommands that use Go's flag parser (`ask`, `judge`, `install`, `uninstall`, `stats`) print full help on `-h`. The others print a one-line usage and exit `4`; those lines are quoted below.

## Asking

| command | what | page |
| --- | --- | --- |
| `ask` | the full command: questions inline or by name, one input or a batch | [Ask](/jevx/guides/ask/) |
| `is` | one yes/no question: `VERDICT P`, exit 0 / 1 / 3 / 4 | [Shortcuts](/jevx/guides/shortcuts/) |
| `pick` | one pick-one question over `key=desc` options | [Shortcuts](/jevx/guides/shortcuts/) |
| `filter` | the input lines whose answer is yes (`-v`: the rest) | [Shortcuts](/jevx/guides/shortcuts/) |
| `rank` | every line with P(yes), best first (`--top N`) | [Shortcuts](/jevx/guides/shortcuts/) |
| `judge` | four turn-level questions: would the user accept this proposal? | [Ask](/jevx/guides/ask/#judge-before-handing-a-turn-back) |

```text title="shortcut usage"
jevx: usage: jevx is "QUESTION"|NAME ...  (see: jevx help)
jevx: usage: jevx filter "QUESTION"|NAME ...  (see: jevx help)
jevx: usage: jevx rank "QUESTION"|NAME ...  (see: jevx help)
```

## Configuring

| command | what | page |
| --- | --- | --- |
| `question add\|list\|show\|remove` | named questions, global or `--local` | [Saved questions](/jevx/guides/questions/) |
| `context` | which `## Jev` sections would be sent, and from which files | [Context](/jevx/guides/context/) |
| `defaults [show]\|set\|unset` | every setting, its value and where it comes from | [Settings](/jevx/reference/settings/) |
| `memory add\|index\|check\|show\|list` | a folder of cited markdown notes for `ask --memory`: register, index, check freshness, preview retrieval | [Memory](/jevx/guides/memory/) |
| `cache [stat]\|enable\|disable\|ttl DAYS\|dir [PATH]\|clear` | the answer cache: status, switch it on or off, set how long answers live, move it, or delete everything (also `config cache …`) | [Privacy](/jevx/reference/privacy/#the-answer-cache) |
| `profile list\|add\|use\|remove\|show` | endpoints | [Settings](/jevx/reference/settings/#profiles) |

```text title="usage lines"
jevx: usage: jevx question add NAME --noul "QUESTION" | --choice "QUESTION|k=desc;k2=desc" | --score "QUESTION|low;mid;high" [--yes 0.85 --no 0.15 --min 0.6]
jevx: usage: jevx defaults [show] | set KEY VALUE | unset KEY  [--profile P]   keys: yes, no, min_confidence, parallel, retries, timeout_s, chunk, ledger, accept_min, more_max, cache, cache_ttl_days
jevx: usage: jevx profile add NAME URL [--model M] [--header 'K: V' ...] [--questions FILE]
```

## Hooks and plugins

| command | what | page |
| --- | --- | --- |
| `plugin list\|show\|add\|remove\|enable\|disable\|mode\|test\|log` | hook behaviours as config | [Plugins](/jevx/guides/plugins/) |
| `hook run EVENT\|status\|review [N]` | the generic runner Claude Code calls, plus status and review | [Plugins](/jevx/guides/plugins/#turn-one-on) |

```text title="usage lines"
jevx: usage: jevx plugin add NAME --on EVENT[:Tool] --ask q1,q2 --deny|--warn|--block|--context "COND" [--say TEXT] [--exec CMD] [--desc TEXT] [--profile P] [--local]
jevx: usage: jevx hook run EVENT | status | review [N] | enable|disable stop  (plugins: jevx plugin)
```

## Setup and housekeeping

| command | what | page |
| --- | --- | --- |
| `install` / `uninstall` | `--skills` / `--hooks` (neither flag = both) | [Install](/jevx/start/install/#what-jevx-install-does) |
| `stats` | the content-free call ledger, per day and host (`--days N`, default 7) | [Privacy](/jevx/reference/privacy/#the-ledger) |
| `skill` | print the skill Markdown | [Using it from an agent](/jevx/guides/agents/) |
| `version` | the release tag (`jevx dev` for a local build) | |

## Global flags

Every command takes `--cwd DIR` to run as if from that directory. The asking commands take `--profile`, `--yes`, `--no`, `--min`, `--parallel`, `--context TEXT|@file` and `--no-context`. `ask` and the shortcuts also take `--fresh`: skip the answer cache and refresh what it holds. `ask` takes `--memory NAME`, `--memory-k`, `--memory-budget` and `--memory-strict` ([Memory](/jevx/guides/memory/)).
