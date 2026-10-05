---
title: Exit codes & reliability
description: Exit codes an agent can trust, retries with backoff, a reply validator that fails closed, and what the tests cover.
---

## Exit codes

| code | when | what an agent should do |
| --- | --- | --- |
| `0` | yes / decided (or a batch where every input succeeded) | act |
| `1` | at least one no | take the other branch |
| `3` | unsure: in the band between `no` and `yes`, or a choice/score below `min_confidence` | check it yourself or ask the user |
| `4` | any error: no profile, missing `$VAR`, network, HTTP error after retries, invalid reply, bad usage | say the call failed; never treat it as a no |

The rule the code enforces: an error exits `4`, never `1`. A dead endpoint cannot read as "no" in an `if`.

```console
$ jevx is "Is this urgent?" --profile nope --in "x"; echo "exit $?"
jevx: no profile "nope" (have: jev, openjevx)
exit 4
```

A batch exits `0`, or `4` if any input failed; the failed line carries an `"error"` field instead of answers.

## Retries

HTTP 429, 5xx and network errors are retried with backoff, `retries` 3 times by default, `timeout_s` 60 per request. Other HTTP errors (401, 400) fail at once with the status and the first 300 characters of the body.

## Fail-closed validation

A reply is rejected, and the call is an error (exit `4`), unless:

- it answers exactly the questions asked, with nothing extra;
- every probability is in [0, 1] and each distribution sums to 1 (±0.02);
- a choice names one of the offered keys.

A malformed or partial answer never becomes a verdict.

## Hooks fail open

Plugins are the opposite on purpose: in `act` mode an endpoint error lets the tool call or the turn proceed (the hook exits 0), and shadow mode scores in a detached process so it cannot delay the agent. `stop-judge` never blocks the same turn twice. See [Plugins](/jevx/guides/plugins/).

## Tests

`go test ./...` covers reply validation, retries, the Stop gate, the section parser, context merging and plugin conditions against a fake server.

There is no published accuracy benchmark for any question. The maintainer's notes record that a small personal model answered several cookbook recipes wrongly, which is the honest summary: the verdicts are only as good as the model behind the profile.
