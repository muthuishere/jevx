---
title: Memory
description: Judge an item against your own notes. jevx retrieves the best-matching sections of a folder of markdown (BM25, no embeddings) and sends them after the item. Measured numbers (largest run first), the weak spot, and the strict rule that covers it.
---

`jevx memory` points a question at a folder of distilled, cited markdown: a wiki, runbooks, release notes. For each item it retrieves the best-matching sections and sends them **after** the item, so a server that keeps only its first N tokens can trim notes but never the item. Retrieval is BM25 over heading sections: no embeddings, no vector database, and nothing leaves your machine to build the index.

## Set it up

```bash title="Terminal"
jevx memory add docs ./wiki --pin NOTICES.md --repo app=~/src/app   # register a folder; cites like app:path:L10-L20 are hashed
jevx memory index docs                                              # (re)build after the pages change
jevx memory check docs                                              # pages whose cited lines changed are marked stale and skipped
jevx memory show docs "does v2 drop the 512-token limit?"           # what would be retrieved, with page#heading and lines
jevx memory list
```

Retrieval order for each item: pages the item names first, then pinned pages (`--pin`), then the best BM25 sections, up to `--memory-k` sections and `--memory-budget` characters.

## Ask with it

```bash title="Terminal"
jevx ask --memory docs --lines claims.txt --noul true="Is this claim correct per the notes?"
jevx ask --memory docs --memory-strict --lines claims.txt --noul true="Is this claim correct per the notes?"
```

| flag | default | meaning |
| --- | --- | --- |
| `--memory NAME` | | append the best-matching notes of that memory after each item |
| `--memory-k N` | 3 | BM25 sections after named and pinned pages |
| `--memory-budget N` | 2000 | characters of notes |
| `--memory-strict` | off | a number, `` `code` `` or name in the item that the retrieved notes lack turns a yes/no answer into `no` |

`--json` and `--raw` carry the retrieved notes (`memory`, with page, heading and lines) and the missing facts (`memory_diff`).

## Freshness

Every cite in a page (`app:path:L10-L20`) is hashed when the page is indexed. `jevx memory check` re-hashes them: a page whose cited lines changed is marked stale and is not retrieved until you fix it and re-index. Lines that only moved (lines inserted above them) are recognised and not flagged.

Pages themselves are checked on every use: `list`, `show` and `ask --memory` hash each page against the index. A page edited or removed since `index` is left out of retrieval (its indexed text is out of date) and a warning names what drifted, with new pages counted too:

```console
$ jevx memory list
jevwiki   259 sections   9 stale  ~/wiki  indexed 2026-10-04 10:06  (9 changed, 1 new since index: jevx memory index jevwiki)
```

Run `jevx memory index NAME` after you edit pages. jevx never re-indexes on its own, because that would re-baseline the cite hashes `check` compares against.

## How well it works

Measured on held-out claims, with the configuration frozen before each test (k 3, budget 2000, item first). The largest run comes first; it is the most reliable, and its effect is smaller than the earlier, smaller runs showed.

**Largest run: 143 fresh claims** (71 true page sentences, 72 with one number changed), local 0.4B model:

| condition | AUC | accuracy | changed numbers caught | true claims wrongly failed |
| --- | --- | --- | --- | --- |
| no notes | 0.48 | 49% | 66 / 72 | 67 / 71 (it calls almost everything false) |
| notes | **0.76** | 69% | 50 / 72 | 22 / 71 |
| notes + `--memory-strict` | n/a | **81%** | **70 / 72** | 25 / 71 |

Notes help (McNemar p=0.0004), and `--memory-strict` helps on top of them (p=0.0005). Its two error rates on this run: it **misses 2 of 72 changed numbers (3%)** and **wrongly rejects 25 of 71 true claims (35%)**. So a strict "no" flags the claim for a person to check; it never decides alone.

**The weak spot is a changed number.** A note that matches except for one figure pulls the answer toward "true": with notes alone, 50 of 72 changed numbers were caught. `--memory-strict` compares the numbers, `` `code` `` and names in the item with those in the retrieved notes and fails a yes/no claim when one is missing. On its own, the rule flags a true claim wrongly only when the right section was not among the retrieved ones; a wider retrieval window was tested and did not help, so the defaults stay.

**Earlier, smaller runs** (same method, kept for the record):

| run | without notes | with notes | `--memory-strict` |
| --- | --- | --- | --- |
| local model, n=68 claims | AUC 0.30, accuracy 47% | AUC 0.86, accuracy 71% (p=0.023) | |
| hosted model, n=48 | AUC 0.62, accuracy 42% | AUC 0.83, accuracy 81% (p=0.0005) | |
| local model, n=80 rows | | accuracy 75%, 29 / 40 changed numbers caught, 9 / 40 true failed | accuracy 86%, 40 / 40 caught, 11 / 40 true failed (p=0.022) |

## Limits

- **It adds missing facts, not missing reasoning.** On a judgement task (not a fact check), notes did not help the small local model.
- **The strict rule checks presence, not negation.** Notes that say "not MIT" contain "MIT".
- **Samples are small** (n = 48 to 143). These are the measured numbers for these sets; run your own claims before trusting a threshold.
