# Backend notes

## Model parameters
- Requests send `max_completion_tokens` (8000) and **no** `temperature`. Reasoning models reject `max_tokens` and `temperature` with a 400; `max_completion_tokens` works for non-reasoning models too, so it is the safe default.
- Still to confirm with `scripts/smoke.sh` against `AI_MODEL_A` and `AI_MODEL_B`: that `json_schema` with `strict: true` works with images and what the latency is.
