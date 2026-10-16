# PillGaze backend: plán pro Claude Code

Rakathon 2026, výzva Masarykova onkologického ústavu. Deadline: **sobota 13:00**. Tohle je plán **jen pro backend** (Go). Frontend dělá Jonáš. Párování s katalogem, slučování zdrojů, váhy a označení rozporů dělá **core (JS) na klientovi**, ne backend.

**Zdrojem pravdy pro API je `docs/api.md`.** Neměň kontrakt bez domluvy s frontendem. Před rozšířením scope se zeptej.

## 1. Co backend dělá

1. **Analyze:** z fotek (psaný nebo tištěný lístek, krabička, název onkologického režimu) vytáhne vision modelem strukturovaná data. **Věrný přepis, nic neopravuje ani nedohledává.**
2. **Share:** anonymní předání seznamu lékaři přes 4místný PIN s platností 3 minuty, jen v paměti.
3. Mock režim pro frontend, deploy a ochrana endpointů.
4. *(Nerozhodnuto)* interakce a rozklad onkologických režimů pro lékaře, pokud je nebude dělat core. Viz `docs/api.md`, sekce 5.

Co backend **nedělá**: slučování zdrojů, váhy, katalog léků, hledání účinné látky, označování rozporů.

## 2. Tvrdá pravidla ze zadání

1. **Žádné osobní údaje.** Ani ve schématu, promptu, odpovědích ani v logu. Prompt říká: "Ignoruj osobní údaje, nikdy je nevracej."
2. **Žádné úložiště dat.** Žádná databáze, žádné soubory s daty pacientů. Jediný stav je PIN store v paměti.
3. **Fotky se neukládají.** Zpracují se v paměti, pošlou do modelu a zahodí. Logy neobsahují těla požadavků, obrázky ani názvy léků.
4. **PIN:** max 4 číslice, platnost **3 minuty**, jednorázový.
5. **Nikdy "žádné interakce".** (Platí, pokud interakce dělá backend.) Prázdný výsledek znamená "nenalezeno ve zdroji X" a odpověď nese `checked_against`.
6. **Model nesmí hádat a nesmí opravovat.** Nečitelné pole je `null`. Překlep se přepíše tak, jak je.

## 3. Stack a konfigurace

- Go (aktuální stable), **jen stdlib** kde to jde (`net/http` s method patterns, `encoding/json`, `log/slog`).
- AI: Azure OpenAI přes APIM gateway, OpenAI v1 API. Tenký `net/http` klient nad `POST {AI_BASE_URL}/chat/completions`.
- Deploy: **Azure App Service for Containers** (B1, jedna instance, always on), image staví ACR. Container Apps nejdou, protože `Microsoft.App` není v subscription zaregistrovaný a naše role (Contributor na `rg-team92`) ho zaregistrovat nesmí. Skript je `deploy.sh`, veřejná URL **https://pillgaze-team92.azurewebsites.net**.

```
AI_BASE_URL=https://apim-rakathon-q7146n.azure-api.net/openai/v1
AI_API_KEY=...                  # hlavička "api-key", nikdy do repa
AI_MODEL_A=gpt-6-1-sol-dz-eu    # "model" v requestu = název deploymentu
AI_MODEL_B=gpt-6-luna-dz-eu     # zatím nepoužitý (luna na rukopis nefunguje, viz docs/notes.md)
MOCK_AI=false                   # true = /analyze vrací fixtury, PIN 0000 vrací fixní seznam
PIN_TTL=180s
APP_API_KEY=...                 # sdílený klíč v hlavičce X-Api-Key
ALLOWED_ORIGIN=...              # CORS
PORT=8080
```

Oba modely jsou reasoning modely: posíláme `max_completion_tokens` a `temperature` vynecháváme. Obrázky jdou s `detail: high`. Pozor, názvy deploymentů jsou s `-dz-eu`, bez té přípony vrací 404.

## 4. Struktura

```
/backend
  cmd/server/main.go          # routing, config, graceful shutdown
  cmd/eval/main.go            # eval modelů a promptů nad vzorky
  internal/config/
  internal/httpx/             # middleware: api-key, rate limit, CORS, limit těla, recover, slog
  internal/ai/                # klient, prompty, JSON schémata, retry
  internal/model/             # typ Medication podle docs/api.md
  internal/share/             # PIN store
  internal/fixtures/          # mock odpovědi pro MOCK_AI
  internal/analyze/           # handler POST /api/analyze (multipart jen v paměti)
/scripts
  smoke.sh                    # jeden curl na model s obrázkem a JSON schématem
  split_samples.py            # rozřízne vzorové listy na tištěnou a ručně psanou půlku
  ground_truth.py             # 50_Pacientu.docx -> data/ground_truth.json
/data
  manifest.json               # fotka -> číslo pacienta
  ground_truth.json           # seznamy fiktivních pacientů od organizátorů
  samples/, eval/             # rozřezané fotky a výsledky evalu (gitignored)
/docs
  api.md                      # kontrakt (zdroj pravdy)
  notes.md                    # nálezy: modely, eval, deploy, limity
```

## 5. Logika

### 5.1 `internal/ai`
- Funkce `Extract(ctx, source, imageBytes, model) ([]Medication, unreadable int, error)`.
- Obrázek jako `data:image/jpeg;base64,...` v `image_url`. Odpověď vynucená `response_format: json_schema` (`strict: true`).
- Prompty zvlášť pro `note` (včetně určení `printed` / `handwritten` do `note_style`), `box` a `regimen`. Pravidla:
  - přepiš přesně, co vidíš, **neopravuj překlepy**, nedoplňuj,
  - `active_substance` jen pokud je **doslova vytištěná na fotce**, jinak `null`,
  - nečitelné = `null` a název pole do `uncertain_fields`,
  - nevracej osobní údaje, spočítej `unreadable_items`.
- Backend přidělí `id` (např. `med_<random>`).
- Více fotek se zpracuje paralelně, každá zvlášť. Výsledky se spojí.
- Retry **1×** při nevalidním JSON nebo 5xx, pak `422`. Žádné smyčky (ochrana kreditů).
- ~~`DUAL_MODEL`~~ **zrušeno:** luna si u rukopisu vymýšlí (najde jen 29 % položek), takže by jako nejistá vyšla skoro každá položka.
- `MOCK_AI=true`: vrať fixture z `internal/fixtures` podle `source`, bez volání modelu. **Hotové do F0, ať frontend může začít hned.**

### 5.2 `internal/share` (PIN store)
- `map[string]entry` + `sync.Mutex`. PIN: 4 číslice z `crypto/rand`, unikátní mezi aktivními. Pokud je plno nebo kolize opakovaně, vrať `503`.
- TTL `PIN_TTL` (výchozí **3 minuty**). Janitor goroutine maže prošlé záznamy. Claim záznam hned smaže.
- Neúspěšné claimy: limit na IP **a globální limit** (4 číslice jsou jen 10 000 kombinací). Překročení vrací `429` s `Retry-After`.
- Validace při `share`: whitelist polí (`DisallowUnknownFields`), max 50 položek (`413`), max 200 znaků na řetězec.
- Při `MOCK_AI=true` vrací PIN `0000` vždy stejnou fixturu.
- Testy: expirace (injektovatelné hodiny), jednorázovost, kolize, limit neúspěšných pokusů, souběh (`go test -race`).

### 5.3 `internal/httpx`
- Middleware: `recover`, `slog` bez těl requestů a bez dat, `X-Api-Key` (konstantní čas), rate limit na IP (token bucket v paměti), limit velikosti těla (`http.MaxBytesReader`), CORS.
- Server timeouts (`ReadHeaderTimeout`, `WriteTimeout` vyšší kvůli vision callu), graceful shutdown.
- Volitelně servíruj build frontendu přes `embed` ze složky `web/dist`, aby byl jeden deployment.

### 5.4 Interakce a režimy (jen pokud je neřeší core)
Návrh je v `docs/api.md`, sekce 5. Zdrojem je ručně ověřená tabulka v `data/` (účinné látky jako klíč, každý záznam se zdrojem). Odpověď vždy nese `checked_against` a `unchecked`. NIH Drug Interaction API je ukončené, nepoužívat.

## 6. Eval modelů (`cmd/eval`)

- Vstup: 27 vzorových listů organizátorů rozřezaných na tištěnou a ručně psanou půlku (160 položek na půlku) a `data/ground_truth.json`. Krabičky v evalu zatím nejsou, chybí k nim ground truth.
- Spuštění: `go run ./cmd/eval -models gpt-6-1-sol-dz-eu -variants handwritten -detail high`, přepočet bez volání modelu přes `-rescore data/eval/<run>.json`.
- Pro každý vzorek a model (A, B) zavolá `ai.Extract` a porovná s ground truth: název (přesná shoda i po normalizaci), síla, frekvence, latence, počet chyb JSON, **počet "opravených" překlepů** (model nesmí opravovat).
- Výsledky slouží k výběru modelu a promptu. **Pošli je Matyášovi**, pro ladění vah v core.
- Smoke test obou modelů uděláš dřív (jeden curl s obrázkem a JSON schématem), ještě před kódem.

## 7. Pořadí práce

**F0, do 15:30**
- [x] Smoke test obou modelů (obrázek + JSON schema, krabička i rukopis)
- [x] Go skeleton: config, routing, middleware, `/health`
- [x] `internal/model` podle `docs/api.md`, **fixtury a `MOCK_AI=true`** (frontend se napojí hned)
- [ ] Domluvit s Matyášem, jestli jeho core bere `Medication[]` z `analyze` (čeká na odpověď v [core#1](https://github.com/modulbit-rakathon/core/issues/1))
- [x] Získat od organizátorů vzorové seznamy a fotky krabiček

**F1, do 18:00**
- [x] `ai.Extract` na reálném modelu (JSON schéma, věrný přepis, retry 1×, jen paměť)
- [x] `POST /api/analyze` včetně paralelního zpracování více fotek
- [x] **Hello world deploy** (místo Container Apps na App Service, jedna instance), ověřeno z veřejné URL
- [x] `cmd/eval` a první výsledky (sol pro analyze; luna na rukopis nefunguje)

**F2, do 21:00**
- [x] `internal/share` + `POST /api/share` a `/claim` + testy s `-race`
- [x] Rate limity a globální limit neúspěšných PIN pokusů
- [ ] První end-to-end proti reálnému frontendu na nasazené URL (issues pro frontend: [#4](https://github.com/modulbit-rakathon/frontend/issues/4), [#5](https://github.com/modulbit-rakathon/frontend/issues/5), [#6](https://github.com/modulbit-rakathon/frontend/issues/6))
- [ ] Test PIN přenosu na dvou zařízeních

**F3, do 01:00**
- [x] ~~`DUAL_MODEL` jako signál nejistoty~~ zrušeno, viz 5.1
- [x] Ladění promptů podle evalu (`detail: high` a přísnější prompt pro rukopis: misread názvy teď mají vždy příznak v `uncertain_fields`)
- [ ] Interakce a režimy, **pokud** je nedělá core (po rozhodnutí)

**01:00-06:00:** spánek na směny.

**F4, 06:00-09:30**
- [ ] Ladění promptů na reálných vzorcích (kolega píše vlastní lístek; potřebujeme k němu i správný seznam)
- [ ] Krabičky: česko-slovenský text se slepuje do `active_substance` a `form`
- [ ] Kontrola, že logy ani odpovědi neobsahují osobní údaje nebo názvy léků
- [ ] Kontrola, že po restartu a po expiraci nezůstává nic uloženého

**09:30 feature freeze.** Dál jen bugfixy.

**09:30-12:00:** finální deploy (pozor: každý deploy restartuje appku a smaže aktivní PINy), záložní konfigurace (`MOCK_AI=true` jako nouzová varianta demo), zkouška PIN přenosu.

## 8. Co NEDĚLAT

- Žádná databáze, žádné soubory s daty, žádné účty ani login.
- Žádné slučování zdrojů, katalog ani váhy v backendu (je to core).
- Žádné opravování překlepů ani dohledávání účinné látky modelem.
- Žádné další Azure služby (Cosmos, AI Search, Functions, fronta).
- Žádné logování těl requestů, obrázků nebo názvů léků.
- Klíč modelu do repa nebo do odpovědí.
- Žádné smyčky nad modelem (jeden retry, strop).

## 9. Definition of done

1. `/api/analyze` věrně přečte vzorové krabičky a lístky (tištěné spolehlivě, psané co nejlépe), překlepy nechává a nečitelné vrací jako `null`.
2. Výstup `analyze` projde core (JS) bez úprav formátu.
3. PIN: platí 3 minuty, jednorázový, po restartu a expiraci nic nezůstává, brute force je zablokovaný.
4. Běží na veřejné URL na jedné replice. Existuje `docs/api.md` a `MOCK_AI` jako záloha.
5. `go vet ./... && go test -race ./...` projde.

## 10. Způsob práce

- Malé commity, každý úkol zvlášť. Před commitem `go vet`, `go test -race`.
- Testy povinně pro `share`, ostatní stačí smoke.
- Při pochybnosti o zadání se zeptej. Otevřené otázky pro mentora Rusinka:
  - Je rozklad režimu a kontrola interakcí jen v rozhraní lékaře?
  - Jaký formát očekává pro vložení do dokumentace?
  - Jaké nástroje na interakce doporučuje?
