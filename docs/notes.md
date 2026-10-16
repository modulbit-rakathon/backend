# Backend notes

## Model parameters
- Deployment names (the `model` field) are `gpt-6-1-sol-dz-eu` (A) and `gpt-6-luna-dz-eu` (B). `gpt-6.1-sol` and `gpt-6-luna` return 404 `DeploymentNotFound`.
- Both are reasoning models (`reasoning_tokens` > 0 in usage). Requests send `max_completion_tokens` (8000) and **no** `temperature`.
- Smoke test of 2026-10-16: `json_schema` with `strict: true` and an image works on both models, about 4 s per box photo.

## First real analyze (model A, 2026-10-16)
- Box (Advagraf): about 9 s, about 3k prompt tokens. Bilingual CZ/SK packaging comes back joined with `\n` in `active_substance` and `form`. That is faithful but noisy; to sort out when tuning the prompt.
- Note (a list with a printed and a handwritten part): about 32 s, 1.7k completion tokens, 18 items. `note_style` is set per item correctly, and handwritten items get `uncertain_fields`.
