# Backend notes

## Model parameters
- Deployment names (the `model` field) are `gpt-6-1-sol-dz-eu` (A) and `gpt-6-luna-dz-eu` (B). `gpt-6.1-sol` and `gpt-6-luna` return 404 `DeploymentNotFound`.
- Both are reasoning models (`reasoning_tokens` > 0 in usage). Requests send `max_completion_tokens` (8000) and **no** `temperature`.
- Smoke test of 2026-10-16: `json_schema` with `strict: true` and an image works on both models, about 4 s per box photo.

## First real analyze (model A, 2026-10-16)
- Box (Advagraf): about 9 s, about 3k prompt tokens. Bilingual CZ/SK packaging comes back joined with `\n` in `active_substance` and `form`. That is faithful but noisy; to sort out when tuning the prompt.
- Note (a list with a printed and a handwritten part): about 32 s, 1.7k completion tokens, 18 items. `note_style` is set per item correctly, and handwritten items get `uncertain_fields`.

## Baseline eval (2026-10-16, `cmd/eval`, 27 sheets, 160 items per half)
Sheets split into a printed and a handwritten half (`scripts/split_samples.py`). Ground truth comes from `50_Pacientu.docx`. Strength is compared by numbers.

| model | half | name found | strength OK | unflagged error | extra items |
|---|---|---|---|---|---|
| sol (A) | printed | 99 % | 100 % | 0 | 1 |
| sol (A) | handwritten | 95 % | 95 % | 3 (2 %) | 11 |
| luna (B) | printed | 99 % | 100 % | 10 (6 %) | 1 |
| luna (B) | handwritten | 29 % | 76 % | 3 | 103 |

- **Sol is the model for analyze.** Luna is unusable on handwriting (it mostly invents names) but is faster on printed text (p50 6.8 s vs 10.5 s).
- Sol latency on handwriting: p50 21 s, p90 36 s per photo.
- **Main risk: substituting a known drug.** In about 5 of 160 handwritten items sol returned a different existing drug instead of an illegible name (Januvia instead of Juzina, Myrbetriq instead of Moventig, Prothiaden, Gabapentin, Kalium FMK instead of Kodein FMK). Those are clinically dangerous errors, and the eval reports them as `extra`.
- The handwritten copies deliberately use alternative brands (Concor instead of Bisoprolol PMCS, Hypnogen instead of Zolpidem) and a different notation ("20" without units, "3x za den"). Sol transcribes them faithfully.
- Cost of the run: 108 calls, about 120k completion tokens.
