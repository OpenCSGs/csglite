# Decisions (OpenAI `/v1/decisions`)

csghub-lite serves the [OpenAI Decisions API](https://developers.openai.com/api/docs/guides/decisions)
from local models whose model card is tagged `jev` or `typed-decisions`, such as
`autotrust/JEV-9B` and its GGUF quantizations. A decision answers typed
questions about shared input in one prefill pass per question and returns
calibrated probabilities instead of generated text:

| type | question | main result |
| --- | --- | --- |
| `predicate` | is a condition true? | `probability` |
| `choice` | pick one of 2-255 typed values (string or boolean) | `choice`, `probabilities`, `confidence` |
| `score` | rate against ordered `levels` | `score` (probability-weighted level index), `probabilities`, `confidence` |

The same model keeps serving ordinary chat through `/v1/chat/completions`.

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

## JEV-compatible endpoint

`POST /v1/decide` keeps the hosted JEV API shape (`kind` noul / score 0-5 /
choice, `state`, `question`, `options`; response `probabilities`, `choice`,
`choice_index`) for clients written against JEV, and `GET
/v1/decide/info?model=...` reports the head's protocol, slot counts and
calibration temperatures.

## Limits

- `input_image` parts are refused: JEV-9B's decision head reads text.
- Decisions run on local llama.cpp models only.
- On JEV-9B Q4_K_M the probabilities stay within about 0.05 of the model
  card's bf16 reference values.
