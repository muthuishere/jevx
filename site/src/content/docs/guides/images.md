---
title: Images and profile types
description: Pick which backend answers with a profile's type (TypeSafe, OpenRouter, Cloudflare, Vercel or your own server), and send screenshots and photos with a question, one at a time or as a parallel batch.
---

## Profile types

jevx keeps the profiles; [toolnexus](https://github.com/muthuishere/toolnexus) sends the requests. A profile's `type` is the toolnexus classifier backend that answers it, and on each call jevx hands toolnexus that backend with the profile's url, model and headers. toolnexus builds the request for the backend, retries, checks the reply and reads it back. Questions, verdicts, thresholds, the cache and batches are the same for every type.

| type | what | url, model and key when the profile gives none | images |
| --- | --- | --- | --- |
| `typesafe` (default) | TypeSafe's System One API (hosted Jev). With its own url, any System One server such as openjevx | `https://api.typesafe.ai/v1/systemone`, `jev-latest`, `$TYPESAFE_API_KEY` | yes |
| `openrouter` | OpenRouter's System One route | `https://openrouter.ai/api/v1/systemone`, `typesafe/jev-1.14`, `$OPENROUTER_API_KEY` | yes |
| `cloudflare` | Cloudflare Workers AI `/ai/run`: the System One body in a `{model, input}` envelope | `.../accounts/${CLOUDFLARE_ACCOUNT_ID}/ai/run`, `typesafe/jev`, `$CLOUDFLARE_JEV_API_KEY` | Clef models only |
| `vercel` | Vercel AI Gateway's TypeSafe-compatible route | `https://ai-gateway.vercel.sh/typesafe/v1/systemone`, `typesafe-ai/jev`, `$AI_GATEWAY_API_KEY` | no, unless `--images` |
| `custom` | a self-hosted System One endpoint | url required, no key unless `--header` | no, unless `--images` |

`jevx profile types` prints the same table from the toolnexus version jevx was built with, and `jevx help profiles` adds examples. With no config at all, jevx uses the built-in `jev` profile (type `typesafe`, `$TYPESAFE_API_KEY`).

```bash title="Terminal"
jevx profile add or --type openrouter                                   # OpenRouter's url, model and key variable
jevx profile add cf --type cloudflare                                   # needs CLOUDFLARE_ACCOUNT_ID and CLOUDFLARE_JEV_API_KEY
jevx profile add clef --type cloudflare --model @cf/cloudflare/clef     # Clef on Workers AI takes images
jevx profile add openjevx http://127.0.0.1:21160/v1/systemone --model openjevx   # typesafe type, your own server
jevx profile list
```

A profile with its own url gets no key header unless you give one (`--header 'Authorization: Bearer $VAR'`), so a key never reaches a host you did not name. A url containing `/ai/run`, `openrouter.ai` or `ai-gateway.vercel.sh` sets the type when `--type` is left out. The older `style` field and `--style` flag still work. There is no `openai` type yet: OpenAI's Decisions API needs a second wire in toolnexus ([toolnexus#157](https://github.com/muthuishere/toolnexus/issues/157)). A failed Cloudflare envelope currently reports only "envelope not successful", without Cloudflare's own message such as a missing AI Gateway balance ([toolnexus#156](https://github.com/muthuishere/toolnexus/issues/156)). A System One url is given in full (`.../v1/systemone`): toolnexus posts to its base plus `/systemone`.

## Images

`--image FILE` sends a PNG or JPEG with the input. Repeat it for several images.

```bash title="Terminal"
jevx is "Is this an error page?" --image shot.png
jevx ask --image before.png --image after.png --noul same="Do the two screenshots show the same layout?"
jevx pick "Which state is the checkout in?" ok=works error="error shown" blank="blank page" --image shot.png --in "Checkout page"
```

jevx puts each image in the state after the text, and toolnexus does the rest:

- **typesafe, openrouter:** System One `assets` named `image1`, `image2`, ..., with each name in the state, and the `TypeSafe-Danger-Zone-Images: true` header TypeSafe requires. TypeSafe marks image input as a "danger zone" feature of Jev 1.14, not recommended for production; use a model that takes images (`--model jev-1.14.0`).
- **cloudflare:** Clef models (`@cf/cloudflare/clef`, `clef-flash`) get them as data URLs in `input.images`. Jev on Workers AI is refused: images there are not proven.
- **vercel, custom:** refused unless the profile sets `"images": true` (`jevx profile add ... --images`).

toolnexus checks TypeSafe's limits before any request: PNG or JPEG, 280 to 1568 px per side, 16 MiB per image, 32 MiB per request. An image outside them fails with exit `4`; it is never resized. `--no-images` on `profile add` turns images off for an endpoint whose type would otherwise send them.

The image bytes are part of the cache key, so a changed image is asked again. Images are never redacted: do not send screenshots that show secrets to a hosted endpoint ([Privacy](/jevx/reference/privacy/)).

## Batches of images

`--images` is a batch with one input per image: a folder (its `.png`, `.jpg` and `.jpeg` files), a glob, or a text file listing one path per line. `--in` gives every image the same text. Each row is shown by its image file, and `filter` prints the matching files.

```bash title="Terminal"
jevx filter "Does this photo show a damaged package?" --images ./returns/
jevx rank "Does this screenshot show an error?" --images 'shots/*.png' --top 5
jevx ask --images shots/ --in "Checkout page" --noul err="Is an error shown?" --choice state="Which state?|ok=works;error=error shown;blank=blank page"
```

```text title="output (ask)"
err  P     state  P     INPUT
no   0.04  ok     0.91  shots/01-cart.png
yes  0.97  error  0.88  shots/02-pay.png
```

The output above shows the table's shape; the probabilities are illustrative, not from a recorded run.

`--image FILE` with `--lines` or `--states` attaches the same image to every input instead, e.g. one reference design checked against many descriptions.

No backend has a request that carries several inputs, so a batch is one request per input through toolnexus, `--parallel N` at a time (default 8), printed in input order. A failed input prints its error on stderr and the rest go on; the batch exits `4` if any failed.
