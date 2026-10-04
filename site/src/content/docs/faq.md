---
title: FAQ
description: Short answers to the questions people actually ask, including cost, accuracy and what leaves your machine.
---



<details class="faq">
<summary>Is this an LLM?</summary>

It is a call to a System One style endpoint: you send questions plus an input and get back probabilities, a chosen key or a level, no text. The hosted Jev model is the reference; jevx does not care what is behind the URL as long as it speaks that request shape.

</details>

<details class="faq">
<summary>Why not just let Claude decide?</summary>

Because the small calls add up. Deciding whether each of 300 log lines is an error costs the agent context and tokens for every line; one batch call gives a number per line that the agent can filter on. A number is also something an `if` can use.

</details>

<details class="faq">
<summary>Does it explain its answer?</summary>

No. You get `yes 0.92`. If you need the why, ask a better question (ask about the reason directly: "Does this line show a timeout?") or use a `--choice` over the candidate reasons.

</details>

<details class="faq">
<summary>How accurate is it?</summary>

There is no published number, because none would hold for your questions and your model. Run a few dozen of your own items through it, compare with what you would have said, and set `--yes` / `--no` per question. An `eval` command for exactly this is on the maintainer's list, not shipped.

</details>

<details class="faq">
<summary>Does it cache answers?</summary>

Yes, by default. The same endpoint, model, input and question returns the stored answer in milliseconds and sends nothing, so a repeat costs nothing. Only the model's answer is stored (the file is named by a hash), never your input. `--fresh` asks again and refreshes it, an answer older than `cache_ttl_days` (7) is asked again on its own, and `jevx cache disable` turns it off. See [the answer cache](/jevx/reference/privacy/#the-answer-cache).

</details>

<details class="faq">
<summary>Every call fails with "profile jev needs YOUR_KEY_VAR in the environment"</summary>

The installer before v0.12.3 ended with a suggested `jevx profile add jev URL --model M --header "Authorization: Bearer $YOUR_KEY_VAR"`. Typed as printed, it replaces the built-in hosted profile with a placeholder, so every later call stops with exit `4`. Remove it and the built-in profile is back:

```console
$ jevx profile remove jev
$ echo "Prod is down" | jevx is "Is this urgent?"
yes 0.92
```

The v0.12.3 installer prints the real next step instead: set `TYPESAFE_API_KEY`, then ask.

</details>

<details class="faq">
<summary>What does it cost?</summary>

jevx itself is free and open source. A hosted endpoint charges per call, and a repeated call is answered from the [cache](/jevx/reference/privacy/#the-answer-cache) at no charge; `jevx stats` shows calls and tokens per day. A self-hosted or local model costs whatever it costs you to run.

</details>

<details class="faq">
<summary>What does the hosted endpoint see?</summary>

The question, the options, the input and the merged context. Never your config, your other files or your keys. See [Privacy](/jevx/reference/privacy/).

</details>

<details class="faq">
<summary>Can I run it fully offline?</summary>

Yes, if you have a model that serves the System One request shape locally; point a profile at it. jevx makes no other network calls: no telemetry, no update check.

</details>

<details class="faq">
<summary>Does it work with Codex, Cursor, Gemini CLI?</summary>

The skill is a Markdown file in `~/.agents/skills` and `~/.codex/skills`, so any agent that reads those gets it. The plugins use Claude Code's hook protocol and only run there today.

</details>

<details class="faq">
<summary>Why are the plugins disabled after install?</summary>

Because a probability threshold that can refuse a shell command deserves a look at its log first. Enable one, watch `plugin log` in shadow mode, then `plugin mode NAME act`.

</details>

<details class="faq">
<summary>Can it edit my CLAUDE.md?</summary>

No, by design. It reads the `## Jev` section, and `jevx context` shows what it read. There is no command that writes it.

</details>

<details class="faq">
<summary>What is "noul"?</summary>

The endpoint's name for a yes/no question, kept as the flag name (`--noul`) so the CLI matches the API and the model's docs.

</details>

<details class="faq">
<summary>Why exit 3 and 4 rather than 1 for everything that is not yes?</summary>

So `if jevx is …` cannot take the "no" branch because the network was down. `1` is a real no; `3` is "not sure, look yourself"; `4` is "the call failed".

</details>

<details class="faq">
<summary>Where is the config?</summary>

`~/.config/jevx/config.json` (or `JEVX_CONFIG`), repo-level `.jevx/questions.json` and `.jevx/plugins.json`, logs under `~/.local/share/jevx/`. See [Config file](/jevx/reference/config/).

</details>

<details class="faq">
<summary>I used jevcli before. What changed?</summary>

The command is now `jevx`. The binary still reads the old `JEVCLI_CONFIG` and `JEVCLI_LEDGER` variables as a fallback.

</details>
