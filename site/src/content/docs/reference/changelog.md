---
title: Changelog
description: What changed in each jevx release, newest first. Written by the release command from the commits since the previous release.
---

## v0.12.4 (2026-10-04)

- Hosted-call redaction is no longer silent: when jevx removes a secret, email or phone number before a call to a hosted endpoint, it prints one line on stderr (`jevx: redacted N item(s) ...`, count only, never the values), also for cached answers. A "does this contain a password?" no longer comes back as an unexplained `unsure`.
- `jevx ask -h` prints its flags correctly (`-yes float`, not `-yes jevx defaults`).
- FAQ: the one-command fix for anyone who typed the pre-v0.12.3 installer hint (`jevx profile remove jev`).

## v0.12.3 (2026-10-04)

- The installer's last line now gives the real next step (set `TYPESAFE_API_KEY`, then one ask). Before, it suggested `jevx profile add jev URL ...`, which, typed as printed, replaced the built-in hosted profile and made every later call fail.
- Every release is installed and checked on Linux, macOS and Windows (install.sh, install.cmd and npm) before users get it.
- `go.mod` names the toolchain `go 1.26.0`, so building a checkout with an older Go works.
