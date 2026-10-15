# Decisions (OpenAI `/v1/decisions`)

csghub-lite serves the [OpenAI Decisions API](https://developers.openai.com/api/docs/guides/decisions)
from two kinds of local models:

- JEV models whose model card is tagged `jev` or `typed-decisions`, such as
  `autotrust/JEV-9B` and its GGUF quantizations, read through a decision LoRA
  (see [Setup](#setup)).
- Native decision models whose decision head llama.cpp runs itself, such as
  `convaiinnovations/laya` (see [Native decision models](#native-decision-models)).

A decision answers typed questions about shared input in one forward pass and
returns calibrated probabilities instead of generated text:

| type | question | main result |
| --- | --- | --- |
| `predicate` | is a condition true? | `probability` |
| `choice` | pick one of 2-255 typed values (string or boolean) | `choice`, `probabilities`, `confidence` |
| `score` | rate against ordered `levels` | `score` (probability-weighted level index), `probabilities`, `confidence` |

A JEV model keeps serving ordinary chat through `/v1/chat/completions`.

## Setup

A decision model has two parts: the base weights and a small "System 1" block
(`adapter_vllm/` with `decision_head.json`, plus `calibration.json`). GGUF
quantizations usually ship only the base weights, so csghub-lite looks for
the System 1 block in the model directory first, then in every `base_model`
repository named by the model card that is downloaded locally.

```sh
csghub-lite pull mradermacher/JEV-9B-GGUF   # base weights, quantized
csghub-lite pull autotrust/JEV-9B           # provides the System 1 block
```

On first use the adapter is converted to a llama.cpp LoRA GGUF under
`~/.csghub-lite/cache/decision-lora/` and loaded into the model's llama-server
with scale 0, so chat requests run the unmodified base model and only
decision requests enable it.

## Request

```sh
curl http://localhost:11435/v1/decisions -H 'Content-Type: application/json' -d '{
  "model": "huggingface/mradermacher/JEV-9B-GGUF",
  "input": "I was charged twice for my order.",
  "questions": [{
    "type": "choice",
    "name": "department",
    "instructions": "Which department should handle this complaint?",
    "choices": [
      {"value": "billing", "description": "Payments, invoices, and refunds."},
      {"value": "technical", "description": "Problems using the product."},
      {"value": "shipping", "description": "Delivery and tracking."},
      {"value": "other", "description": "Requests outside these categories."}
    ]
  }]
}'
```

The OpenAI SDKs work unchanged with `base_url` pointed at csghub-lite, for
example `client.decisions.create(...)` in Python. The response has the
Decisions API shape:

```json
{
  "answers": [{
    "type": "choice",
    "name": "department",
    "choice": "billing",
    "probabilities": [
      {"value": "billing", "probability": 0.97},
      {"value": "technical", "probability": 0.004},
      {"value": "shipping", "probability": 0.003},
      {"value": "other", "probability": 0.023}
    ],
    "confidence": 0.95
  }],
  "model": "huggingface/mradermacher/JEV-9B-GGUF",
  "usage": {"input_tokens": 73, "output_tokens": 0, "total_tokens": 73, "...": "..."}
}
```

`input` is a string or user messages with `input_text` parts. Up to 200
questions share one input; answers come back in question order with each
question's `name` (or `null`).

## How questions map onto the model

- `predicate` uses the head's yes/no slots; `probability` is P(true).
- `choice` uses the head's choice slots with each option shown as
  `value: description`. JEV-9B answers 16 options per pass; larger sets run
  as a tournament of 16-option groups and a final.
- `score` asks the choice head to pick among your levels; `score` is the
  probability-weighted level index, so it can fall between levels.
- `confidence` is the gap between the two most likely options.
- csghub-lite never returns `refusal` answers.

## Native decision models

llama.cpp runs some decision models natively and answers them at its
TypeSafe-compatible `/v1/systemone` endpoint: Laya and Julia (ModernBERT
encoders with a decision head), OpenJev, Lev, Kev, Nimble, Clef, Perplexity
Decider and LFM2-D1. csghub-lite recognizes them by the
`<arch>.decision.type` metadata of their GGUF, or for a Laya-style checkpoint
that is not converted yet, by its layout (`encoder/config.json` next to
`rl_agent_config.json` or `julia_config.json`). They need no tag and no
separate adapter.

```sh
csghub-lite pull convaiinnovations/laya
curl http://localhost:11435/v1/decisions -H 'Content-Type: application/json' -d '{
  "model": "huggingface/convaiinnovations/laya",
  "input": "Customer message: I was charged twice and nobody has replied.",
  "questions": [
    {"type": "choice", "name": "route", "instructions": "Which team should handle this?",
     "choices": [{"value": "billing"}, {"value": "shipping"}, {"value": "technical"}]},
    {"type": "predicate", "name": "angry", "instructions": "Is the customer angry?"},
    {"type": "score", "name": "urgency", "instructions": "How urgent is this?",
     "levels": [{"label": "can wait"}, {"label": "this week"}, {"label": "today"}, {"label": "right now"}]}
  ]
}'
```

On first use csghub-lite converts the SafeTensors checkpoint to GGUF with the
bundled llama.cpp converter (a few seconds for Laya) and starts llama-server
with the whole context as one batch, since these heads read the full prompt
in one pass. llama.cpp sizes its output buffers to that batch, so memory grows
with hidden size times context: about 10 MB for Laya (1,024 hidden, 8,192
context), but Kev and Clef have Qwen-sized hidden states and can need a few
hundred MB more at a 16K context. Lower the model's context length in its
settings if that matters; inputs longer than it are then refused. csghub-lite translates each request to `/v1/systemone`:
`predicate` becomes `noul`, `choice` keeps its values as option keys with their
descriptions, and `score` passes the levels as `label: description`. The
probabilities are the model's own, scaled by the temperatures stored in the
GGUF; `confidence` is again the gap between the two most likely options.

Laya, Kev, Clef and LFM2-D1-Omni read hidden states rather than logits, so
they cannot generate text: chat requests are refused with a pointer to
`/v1/decisions`, the model listing marks them `decision_only`, and the Web UI
opens them in **Decide** mode without a Chat option. OpenJev, Lev, Nimble,
Perplexity Decider and LFM2-D1 still chat.

Limits of native models: a score question takes 2-10 levels; the number of
choice options depends on the model (255 for Laya); and Laya cuts long
questions and options to the token budget it was trained with. The state is
not cut: Laya English was trained on 512-token inputs, and accuracy on much
longer inputs is not guaranteed.

## JEV-compatible endpoint

`POST /v1/decide` (JEV models only) keeps the hosted JEV API shape (`kind` noul / score 0-5 /
choice, `state`, `question`, `options`; response `probabilities`, `choice`,
`choice_index`) for clients written against JEV, and `GET
/v1/decide/info?model=...` reports the head's protocol, slot counts and
calibration temperatures.

## Limits

- `input_image` parts are refused: JEV-9B's decision head reads text, and
  csghub-lite does not yet pass images to native decision models.
- Decisions run on local llama.cpp models only.
- On JEV-9B Q4_K_M the probabilities stay within about 0.05 of the model
  card's bf16 reference values.
