---
title: Plugins
description: A hook behaviour as config. At an agent event, ask questions about what is happening and deny, warn, block or add context on the answer. Shipped off, shadow mode first.
---

A plugin is a Claude Code hook written as a few JSON fields instead of a script: which event, which questions to ask, and what to do with the answers.

## What ships

```console
$ jevx plugin list
plugin             on                       enabled  mode    description
bash-guard         PreToolUse:Bash          false    shadow  Before a shell command runs: refuse one that would destroy data, ask about one that touches remote systems.
injection-screen   PostToolUse:WebFetch|WebSearch false    shadow  After the agent reads a web page: warn when the text tries to give the agent instructions.
route              UserPromptSubmit         false    shadow  When the user writes: if the request is simple, tell the agent to hand it to a sub-agent on a smaller model.
stop-judge         Stop                     false    shadow  Before the agent hands a turn back: would the user accept it, and did it stop short?
```

Four plugins, all disabled. Enabling one puts it in **shadow** mode: it runs at the event, logs what it would have decided, and changes nothing. `mode NAME act` lets it act.

:::tip[Why shadow first]
Look at the log for a day before you let a probability threshold refuse a command.
:::

## A plugin is a few fields

```console
$ jevx plugin show bash-guard
{
  "description": "Before a shell command runs: refuse one that would destroy data, ask about one that touches remote systems.",
  "on": "PreToolUse:Bash",
  "ask": "destroys,remote,irreversible",
  "deny": "destroys >= 0.8 && irreversible >= 0.6",
  "warn": "destroys >= 0.5 || remote >= 0.8",
  "enabled": false
}
```

| field | meaning |
| --- | --- |
| `on` | `EVENT[:Tool regex]`: `Stop`, `PreToolUse:Bash`, `PostToolUse:WebFetch\|WebSearch`, `UserPromptSubmit`, `PreCompact`, `SessionStart` |
| `ask` | question names, comma-separated: your saved ones, or the built-ins `accepts`, `wanted_more`, `destroys`, `remote`, `irreversible`, `injection` (yes/no), `kind`, `reaction` (choice), `complexity`, `satisfaction` (score) |
| `deny` / `warn` / `block` / `context` | one condition each: refuse the tool call (PreToolUse); ask the user (PreToolUse) or warn the agent; send the agent back with a reason (Stop); add `say` as context |
| `say` | the text handed to the agent; `{{name}}` is that answer |
| `exec` | a command that decides instead: it gets `{event, payload, state, answers}` on stdin and prints the hook decision JSON |
| `profile`, `gate` | which endpoint; for Stop, `gate: all` judges every turn instead of only turns that edited files with no check after |
| `enabled`, `mode` | off by default; `shadow` or `act` |

Conditions are a tiny expression language: `name >= 0.8 && other < 0.5 || kind == ops`. Answers are numbers (P for yes/no, the numeric level for a score) or keys (a choice).

## Try one without enabling it

```bash title="Terminal"
jevx plugin test bash-guard "git push --force"
```

```json title="output" {3-4}
{
  "decision": {
    "action": "deny",
    "reason": "jevx bash-guard: destroys 0.89, irreversible 0.65, remote 0.94 (deny: destroys >= 0.8 && irreversible >= 0.6)"
  },
  "output": "{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"deny\",\"permissionDecisionReason\":\"jevx bash-guard: destroys 0.89, irreversible 0.65, remote 0.94 (deny: destroys \\u003e= 0.8 \\u0026\\u0026 irreversible \\u003e= 0.6)\"}}",
  "record": {
    "answers": {
      "destroys": 0.89,
      "irreversible": 0.65,
      "remote": 0.94
    },
    "cwd": "/Users/you/repo",
    "decision": "deny",
    "event": "PreToolUse",
    "mode": "",
    "ms": 452,
    "plugin": "bash-guard",
    "profile": "jev",
    "reason": "jevx bash-guard: destroys 0.89, irreversible 0.65, remote 0.94 (deny: destroys >= 0.8 && irreversible >= 0.6)",
    "session": "test",
    "state": "The agent is about to call tool Bash with:\n{\"command\":\"git push --force\"}\nWorking directory: /Users/you/repo",
    "third_party": true,
    "tool": "Bash",
    "ts": "2026-09-27T22:19:12"
  }
}
```

(Paths shortened.) `test` builds a sample payload for the event from the words you give it, or takes a real hook payload with `--payload FILE` / `--payload -`. It shows the answers, the decision, and the exact JSON Claude Code would receive. Nothing is logged.

Here the deny condition fired because `destroys 0.89 >= 0.8` and `irreversible 0.65 >= 0.6`. The warn condition would also have matched; deny wins.

## Turn one on

```bash title="Terminal"
jevx install --hooks                 # one settings entry per event in Claude Code's settings
jevx plugin enable bash-guard        # shadow: logs only
jevx plugin log bash-guard 20        # what it would have decided, with the answers
jevx plugin mode bash-guard act      # deny / ask for real
jevx plugin disable bash-guard
jevx plugin enable all --act         # every plugin at once (also: disable all)
```

`enable NAME --act` skips shadow. `hook status` shows what is installed and a 24-hour count of scored / would-act / errors. `hook run EVENT` is what the settings entry calls; you never run it yourself.

## Add your own

```bash title="Terminal"
jevx question add secret --noul "Does this file content contain a credential, token or private key?"
jevx plugin add secret-guard --desc "Refuse writing a secret into a file" \
  --on "PreToolUse:Write|Edit" --ask secret --deny "secret >= 0.8" --local
jevx plugin test secret-guard "aws_access_key_id = AKIAIOSFODNN7EXAMPLE"
jevx install --hooks && jevx plugin enable secret-guard
```

`test` builds its sample for the tool the plugin watches (here a `Write` with that content). Against hosted Jev (2026-10-04) it denies AWS's documentation example key (`secret` 0.95) and a `password: ...` line (0.96), and allows `LOG_LEVEL=debug` (0.03). Plugins redact secrets before a hosted call, so the model sees `[SECRET]` in place of the value; that marker is enough for it to answer yes.

```console
$ jevx plugin add
jevx: usage: jevx plugin add NAME --on EVENT[:Tool] --ask q1,q2 --deny|--warn|--block|--context "COND" [--say TEXT] [--exec CMD] [--desc TEXT] [--profile P] [--local]
```

`--local` stores it in the repo's `.jevx/plugins.json`, which overrides a global plugin of the same name and can be committed. `--enable` on `add` enables it at once (shadow). `--exec CMD` plugs in a tool from another project without a jevx change.

## Honest limits

- **Claude Code's hook protocol only.** The skill works in Codex and other agents; the plugins do not run there yet.
- **A threshold is not a policy.** `bash-guard` denied `git push --force` above; it will also sometimes deny something harmless or pass something harmful. Keep the agent's own permission system as the real gate.
- **Every matching event costs a call** to your endpoint. `stats` shows how many.
- Shadow mode runs detached so it never slows the agent; `act` runs inline and **fails open** on an error (the hook exits 0 and lets the agent continue).
- `stop-judge` never blocks twice in a row (`stop_hook_active`), so an agent cannot get stuck.
