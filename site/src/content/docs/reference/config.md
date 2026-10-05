---
title: Config file
description: The shape of ~/.config/jevx/config.json and the per-repo .jevx/ files, field by field.
---

jevx keeps everything it writes in one JSON file, `~/.config/jevx/config.json` (override the path with `JEVX_CONFIG`). The commands (`profile`, `question`, `defaults`, `plugin`) edit it for you; you can also edit it by hand. It never holds a key's value, only the `$VAR` reference.

## Shape

An illustrative file with every top-level field (values are examples, not defaults):

```json title="~/.config/jevx/config.json"
{
  "default_profile": "jev",
  "profiles": {
    "jev": {
      "url": "https://your-endpoint/v1/systemone",
      "model": "MODEL",
      "headers": { "Authorization": "Bearer $YOUR_KEY_VAR" }
    },
    "openjevx": {
      "url": "http://127.0.0.1:21160/v1/systemone",
      "model": "openjevx",
      "defaults": { "parallel": 4 }
    }
  },
  "defaults": { "yes": 0.85 },
  "questions": {
    "urgent": {
      "type": "noul",
      "instructions": "Is this urgent for the person receiving it?",
      "yes": 0.85
    }
  },
  "plugins": {},
  "local_context_section": "Jev"
}
```

## Top-level fields

| field | meaning |
| --- | --- |
| `default_profile` | the profile used when `--profile` is not given |
| `profiles` | name → profile (below) |
| `defaults` | settings that override the built-ins; see [Settings](/jevx/reference/settings/) |
| `questions` | named questions: `jevx ask NAME` |
| `plugins` | name → plugin; see [Plugins](/jevx/guides/plugins/) |
| `cache_dir` | where the answer cache lives (default `~/.cache/jevx`; `~` expanded) |
| `local_context_section` | the section heading, global and folder (default `Jev`; any level) |
| `local_context_file` | file holding the folder section (default `AGENTS.md`, then `CLAUDE.md`) |
| `global_context_file` | global files holding the section (comma list, `~` expanded) |

## Profile

| field | meaning |
| --- | --- |
| `url` | the System One endpoint |
| `model` | the model name sent with each request |
| `headers` | header name → value; `$VAR` / `${VAR}` expanded per request |
| `style` | request shape: `typesafe` (the flat System One body, the default) or `cloudflare` (a `{model, input}` envelope whose `{result, success}` reply is unwrapped; a failed envelope is an error with Cloudflare's message). Empty means `typesafe`, except that a URL containing `/ai/run` is treated as `cloudflare`. Set it with `jevx profile add NAME URL --style cloudflare`. Cloudflare needs an AI Gateway balance or BYOK (bring your own key) on the account; without one it answers HTTP 402, which jevx reports as an error (exit `4`). |
| `questions` | optional question pack (JSON) for `judge` |
| `defaults` | settings that apply only when this profile is used |
| `note` | free text |
| `endpoint`, `key_env` | legacy names, still read |

## Question

| field | meaning |
| --- | --- |
| `type` | `noul` (yes/no), `choice` or `score` |
| `instructions` | the question text |
| `criteria` | noul `{true, false}` · choice `{key: desc}` · score `[levels]`, lowest first |
| `context` | background for this question only, sent ahead of its instructions |
| `yes`, `no`, `min_confidence` | this question's own thresholds |

## Per-repo files

The nearest `.jevx/` directory from the working directory up (found like `.git`) can hold:

- `.jevx/questions.json`: questions with the same shape as `questions` above, written by `question add --local`.
- `.jevx/plugins.json`: plugins, written by `plugin add --local`.

A local entry wins over a global one of the same name. Both files are meant to be committed.
