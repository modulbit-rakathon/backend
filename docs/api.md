# PillGaze API kontrakt (v2)

Spojení Jonášova návrhu a požadavků zadání MOÚ. **Závazný pro frontend i backend, změny jen po domluvě.** Frontend jede proti mocku (`MOCK_AI=true`), backend proti reálnému modelu, oba stejný formát.

**Dělba práce:** backend **jen čte fotky** (`analyze`) a **předává seznam lékaři** (`share`). Párování s katalogem, slučování zdrojů, váhy a označení rozporů dělá **core (JS) na klientovi**.

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
  ]
}
```

| Pole | Typ | Povinné | Kdo nastavuje | Poznámka |
|---|---|---|---|---|
| `id` | string | ano | backend v `analyze`, klient při ručním zadání | unikátní v rámci sdílení |
| `kind` | enum | ano | backend | `drug` \| `regimen` (zkratka onkologické léčby, např. FOLFOX). U `regimen` je `name` zkratka |
| `name` | string | ano | backend | **přesně jak je přečtený**, bez oprav překlepů |
| `active_substance` | string \| null | ne | backend jen pokud je **doslova na fotce**, jinak core z katalogu | INN |
| `strength` | string \| null | ne | backend | např. `"5 mg"` |
| `form` | string \| null | ne | backend | tableta, kapsle… |
| `frequency` | string \| null | ne | backend | volný text nebo schéma `1-0-1` |
| `amount` | string \| null | ne | backend | dávka na jedno užití |
| `source` | enum | ano | backend / core | `note` \| `box` \| `manual` |
| `note_style` | enum | ne | backend | `printed` \| `handwritten`, jen u položek z lístku. Core z něj volí váhu zdroje |
| `uncertain_fields` | string[] | ne | backend (nečitelné) i core (rozpory) | pole, která má frontend zvýraznit |
| `sources` | enum[] | ne | **core** | všechny zdroje, které přispěly po sloučení |
| `conflicts` | object[] | ne | **core** | pole, kde se zdroje neshodují |

Nenalezené nebo nečitelné pole je `null`, nikdy odhad.

**Pole přijímaná při sdílení** (`POST /api/share`): `id, kind, name, active_substance, strength, form, frequency, amount, source, sources, note_style`. `uncertain_fields` a `conflicts` se **nesdílejí**, pacient je při potvrzení vyřešil.

Validace: neznámé `source`, `kind` nebo jakékoli neznámé pole → `400`. Max 50 položek (`413`), max 200 znaků na řetězec.

## 3. Endpointy

### `GET /health`
`200 ok`, bez autentizace.

### `POST /api/analyze`
`multipart/form-data`:
- `source` = `box` \| `note` \| `regimen`
- `image` (lze opakovat, max 6 souborů, JPEG/PNG, dohromady max 10 MB)

Response `200`:

```json
{
  "medications": [ /* Medication[] přesně jak byly přečteny */ ],
  "unreadable_items": 0
}
```

- Backend **nic neopravuje ani nedohledává**. Překlep v názvu zůstane překlepem, rozpory hledá core.
- Chyby: `400` špatný vstup, `413` moc velké, `415` špatný formát, `422` na fotce nic nenalezeno, `429` rate limit, `502` chyba modelu.
- Při nevalidním výstupu modelu backend jednou zopakuje volání. Pak vrátí `422`.

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

- `MOCK_AI=true`: `/api/analyze` vrací fixní odpověď podle `source`, bez volání modelu.
- Při `MOCK_AI=true` navíc **PIN `0000` vždy vrací stejný seznam** (fixtura) i po opakovaném vyzvednutí, ať jde testovat aplikaci lékaře bez sdílení z telefonu.

## 5. Interakce a onkologické režimy (v2.1)

Zadání je chce **jen v rozhraní lékaře**. Interakce dělá backend (rozhodnuto 2026-10-16), rozklad režimů zatím nerozhodnut.

### `POST /api/interactions`
Request: `{ "medications": Medication[] }`. Bere tvar z `analyze` i z `claim`. Neznámé pole vrací `400`, víc než 50 položek `413`, `id` musí být vyplněné a unikátní.

Kontroluje se jen podle `active_substance`. Ta může být česky, latinsky i anglicky, včetně kombinací oddělených `+` nebo `/` a tvaru `"Amlodipin (amlodipinum)"` z katalogu. Položka bez `active_substance` se nekontroluje, core ji musí doplnit z katalogu.

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

### `POST /api/regimens/resolve` (zatím nerozhodnuto)
`{ "text": "FOLFOX" }` → `{ regimen, drugs[], source }`, `404` pokud režim není v tabulce.

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
| `candidates` v `analyze` | **vypuštěno** | podobné názvy hledá core v katalogu |
| `source` = `note` bez rozlišení | přibylo `note_style` | váhy zdrojů se liší pro psané a tištěné |
| — | `kind`, `form`, `uncertain_fields` | onkologické režimy, zvýraznění nejistých polí |

## 7. Otevřené body

- **Formát vstupu core.** Core (JS) musí brát `Medication[]` z `analyze`. Pokud ne, domluvit úpravu na jedné ze stran.
- **Aplikace lékaře je povinný výstup** zadání. Doporučení: route `/doctor` ve stejné Vite appce. Je potřeba určit vlastníka.
- Kdo dělá interakce a rozklad režimů (sekce 5)? Ověřit u mentora Rusinka, že patří jen do rozhraní lékaře.
