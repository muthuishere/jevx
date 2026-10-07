---
title: Changelog
description: What changed in each jevx release, newest first. Written by the release command from the commits since the previous release.
---

## v0.12.13 (2026-10-07)

- A plugin enabled in one repo (`jevx plugin add ... --local`) keeps its Claude Code hook when `jevx install`, an installer or an upgrade runs from another folder. In v0.12.12 that removed the repo's hook. jevx now remembers folders that have plugins and forgets one when its `.jevx/plugins.json` is gone.
- A plugin's `--exec` command gets 15 seconds. One that hangs now fails open (the tool call goes ahead, the error is logged) instead of holding every tool call until Claude Code's 20-second hook limit.
- Release checks wait for npm to serve a new version before testing the npm install, and fail if a default install (no plugin enabled) leaves any jevx hook in Claude Code's settings, on Linux, macOS and Windows, through install.sh, install.cmd and npm.

## v0.12.12 (2026-10-06)

- **Hooks run only when you turn a plugin on.** Installing jevx no longer adds Claude Code hook entries for disabled plugins: `settings.json` holds a jevx entry only for an event that has an enabled plugin, and `jevx plugin enable` / `disable` add and remove it. Running `jevx install` (or any installer) removes entries left by earlier versions, and keeps other tools' hooks. Before, every install added four entries that ran jevx on each tool call; if that binary later disappeared (a test install in a temporary folder), every tool call failed.
- jevx refuses to write hooks for a binary in a temporary folder, and on macOS and Linux a hook entry does nothing if its binary is gone.

## v0.12.11 (2026-10-06)

- The release script always allows three vault values that are also public strings here (`DB_SSLMODE`, `SMTP_HOST`, `DEEMWAR_REGISTRY_USER`). No change to the jevx binary.

## v0.12.10 (2026-10-06)

- The local-endpoint example in the agent skill, README and docs is openjevx on its default port since openjevx v0.5.11: `jevx profile add openjevx http://127.0.0.1:21160/v1/systemone --model openjevx`. jevx ships no openjevx profile, so an existing profile keeps its URL; one made for an openjevx older than v0.5.11 (port 21118) keeps working while that server stays on 21118.
- [Memory](/jevx/guides/memory/#how-well-it-works) states its retrieval coverage: on 1042 verified-true claims, v0.12.9's notes carry every number, code and name for all 1042 (1012 before), so `--memory-strict` wrongly fails none of them.
- The docs site build fails on a broken internal link or `#anchor`.
- The agent skill says what to do with the memory drift warning (`N pages changed ... since index`): re-index when the agent edited the docs itself, otherwise tell the user, and report that those pages were left out.

## v0.12.9 (2026-10-05)

- Memory retrieval no longer lets a common word fill the notes. A page counts as named in an item when the item has its full name, or the first word of its name when no other page starts with that word: `model` no longer names both `model-card.md` and `model-from-object-storage.md`, which used up the note budget before the section that matched. On 1042 checked claims against a 16-page wiki, the notes now carry every number, code and name in the claim for all 1042 (before: 1012).

## v0.12.8 (2026-10-05)

- Memory never serves an edited page from an old index. `jevx memory list`, `memory show` and `ask --memory` compare each page with the index on every use: a page edited or removed since `jevx memory index` is left out of retrieval, a warning on stderr names what drifted (new pages are counted too), and `list` counts those pages as stale. Before, a stale index answered silently with the old text and `list` showed `0 stale`. jevx still never re-indexes on its own; run `jevx memory index NAME` after editing pages. See [Memory](/jevx/guides/memory/#freshness).
- The installers' last line and the missing-key error say where to create a key: https://console.typesafe.ai/keys. The site's first page and Getting started link it too.

## v0.12.7 (2026-10-05)

- Profiles can target Cloudflare Workers AI: `jevx profile add NAME URL --style cloudflare` sends the `{model, input}` envelope and unwraps the reply (including the nested `{state, result}` of `/ai/run`; a run that is not `Completed` is an error). The style is picked automatically for URLs containing `/ai/run`. It needs AI Gateway credit or your own provider key (BYOK); without either, Cloudflare answers 402. See [Config file](/jevx/reference/config/).
- The agent skill's context example is a real, reproducible run: "Can you send me the Q3 revenue numbers before the board meeting?" goes from unsure 0.74 (no context) to yes 0.93 ("the meeting starts in 20 minutes") to unsure 0.25 ("in three months").

## v0.12.6 (2026-10-05)

- The redaction notice names what it removed, by category, never the values: `jevx: redacted 1 password (in a URL), 1 email-shaped (user@host) ...`. Before, it said only "N item(s)". The `user@host` part of a connection URL such as `postgres://app@db.internal` matches the email rule, so the notice calls it email-shaped instead of hiding why the hostname was removed.

## v0.12.5 (2026-10-05)

- `jevx plugin test` builds its sample for the tool the plugin watches. A plugin on `Write`, `Edit` or `WebFetch` (the `secret-guard` example, the shipped `injection-screen`) printed "would not run" and could not be tested.
- The agent skill quotes the largest memory measurement (143 held-out claims: AUC 0.48 to 0.76; `--memory-strict` misses 3% of changed numbers and wrongly rejects 35% of true claims, so a strict "no" flags for a person and never decides alone). Earlier releases quoted a smaller run (AUC 0.86).

## v0.12.4 (2026-10-04)

- Hosted-call redaction is no longer silent: when jevx removes a secret, email or phone number before a call to a hosted endpoint, it prints one line on stderr (`jevx: redacted N item(s) ...`, count only, never the values), also for cached answers. A "does this contain a password?" no longer comes back as an unexplained `unsure`.
- `jevx ask -h` prints its flags correctly (`-yes float`, not `-yes jevx defaults`).
- FAQ: the one-command fix for anyone who typed the pre-v0.12.3 installer hint (`jevx profile remove jev`).

## v0.12.3 (2026-10-04)

- The installer's last line now gives the real next step (set `TYPESAFE_API_KEY`, then one ask). Before, it suggested `jevx profile add jev URL ...`, which, typed as printed, replaced the built-in hosted profile and made every later call fail.
- Every release is installed and checked on Linux, macOS and Windows (install.sh, install.cmd and npm) before users get it.
- `go.mod` names the toolchain `go 1.26.0`, so building a checkout with an older Go works.
