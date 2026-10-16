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

## Deploy (2026-10-16)
- **URL:** https://pillgaze-team92.azurewebsites.net (App Service for Containers, B1, one instance, always on, `rg-team92`, Sweden Central).
- **Why not Container Apps:** `Microsoft.App` is not registered in the `rakaton` subscription, and our role (Contributor on `rg-team92`) cannot register it.
- **Image:** built by `az acr build` into `pillgazeteam92.azurecr.io`. The tag is the commit hash.
- **Env:** goes in as app settings (encrypted at rest). Redeploy with `set -a; . ./.env; set +a; ./deploy.sh`.
- **Verified from the public URL:** `/health` 200, missing key 401, CORS preflight 204, real analyze of a box photo 200 in 8 s.

## Share and PIN (2026-10-16)
- **Storage:** PINs live only in memory (`internal/share`). The janitor drops expired entries every 15 s, and a restart or redeploy wipes everything.
- **Limits:** creating a share allows 10 per minute per IP. Claim allows 30 per minute per IP, plus a separate limit on wrong PINs: 10 per IP per 15 min and 30 globally per minute (429 with `Retry-After`).
- **Global limit trade-off:** it guards against guessing spread over many IPs, but an attacker can use it to block claims for everyone for up to a minute.
- **Active PINs:** capped at 1000. Above that, share returns 503.
- **Redeploy:** a deploy restarts the app and invalidates active PINs. Don't deploy during the demo.

## Handwriting tuning (2026-10-16, sol, handwritten half, 160 items)
| run | found | missing | strength OK | extra unflagged (excluding alternative brands) |
|---|---|---|---|---|
| baseline | 95 % | 5 % | 95 % | several real drugs instead of illegible ones (Januvia, Prothiaden, Paroxetin…) |
| 1: `detail: high` | 97 % | 3 % | 96 % | 0 |
| 2: + stricter prompt for handwriting | 95 % | 5 % | 96 % | 0 |
| 3: + `line` field (verbatim line first) | 95 % | 5 % | 96 % | 0, but 10 % slower and more real names invented on the messiest sheet; dropped |

- **Kept:** `detail: high` (the server now sends it by default) and the prompt from run 2.
- **Effect:** a name the model can't read is now written out letter by letter ("Atevhon", "WAREARiN") and flagged in `uncertain_fields`, instead of being silently replaced by a known drug. In runs 1–3, every misread or substituted item had `name` in `uncertain_fields`.
- **Remaining unflagged extras:** alternative brands on a separate line with no strength ("Gabapentin" under "Neurontin"). That is a faithful transcription; core deduplicates them.
- **Brackets:** `name` can include an alternative in brackets or after a slash, as written ("BISOPROLOL PMCS (Concor)", "Videmel /Caltrate"). Core has to count on it when matching.
- **Printed half with the final setup:** 100 % found, 100 % strengths, p50 10 s.
- **Noise:** results vary between runs by a few items, so differences of ±2 % are noise.
- **Cost of tuning:** 4 runs × 27 calls, about 410k prompt and 90k completion tokens.
