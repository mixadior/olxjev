# OlxJev — natural-language OLX search via Jev

Date: 2026-10-14
Status: approved design, pending implementation plan
Backends: see `2026-10-18-olxjev-decision-backends-design.md` (Jev or SubQ via `decision.Decider`).

## Goal

Turn a free-text request such as

> хочу зняти однушку квартиру в Харкові до 10к гривень в центрі

into an OLX.ua search URL, by letting [Jev](https://github.com/mattn/go-jev)
(TypeSafe's decision-only model) make the choices that the `/olxwizard`
bfc form (`controller/choices.go`) currently asks the user to make by hand.

## Scope

In scope (v1):

- New library package `olxjev/` (`feodosian.com/olxsearch/olxjev`).
- New CLI `cmd/olxjev` that prints the resolved values and the OLX URL.
- OLX.ua real-estate form only — the same bfc composer `/olxwizard` uses.
- **All** form elements reachable through the bfc `OrderTree` are resolved
  generically (no per-field code); new filters in the YAML work without
  code changes.
- Refactor: move bfc composer construction out of `cmd/bot/main.go` into
  `search_olxua` so the bot and olxjev share it.
- Golden-corpus accuracy eval against the live API (opt-in).

Out of scope (v1):

- Telegram bot integration (later project; the `Result.Values` shape is
  chosen so it can feed bfc `State` / `OlxBuildUrl` directly).
- Rst.ua / Auto.ria drivers.
- Number words («однушка» → 1 for *float* fields, «двісті тисяч»).
- Local `tensai serve` backend — v1 targets the hosted TypeSafe API only.

## Background: how the bfc form is structured

The bot builds one `botform.BotFormComposer` from three embedded YAMLs
(`cmd/bot/resources/`):

| File | Composer field | Content |
|---|---|---|
| `out_data.yml` | `Data` | Option values: category tree (20 leaves), region (25) → city (29 751 total, up to 1 918 per region) → district (≤ 23), and every enum/bool filter's values |
| `out_elements_filters.yml` | `Elements` | Element definitions: renderer type (`BotChoiceKeyboardControl`, `BotMultiChoiceKeyboardControl`, `BotTextKeyboardControl`), title, `DataPath` into `Data` |
| `out_data_order_tree.yml` | `OrderTree` | Step order, branching on already-chosen values. Node `Name` = next element; `Children` keyed by chosen value or `*` |

`cmd/bot/main.go` additionally overrides the `region`, `city`, `district`
elements and wraps the order tree under a `region` root.

`bfc.GetNext(state, key)` walks `OrderTree` using `state.Values`; this is
what decides which elements are **applicable** for a given category and
location. Example — «Довгострокова оренда квартир»
(`category_1757 → category_1760`):

```
city[=*] → currency → category_1[=category_1757] → category_1757[=category_1760]
  → filter_enum_number_of_rooms_string → filter_float_total_area:from/to
  → filter_enum_pets → filter_float_total_floors:from/to
  → filter_float_price:from/to → filter_float_floor:from/to
  → owner_type → filter_bool_commission → END
```

`district` appears only after cities that have districts (e.g. Харків,
city 280: 9 administrative districts — Шевченківський, Київський, … —
there is no «Центр» option).

`search_olxua.OlxBuildUrl(ctx, values, rq)` turns `State.Values` into the
final URL (category chain walk + one OLX breadcrumbs API call +
`filter_*` query params).

## Jev in brief

`github.com/mattn/go-jev` (v0.0.3, go 1.27.0):

- `jev.NewClient(WithAPIKey, WithTimeout, WithMaxRetries, …)`; default
  endpoint `https://api.typesafe.ai/v1/systemone`. The package reads no env
  vars.
- `Client.Evaluate(ctx, state, map[string]jev.Question) (*jev.Response, error)`
  — several questions in one request.
- `Question{Type: "choice", Instructions, Criteria: jev.Options{{Name, Desc}}}`
  → `Answer{Choice, Probabilities, Confidence}`.
- `Question{Type: "noul", Instructions}` → `Answer{Noul}` — yes-probability
  in [0, 1]; used for per-option decisions on multi-choice elements.
- Jev cannot extract free values. `score` returns a weighted position on an
  ordered scale, not a pick, so it is **not** used; numbers are handled as
  `choice` over extracted candidates.

## Architecture

### Package `olxjev/`

| File | Responsibility | Depends on |
|---|---|---|
| `numbers.go` | `ExtractNumbers(text) []NumberMention` — «10к», «10 000», «10тис», «1,5 млн», «60м²»; normalized `Value`, `Raw` snippet (±2 words context), position | — |
| `gazetteer.go` | Built once from `Data` region→city; `Shortlist(text, n) []CityCandidate`: UA/RU letters folded to one alphabet (і/ї/ы→и, є/э→е, ь and apostrophes dropped), each city word compared by edit distance to token prefixes around its length (budget 0–3 by word length), query stop-words ignored; ranked cities-with-districts first, then distance relative to name length, then longer names. Verified on real data: «Харкові», «Харькове», «Киеве», «Днепре», «Николаеве», «Запорожье», «Кам'янському», «Білій Церкві» | bfc `Data` |
| `catalog.go` | Category leaves flattened into path labels («Нерухомість › Квартири › Довгострокова оренда квартир») plus the `category_*` key chain to set in state | bfc `Data` |
| `questions.go` | Element → `jev.Question`(s): single-choice elements → one `choice` over `GetChoicesByPath` + `none`; multi-choice elements → one `noul` per option; text elements → one `choice` over extracted numbers + `none` | bfc, go-jev |
| `resolver.go` | `Resolver.Resolve(ctx, text) (*Result, error)`: pre-pass, stage 1, order-tree walk, stage 2, acceptance and post-checks | all above |

Jev is accessed through an interface so tests use a fake:

```go
type Evaluator interface {
    Evaluate(ctx context.Context, state, questions any) (*jev.Response, error)
}
```

`*jev.Client` satisfies it as-is.

```go
type Resolver struct {
    Composer  *botform.BotFormComposer
    Jev       Evaluator
    Gazetteer *Gazetteer
    Catalog   *Catalog
    Threshold float64          // default 0.5
    Log       *zerolog.Logger  // nil-safe
}

type FieldInfo struct {
    Value      string  // raw value stored in Values
    Label      string  // human label (option Desc / title)
    Confidence float64
    Source     string  // "jev" | "gazetteer" | "derived"
}

type Result struct {
    Values  map[string]string    // bfc State.Values shape → OlxBuildUrl
    Fields  map[string]FieldInfo // per resolved element
    Skipped []string             // applicable elements left unset
}

var ErrCategoryUnresolved = errors.New("olxjev: category unresolved")
```

### Shared composer construction

Move the YAML loading and element overrides from `cmd/bot/main.go`
(currently lines ~162–179 and ~470–509) into a new sub-package:

```go
// search_olxua/olxform
func NewComposer() (*botform.BotFormComposer, error)
// Walk returns the element names the order tree visits for values.
func Walk(bfc *botform.BotFormComposer, values map[string]string) ([]string, error)
```

with the three YAMLs (~8.8 MB) embedded in `search_olxua/olxform/resources/`
(moved from `cmd/bot/resources/`). A sub-package — not `search_olxua`
itself — so the worker, scheduler and adminui binaries, which import
`search_olxua`, do not grow by the embedded YAML. The bot calls
`olxform.NewComposer()` instead of its inline code; behaviour is unchanged.

### CLI `cmd/olxjev`

- Input: `-q "<text>"`, or stdin (one query per line).
- API key: `TYPESAFE_API_KEY` env, read by the CLI and passed via
  `jev.WithAPIKey`; required unless `-endpoint` points elsewhere.
- `-endpoint` (default `jev.DefaultURL`, the TypeSafe API) exists so the
  CLI test can point at an `httptest` fake; production use is TypeSafe only.
- Flags: `-threshold` (0.5), `-json` (print `Result`), `-no-url` (skip
  `OlxBuildUrl`), `-debug` (print, per stage, the `state` sent to Jev,
  the questions and the raw answers).
- Default output: resolved fields + URL built with
  `search_olxua.OlxBuildUrl(ctx, values, request.NewLocalRequest())`.

### Dependency

Add `github.com/mattn/go-jev` to `go.mod`.

## Data flow

`Resolve(ctx, "хочу зняти однушку квартиру в Харкові до 10к гривень в центрі")`:

**0. Deterministic pre-pass (no API)**

- `ExtractNumbers` → `[{Value: 10000, Raw: "до 10к гривень"}]`
- `Shortlist(text, 8)` → candidates labelled with region
  (`"Харків, Харківська обл."`) so same-named villages are distinguishable.

**1. Stage 1 — one `Evaluate`, `state` = user text**

All three questions go in the same request; `region` is the only
speculative question (used only when `city` is unresolved) and costs no
extra call. The order tree branches on values only at `city` (district
presence) and `category_*` — every other branch is `*` — so the stage-2
question set is fully determined after stage 1: **two API calls is a hard
upper bound**, and no filters for unchosen categories are ever asked.


| Question key | Options |
|---|---|
| `category` | 20 leaves; `Name` = leaf key (`category_1760`), `Desc` = path label |
| `city` | shortlist + `none` (question omitted if shortlist is empty) |
| `region` | 25 regions + `none`; always asked, used only when `city` is unresolved |

Commit into `Values`:

- category key chain, e.g. `category_1=category_1757`,
  `category_1757=category_1760`;
- `region=<id>`, `city=<id>` (region derived from the accepted city;
  otherwise from the `region` answer).

Unresolved category → return `ErrCategoryUnresolved` (no valid URL is
possible). Unresolved city → keep region-level search. Unresolved city and
region → nationwide search, recorded in `Skipped`.

**2. Order-tree walk**

`botform.NewState(values, BfcKey{"root"})`, then repeated `GetNext`:
keys already in `Values` are walked through; every other element name is
collected until END. `search_name` is excluded (not part of the URL).

`filter_bool_*` elements offer only option `1` (e.g. «Без комісії»):
their `0` option («Усі оголошення») means the same as not filtering, and
`OlxBuildUrl` turns **any** non-empty bool value into an active filter, so
`0` must never be stored.

**3. Stage 2 — one `Evaluate`, one question per collected element**

`state` carries the query plus stage-1 results as human-readable labels
(never IDs such as `category_1760` / `280`), so Jev can interpret numbers,
district and currency in context (e.g. «до 200» means different things
for flat rent and house sale):

```go
state := map[string]any{
    "query": "хочу зняти однушку квартиру в Харкові до 10к гривень в центрі",
    "resolved": map[string]string{
        "Категорія": "Нерухомість › Квартири › Довгострокова оренда квартир",
        "Місто":     "Харків, Харківська обл.", // or "Область": "…" when city unresolved
    },
}
```

Questions within one stage are answered independently and do not see
each other's answers; cross-field consistency is enforced by the
deterministic post-checks in step 4.

- Single-choice elements (`BotChoiceKeyboardControl`: district, currency,
  `filter_bool_*`, …): one `choice` question, options =
  `GetChoicesByPath(state, el.DataPath, true)` + `none`. Instructions =
  element title + short hint, e.g. «Район міста, якщо згаданий у запиті».
- Multi-choice elements (`BotMultiChoiceKeyboardControl`, 19 in the YAML:
  `filter_enum_number_of_rooms_string`, `filter_enum_pets`,
  `filter_enum_heating`, `owner_type`, …): one `noul` question **per
  option**, key `<element>#<optionKey>`, instructions e.g.
  «Кількість кімнат: чи підходить користувачу варіант "2 кімнати"?».
  Lets «одно- або двокімнатну» select both `one` and `two`.
- Text elements (`filter_float_*:from|to`): options = extracted numbers
  (`Name` = normalized value, `Desc` = raw snippet with context) + `none`.
  Example:

  ```go
  "filter_float_price:to": jev.Question{
      Type:         "choice",
      Instructions: "Ціна до (верхня межа ціни)",
      Criteria: jev.Options{
          {Name: "10000", Desc: "«до 10к гривень»"},
          {Name: "none", Desc: "не вказано в запиті"},
      },
  }
  ```

  If no numbers were extracted, text-element questions are not sent.

**4. Acceptance and post-checks**

- `choice` answers: accept iff `Choice != "none"`, `Choice` is one of the
  sent options, and `Confidence >= Threshold`. Otherwise add to `Skipped`
  (with top option and probability for `-debug`).
- `noul` answers (multi-choice): accept every option with
  `Noul >= Threshold`; join accepted option keys with the element's
  `ValuesSeparator` (`;`), sorted by option key (bfc options are maps
  and have no inherent order). No accepted option →
  element goes to `Skipped`. `FieldInfo.Confidence` = lowest accepted
  `Noul`.
- Range: if `X:from > X:to`, keep the higher-confidence one.
- Number reuse: one extracted number may be assigned to at most one field
  family (price / area / floor / …); highest probability wins. `from` and
  `to` of the same family may share a number.
- Request size: multi-choice elements add one question per option
  (typically 2–8; `filter_enum_country` has 96 but only applies to
  property abroad). Still a single stage-2 API call.

**5. Return** `Result`; `Values` feeds `OlxBuildUrl` unchanged.

Known soft spot: «в центрі» must map to an administrative district;
Jev confidence may split between e.g. Шевченківський and Київський and
fall under threshold. The golden corpus measures this.

## Error handling

- Jev transport/API errors (`*jev.APIError`, timeout) are wrapped with the
  stage (`stage1: …`, `stage2: …`). Client configured with
  `WithMaxRetries(3)` and `WithTimeout(30 * time.Second)`.
- Stage 2 failure after stage 1 success → return partial `Result`
  (category + location) **and** the error; the CLI still prints a
  region-level URL.
- An answer whose `Choice` is not among the sent options → treated as
  `none`, logged.
- `OlxBuildUrl` failure → CLI error; `-json` still prints `Values`.
- Logging via `rs/zerolog`, nil-safe, as in `parser-go-ng`.

## Testing

Unit tests (`go test ./olxjev/...`, no network):

- `numbers_test.go` — table tests: «10к», «10 000», «10тис.», «1,5 млн»,
  «60м²», «3 поверх», ranges («від 8 до 12 тис»); years and phone-like
  digit runs excluded.
- `gazetteer_test.go` — UA/RU inflections («Харкові», «Харькове»,
  «Києві», «Львова»), apostrophe variants («Кам'янське» / «Камʼянське»),
  same-name disambiguation.
- `catalog_test.go` — 20 leaves; each leaf's key chain is walked correctly
  by `OlxBuildUrl`'s category loop.
- `questions_test.go` — question sets for flat-rent and house-rent states
  match the order-tree sequences above; multi-choice elements expand into
  one `noul` per option.
- `resolver_test.go` — fake `Evaluator`: threshold, `none`, invalid
  choice, from > to, number reuse, multi-choice `noul` aggregation
  (0, 1 and 2+ options accepted; `;`-joined in option order), partial
  result on stage-2 failure, unresolved category.
- `search_olxua` — `NewOlxFormComposer` yields the same `GetNext`
  sequences as the composer built by the current bot code (refactor guard).

Golden-corpus eval (opt-in, live API):

- `olxjev/testdata/golden.yaml` — ~40 UA/RU phrases, each with the
  expected subset of `Values` (only listed keys are asserted). Mix: rent /
  sale; flats / houses / rooms / commercial; big cities, a village, a
  region-only phrase; prices with/without currency; ranges; area; floor;
  «однушка»/«двушка»; «1–2 кімнати» (multi-choice); «без комісії»;
  «з тваринами»; «в центрі».
- `TestGolden` runs only with `OLXJEV_LIVE=1` and `TYPESAFE_API_KEY` set.
  Prints a per-field accuracy table and per-phrase diffs.
- Fails only below floors: category ≥ 90 %, city ≥ 90 %. Other fields are
  reported, not enforced.
- Phrases drafted during implementation and reviewed by the project owner.

## Success criteria

- `cmd/olxjev -q "<text>"` prints a valid OLX search URL for the corpus
  phrases.
- Golden eval: category ≥ 90 %, city ≥ 90 %; other fields reported.
- Two Jev API calls per query.
- Bot behaviour unchanged after the composer refactor.
