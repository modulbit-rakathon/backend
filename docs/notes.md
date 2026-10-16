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

## Boxes (2026-10-16, 15 photos spread evenly over `Leky_foto`, `go run ./cmd/eval -boxes ../Data/Leky_foto -n 15 -detail high`)
- **All 15 read correctly** (name, strength, active substance, form), 3–11 s per photo.
- **Fixed in the prompt:**
  - Bilingual CZ/SK packaging used to come back joined ("linagliptin/linagliptín"). Now only the Czech version comes back.
  - ® and ™ are left out of the name.
  - Combined active substances are separated with " + " ("perindoprilum erbuminum + amlodipinum + indapamidum"), so there is no clash with the "/" in strengths.
- **Handwritten dosage on a box:** when one is there ("0-1" on Stadamet), the model transcribes it and flags `frequency` as uncertain. A name on a pharmacy sticker did not reach the output.

## Interactions (2026-10-16)
- **Sources checked:**
  - **NIH RxNav:** the interaction API ended in January 2024.
  - **Drugs.com, Medscape, WebMD:** no API, and automated use is not allowed.
  - **RxLabelGuard:** FDA labels, $20/month after a 7-day trial; it covers US drugs only, so the same gaps.
  - **SÚKL SPC:** section 4.5 is authoritative, but it is unstructured text (a 2.6 GB zip of PDF/DOC) and interactions are listed against drug classes, not pairs.
- **Chosen: DDInter.** Downloadable CSVs with 235k pairs and a severity level, licence CC BY-NC-SA 4.0. Built with `scripts/ddinter.py` into `internal/interactions/ddinter.csv.gz` (1.2 MB, embedded in the binary, about 45 MB RAM).
- **Note:** the download on ddinter2.scbdd.com is the same as 1.0 (about 1,970 drugs). DDInter 2.0 online has more, but not as a download.
- **Name matching:** Czech, Latin and English INNs reduce to one key (salts, Latin and Czech endings, c/k, qu/kv, ph/f…), with exact synonyms in `aliases`.
  - Cations (calcium, magnesium…) are only dropped from the input, otherwise "magnesium lactate" matched "Calcium lactate".
  - If two DDInter names reduce to the same key, nothing is matched.
- **Coverage of the organisers' catalogue:** 214 of 258 substance mentions (83 %). Every match checked by hand.

## Catalogue matching and merging (2026-10-16, contract v3)
- **Catalogue:** the organisers' `Katalog_leciv_kompletni_309.csv` is embedded in `internal/catalog` (303 products after dropping 6 duplicates). Brand families come from the name without strength ("AGEN 5"/"AGEN 10" → `agen`).
- **`matched` only when everything holds:**
  - the same brand (or the brand without the manufacturer suffix, but never across a product word like COMBI/RETARD/NEO/PLUS/H…),
  - exactly the read strength,
  - the model was sure of the name.
- **Everything else is `needs_confirmation` with up to 3 candidates.** Candidates come from:
  - the same brand at another strength,
  - the generic substance,
  - one word of a joined name ("Tritace Piramil"),
  - fuzzy matching against brands and substances (min. similarity 0.6 or 0.75).
- **Eval on saved output, no model calls** (`go run ./cmd/eval -match data/eval/<run>.json`):

  | half | items | matched | WRONG matched | correct among candidates | not offered / not found |
  |---|---|---|---|---|---|
  | printed | 160 | 157 | 0 | 2 | 0 |
  | handwritten (3 runs) | 160 | 84–88 | 0 | 56–61 | 8–9 |

  The remaining misses on handwriting are other brands the writer used that are not in the catalogue (Concor, Hypnogen, Omnic Tocas, Glucophage XR, Nolpaza). That is correct behaviour.
- **Fixes found by the eval and tests:**
  - "Concor" was matched with certainty to CONCOR COMBI (a different product) → product words and exact strength.
  - "Zaldiar" without a strength was matched with certainty → no certainty without a read strength.
  - The magnesium/calcium lactate case in interactions → cations are only dropped from the input.
- **Merging:**
  - same brand, or the same substances and strength (generic swap with a name conflict),
  - fields: identity from the box first, dosing from the note,
  - conflicts on strength, frequency and name,
  - a brand read with certainty at a strength missing from the catalogue joins that brand's group from another source (so the strength shows as a conflict),
  - unsure items are never merged.
- **End-to-end (real model):** the handwritten half of patient 32 + the Palexia retard and Novetron boxes, 3 photos in 39 s, 10k prompt tokens.
  - Palexia merged box + note.
  - Baclofen, Veral retard and Novetron `matched`.
  - Unsure handwritten names have the correct product as the first candidate.
  - Interactions run straight off the output.

## Invented drugs on a handwritten note (2026-10-16)
- Report: a note plus six boxes (`../dataset/`) came back with about three drugs that were not there. Boxes were read correctly; the note lines were read as other, known drugs (Lagosa as Betaloc or Legalon, Kalium chloratum as Folic acid or Metformin, Keron as Ketonal), often without `uncertain_fields`, so they never merged with their boxes and showed up as extra items.
- **Main cause: `AI_MODEL_A` was `gpt-6-luna-dz-eu` in `.env` and in App Service**, the model that fails on handwriting (table above). With `gpt-6-1-sol-dz-eu` the same note is read as written ("Famosa", "Keron", flagged uncertain), 3 of 3 runs. Cropping the photo to the paper did not help luna.
- **Box hints:** notes are now read after the boxes, and the note call lists the box names read in the same request. The model fills `box_match` per line; analyze links that line to its box and `MergeLinked` joins them even when the box is not in the catalogue. The name stays as written, a differing strength is still a conflict. A line the model left unlinked joins a box when its brand words, read with certainty, start the box's ("Bisoprolol" and "Bisoprolol PMCS 10 mg") and only one free box fits.
- Result on the dataset: 6 of 6 items, each merged from box and note, 3 of 3 runs, about 25 s per request (boxes, then the note). Eval on the notes alone with sol: handwritten 95 % found, 3 % silent, 12 extra; printed 100 %, 1 extra, so no regression from the schema change.
