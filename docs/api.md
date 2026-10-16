# PillGaze API kontrakt (v3)

Spojení Jonášova návrhu a požadavků zadání MOÚ. **Závazný pro frontend i backend, změny jen po domluvě.** Frontend jede proti mocku (`MOCK_AI=true`), backend proti reálnému modelu, oba stejný formát.

**Dělba práce (v3, od 2026-10-16):** backend **čte fotky, páruje je s katalogem a slučuje lístky s krabičkami** (`analyze`), **předává seznam lékaři** (`share`) a **kontroluje interakce** (`interactions`). Frontend zobrazí výsledek, nechá pacienta potvrdit nejisté položky a vyřešit rozpory. Core na klientovi párování ani slučování nedělá.

## 1. Principy

- **Anonymita:** žádné jméno, rodné číslo, datum narození ani jiný identifikátor v API, v logu ani v paměti serveru.
- **Žádná databáze.** Jediný stav na serveru je PIN store **v paměti** (jedna replika). Po vypršení nebo vyzvednutí se záznam smaže.
- **Fotky se neukládají,** zpracují se v paměti a zahodí.
- **Seznam pacienta žije v telefonu** (`localStorage`). Server ho drží jen po dobu platnosti PINu.
- Všechna volání `/api/*` nesou hlavičku `X-Api-Key`. CORS je povolený jen pro `ALLOWED_ORIGIN`.
- Chyby: `{"error": {"code": "invalid_input", "message": "..."}}`.

## 2. Datový model `Medication`

```json
{
  "id": "med_01",
  "kind": "drug",
  "name": "Prestarium Neo 5 mg",
  "active_substance": "perindopril",
  "strength": "5 mg",
  "form": "tableta",
  "frequency": "1-0-0",
  "amount": "1 tableta",
  "source": "box",
  "note_style": "handwritten",
  "uncertain_fields": ["frequency"],
  "sources": ["box", "note"],
  "conflicts": [
    {"field": "strength", "values": [{"source": "box", "value": "10 mg"}, {"source": "note", "value": "5 mg"}]}
  ],
  "catalog": {
    "status": "matched",
    "product": {"id": "cat_245", "name": "PRESTARIUM NEO", "strength": "5 mg", "form": "Potahované tablety", "active_substance": "Perindopril-arginin (perindoprilum argininum)"}
  }
}
```

| Pole | Typ | Povinné | Kdo nastavuje | Poznámka |
|---|---|---|---|---|
| `id` | string | ano | backend v `analyze`, klient při ručním zadání | unikátní v rámci sdílení |
| `kind` | enum | ano | backend | `drug` \| `regimen` (zkratka onkologické léčby, např. FOLFOX). U `regimen` je `name` zkratka |
| `name` | string | ano | backend | **přesně jak je přečtený**, bez oprav překlepů |
| `active_substance` | string \| null | ne | backend: doslova z fotky, jinak z katalogu u `catalog.status = matched` | po potvrzení kandidáta ji frontend vezme z `candidates[].active_substance` |
| `strength` | string \| null | ne | backend | např. `"5 mg"` |
| `form` | string \| null | ne | backend | tableta, kapsle… |
| `frequency` | string \| null | ne | backend | volný text nebo schéma `1-0-1` |
| `amount` | string \| null | ne | backend | dávka na jedno užití |
| `source` | enum | ano | backend, frontend u ručního zadání | `note` \| `box` \| `manual`. U sloučené položky je to zdroj s nejvyšší prioritou |
| `note_style` | enum | ne | backend | `printed` \| `handwritten`, jen u položek z lístku |
| `uncertain_fields` | string[] | ano | backend (nečitelné i rozpory) | pole, která má frontend zvýraznit |
| `sources` | enum[] | ano | backend | všechny zdroje, které přispěly po sloučení |
| `conflicts` | object[] | ne | backend | pole, kde se zdroje neshodují (`strength`, `frequency`, `name`) |
| `catalog` | object | ne | backend | párování s katalogem, viz níže. Jen u `kind: drug` z `analyze` |

### `catalog`
- **`status: "matched"`:** jistá shoda. Stejná značka, přesně přečtená síla a model si názvem byl jistý. Je vyplněný `product`.
- **`status: "needs_confirmation"`:** pacient musí vybrat z `candidates` (max 3, seřazené, nejlepší první), nebo vyhledat jiný přípravek přes `GET /api/catalog`. Kandidát má navíc `score` (0–1) a `match`:
  - `name`: stejná značka, jiná nebo nepřečtená síla, případně nejistý název,
  - `prefix`: značka bez výrobce,
  - `substance`: na lístku je účinná látka,
  - `word`: jedno slovo z delšího názvu,
  - `fuzzy`: podobný zápis.
- **`status: "not_found"`:** v katalogu nic podobného není (jiná značka, nečitelné). Pacient může vyhledat ručně nebo položku nechat.
- **Nikdy tichá shoda:** nejistě přečtený název ani shoda podle látky nebo podobnosti nejsou nikdy `matched`.

Nenalezené nebo nečitelné pole je `null`, nikdy odhad.

**Pole přijímaná při sdílení** (`POST /api/share`): `id, kind, name, active_substance, strength, form, frequency, amount, source, sources, note_style`. `uncertain_fields`, `conflicts` a `catalog` se **nesdílejí**, pacient je při potvrzení vyřešil. Frontend je před sdílením odstraní.

Validace: neznámé `source`, `kind` nebo jakékoli neznámé pole → `400`. Max 50 položek (`413`), max 200 znaků na řetězec.

## 3. Endpointy

### `GET /health`
`200 ok`, bez autentizace.

### `POST /api/analyze`
`multipart/form-data`. Fotky se posílají v polích podle druhu, každé pole jde opakovat:
- `note`: lístek nebo seznam (tištěný i ručně psaný),
- `box`: krabička, blistr, lahvička,
- `regimen`: zpráva se zkratkou onkologického režimu.

Celkem max **10 fotek, dohromady 15 MB**, JPEG/PNG (kontroluje se obsah). Starý tvar (`source` + `image`) vrací `400`.

Response `200`:

```json
{
  "medications": [ /* Medication[]: spárované s katalogem a sloučené */ ],
  "unreadable_items": 0
}
```

- **Postup:** každá fotka se čte zvlášť a paralelně. Každý lék se spáruje s katalogem (`catalog`) a pak se sloučí položky, které jsou stejný lék z různých fotek:
  - stejná značka,
  - jiná značka se stejnou účinnou látkou a silou (generická záměna, rozdílný název je pak v `conflicts`),
  - stejná krabička vyfocená dvakrát.
- **Výběr polí při sloučení:**
  - název, síla, forma a účinná látka: krabička > tištěný lístek > rukopis,
  - dávkování: lístek > krabička.
- **Konflikty:** rozdíly mezi zdroji jsou v `conflicts` a v `uncertain_fields`.
- **Co se nespojuje:**
  - nejisté položky (`needs_confirmation`, `not_found`),
  - dvě různé síly stejného léku na jednom lístku, protože pacient může brát obě.
- **Doslovný přepis:** `name` zůstává tak, jak byl přečtený, překlepy se neopravují. Katalogový název je v `catalog.product.name`.
- **Chyby:**
  - `400` špatný vstup,
  - `413` moc fotek nebo moc velké,
  - `415` špatný formát,
  - `422` na fotkách nic nenalezeno, nebo nevalidní výstup modelu i po jednom opakování,
  - `429` rate limit,
  - `502` chyba modelu.

  Když selže jedna fotka, selže celý request.
- **Doba odpovědi:** fotky běží paralelně, takže rozhoduje ta nejpomalejší. Rukopis trvá 20–40 s, krabička a tištěný lístek kolem 10 s.

### `GET /api/catalog?q=prestarium`
Vyhledání v katalogu organizátorů (303 přípravků) podle značky nebo účinné látky. Slouží k výběru jiného přípravku, než nabídly `candidates`, a k ručnímu zadání lékařem.
- **Model se nevolá,** hledá se jen v paměti.
- **`q`** musí mít 2–100 znaků (jinak `400`). Rate limit je 120 za minutu na IP. Ve frontendu hledat s debounce zhruba 300 ms.
- **Response `200`:** `{ "products": [ {id, name, strength, form, active_substance, supplement?} ] }`, max 20 výsledků, nejlepší první.

### `POST /api/share`
Request: `{ "medications": [ /* Medication[] */ ] }`
Response `201`:

```json
{ "pin": "0482", "expires_at": "2026-10-16T15:25:00Z", "expires_in": 180 }
```

- PIN je **string, 4 číslice** (může začínat nulou), z `crypto/rand`, unikátní mezi aktivními.
- **Platnost 3 minuty** (konstanta `PIN_TTL` v configu).
- Chyby: `400` nevalidní body, `413` moc léků, `503` PIN store je plný.

### `POST /api/share/claim` (aplikace lékaře)
Request: `{ "pin": "0482" }`
Response `200`: `{ "medications": [ /* Medication[] */ ] }`

- **Jednorázové:** po úspěšném vyzvednutí se záznam smaže.
- `404`: neexistuje nebo vypršel (záměrně stejná odpověď).
- `429`: příliš mnoho neúspěšných pokusů (`Retry-After`).
- **Ochrana proti hádání PINu:** limit neúspěšných pokusů na IP **a globální limit**.

## 4. Mock režim pro frontend

- `MOCK_AI=true`: `/api/analyze` místo modelu použije pevné čtení pro každý druh fotky. Pak normálně páruje a slučuje, takže mock vrací sloučenou položku, konflikt síly, kandidáty i `not_found`.
- Při `MOCK_AI=true` navíc **PIN `0000` vždy vrací stejný seznam** (fixtura) i po opakovaném vyzvednutí, ať jde testovat aplikaci lékaře bez sdílení z telefonu.

## 5. Interakce a onkologické režimy (v2.1)

Zadání je chce **jen v rozhraní lékaře**. Interakce i rozklad režimů dělá backend (rozhodnuto 2026-10-16).

### `POST /api/interactions`
Request: `{ "medications": Medication[] }`. Bere tvar z `analyze` i z `claim`. Neznámé pole vrací `400`, víc než 50 položek `413`, `id` musí být vyplněné a unikátní.

Kontroluje se jen podle `active_substance`. Ta může být česky, latinsky i anglicky, včetně kombinací oddělených `+` nebo `/` a tvaru `"Amlodipin (amlodipinum)"` z katalogu. Položka bez `active_substance` se nekontroluje. U `matched` ji doplní `analyze`, u potvrzeného kandidáta ji frontend vezme z kandidáta.

Response `200`:

```json
{
  "pairs": [
    {"a": "med_02", "b": "med_01", "substance_a": "Ibuprofen", "substance_b": "Warfarin", "level": "major"}
  ],
  "checked_against": {"name": "DDInter", "url": "https://ddinter.scbdd.com", "license": "CC BY-NC-SA 4.0", "version": "download of 2026-10-16"},
  "unchecked": [
    {"id": "med_04", "name": "Atacand", "reason": "not_in_source", "substances": ["Kandesartan-cilexetil"]},
    {"id": "med_05", "name": "Neznámý lék", "reason": "no_active_substance"}
  ],
  "note": "Interakce jsou hledány jen ve zdroji DDInter… Nenalezení interakce neznamená, že interakce neexistuje."
}
```

- **`pairs`:** dvojice léků (`a`, `b` jsou `id`) s interakcí v DDInter, seřazené podle závažnosti. `level` je `major` | `moderate` | `minor` | `unknown` (`unknown` znamená, že DDInter interakci uvádí, ale bez závažnosti). Látky uvnitř jednoho kombinovaného přípravku se proti sobě nekontrolují.
- **`unchecked`:** položky, které se zkontrolovat nepodařilo. `reason` je `no_active_substance` | `not_in_source` | `partly_in_source` (kombinace, kde se našla jen část látek). V `substances` jsou ty nenalezené.
- **Prázdné `pairs` nikdy neznamená „žádné interakce“.** UI musí vždy zobrazit `checked_against`, `note` a `unchecked`.
- **Zdroj:** DDInter (Xiong et al., NAR 2022), licence CC BY-NC-SA 4.0 (jen nekomerčně, s uvedením zdroje). Pokryje asi 83 % látek z katalogu organizátorů. Chybí v něm třeba kandesartan, gliklazid, dabigatran, metamizol a nimesulid. DDInter dává jen závažnost, mechanismus ani doporučení ne.

### `POST /api/regimens/resolve`
Request: `{ "text": "FOLFOX" }`, typicky `name` položky s `kind = regimen`. Max 100 znaků, neznámé pole vrací `400`.

Hledá se **jen přesná zkratka**. Velikost písmen, mezery a interpunkce se ignorují (`R-CHOP` = `rchop` = `R CHOP`), známé varianty jsou v tabulce (`mFOLFOX6`, `XELOX`, `CyBorD` …). Podobné zkratky se nehádají: `FOLFOXX` vrací `404`.

Response `200`:

```json
{
  "query": "EC",
  "regimens": [
    {"name": "EC", "indication": "karcinom prsu", "drugs": ["Epirubicin", "Cyclophosphamide"]},
    {"name": "EC", "indication": "malobuněčný karcinom plic", "drugs": ["Etoposide", "Carboplatin"]}
  ],
  "source": {"name": "PillGaze tabulka režimů podle HemOnc.org", "url": "https://hemonc.org", "version": "2026-10-16"},
  "note": "Složení režimu je podle zkratky z tabulky, ne z dokumentace pacienta…"
}
```

- **Víc položek v `regimens`** znamená, že zkratka není jednoznačná (`EC`, `TC`, `AC-T`, `THP`, `7+3`). Variantu musí vybrat lékař, frontend je nesmí sloučit.
- **`drugs`** obsahuje jen protinádorová léčiva v anglickém INN, bez podpůrné léčby (mesna, antiemetika) a bez dávkování. Všechny názvy jsou v DDInter.
- **Interakce:** frontend vybranou variantu pošle do `/api/interactions` jako `active_substance` položky režimu, látky spojené `" + "` (např. `"Fluorouracil + Leucovorin + Oxaliplatin"`).
- `404 not_found`: zkratka v tabulce není. UI to má ukázat jako „režim nerozpoznán“, ne jako „bez léků“.
- **Zdroj:** ručně sestavená tabulka asi 80 běžných režimů (`internal/regimens/regimens.csv`), složení podle HemOnc.org. Web HemOnc je za Cloudflare a nejde automaticky stahovat. Úplná data jsou jako HemOncKB (CC BY subset) na Harvard Dataverse, `doi:10.7910/DVN/9CY9C6`, ke stažení po vyplnění guestbooku.

## 6. Změny oproti původnímu návrhu

| Původně | Teď | Proč |
|---|---|---|
| PIN 6 číslic | **4 číslice** | zadání: max 4místný PIN |
| platnost 60 min | **3 minuty** | zadání uvádí 3 minuty. U 4 číslic by 60 min znamenalo snadné hádání |
| `GET /api/share/:pin` | **`POST /api/share/claim`** | PIN nesmí být v URL a v logu |
| PIN opakovaně použitelný | **jednorázový** | zadání: "jednorázový PIN" |
| "DB s TTL a cleanup jobem" | **paměť serveru, žádná DB** | zadání: data nesmí zůstat na externím úložišti |
| 10 neúspěšných pokusů / IP | limit na IP **a globální** | 10 000 kombinací se dá projít přes více IP |
| `matched_with` | **`sources[]` a `conflicts[]`** | párování a slučování dělá core, lékař vidí jeden deduplikovaný seznam |
| `analyze`: `source` + `image`, jeden druh fotek | **`note`, `box`, `regimen` v jednom requestu**, max 10 fotek a 15 MB | backend vrací rovnou sloučený seznam (v3) |
| `candidates` v `analyze` | vypuštěno ve v2, **vráceno ve v3** jako `catalog.candidates` | párování převzal backend (v3) |
| `source` = `note` bez rozlišení | přibylo `note_style` | váhy zdrojů se liší pro psané a tištěné |
| — | `kind`, `form`, `uncertain_fields` | onkologické režimy, zvýraznění nejistých polí |

## 7. Otevřené body

- ~~**Formát vstupu core.**~~ Od v3 nerelevantní, párování a slučování dělá backend.
- **Aplikace lékaře je povinný výstup** zadání. Doporučení: route `/doctor` ve stejné Vite appce. Je potřeba určit vlastníka.
- Interakce i rozklad režimů dělá backend (sekce 5). Ověřit u mentora Rusinka, že patří jen do rozhraní lékaře.
