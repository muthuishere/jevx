---
title: Images and profile types
description: Send screenshots and photos with a question, one at a time or as a parallel batch, to the profile types that take them: TypeSafe System One, Cloudflare Workers AI and the OpenAI Decisions API.
---

## Profile types

A profile's `type` is the API its endpoint speaks. Questions, verdicts, thresholds, the cache and batches work the same for every type; only the request body, the reply and image support differ.

| type | what | request body | images |
| --- | --- | --- | --- |
| `typesafe` (default) | System One / Jev: TypeSafe's hosted Jev, openjevx, any System One server | `{model, state, questions: {NAME: {...}}, assets}` | yes |
| `cloudflare` | Cloudflare Workers AI `/ai/run` | `{model, input: {state, questions}}`, reply in `{result, success}` | no, unless the profile sets `"images": true` |
| `openai` | OpenAI Decisions API, `/v1/decisions` | `{model, input, questions: [{name, type, ...}]}` | yes |

`jevx profile types` prints the same, and `jevx help profiles` adds examples. With no config at all, jevx uses the built-in `jev` profile (type `typesafe`, `$TYPESAFE_API_KEY`).

```bash title="Terminal"
jevx profile add openjevx http://127.0.0.1:21160/v1/systemone --model openjevx                    # type typesafe
jevx profile add cf 'https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT/ai/run' --type cloudflare \
  --model typesafe/jev --header 'Authorization: Bearer $CF_API_TOKEN'
jevx profile add oai https://api.openai.com/v1/decisions --type openai --model gpt-6-luna \
  --header 'Authorization: Bearer $OPENAI_API_KEY'
jevx profile list
```

A URL containing `/ai/run` is read as `cloudflare` when no type is given. The older `style` field and `--style` flag still work.

### How the openai type maps questions

jevx keeps its own question kinds and converts them per request:

| jevx | Decisions API | the answer read back |
| --- | --- | --- |
| `--noul` (yes/no) | `predicate`; the "true means / false means" text is added to the instructions | `probability` is P(yes) |
| `--choice` | `choice` with `choices: [{value, description}]` | `value`, its probability is the confidence |
| `--score` | `score` with `levels: [{label}]`, lowest first | `score` (fractional) and `confidence` |

Answers come back by name (or in question order). A `refusal` answer is an error, exit `4`, never a "no". OpenAI accepts at most 10 questions per request, so an `openai` profile's `chunk` defaults to 10. `--raw` on one input prints the reply after this conversion, in the System One shape.

The OpenAI mapping follows OpenAI's published Decisions API docs; jevx's tests cover it against a stub server, not the live API.

## Images

`--image FILE` sends a PNG or JPEG (up to 16 MB, checked from its bytes) with the input. Repeat it for several images.

```bash title="Terminal"
jevx is "Is this an error page?" --image shot.png
jevx ask --image before.png --image after.png --noul same="Do the two screenshots show the same layout?"
jevx pick "Which state is the checkout in?" ok=works error="error shown" blank="blank page" --image shot.png --in "Checkout page"
```

How each type sends them:

- **typesafe:** as System One `assets`, named `image1`, `image2`, ... The state names them right after the item (`Image: image1`; a JSON state gets an `"images"` field), since a System One server rejects an asset the state never mentions. The request carries `TypeSafe-Danger-Zone-Images: true`, TypeSafe's opt-in for image input (a header the profile sets itself wins). TypeSafe marks image input as a "danger zone" feature of Jev 1.14 and later, not recommended for production; use a model that supports it (`--model jev-1.14.0` or newer).
- **openai:** as `input_image` parts (base64 `data:` URLs) next to an `input_text` part, in one user message.
- **cloudflare:** refused before any call, exit `4`. If the model behind your Workers AI profile does take images, set `"images": true` on the profile (`jevx profile add ... --type cloudflare --images`); they then go as `assets` inside the envelope.

`--no-images` on `profile add` turns images off for an endpoint whose type would otherwise send them.

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

None of the three APIs has a batch endpoint, so a batch is one request per input, `--parallel N` at a time (default 8), printed in input order. A failed input prints its error on stderr and the rest go on; the batch exits `4` if any failed.
