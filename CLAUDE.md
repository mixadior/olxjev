# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

---

## Build & Run

```bash
go build ./...
go test ./...
go test -run TestName ./...
TYPESAFE_API_KEY=... go run ./cmd/olxjev -q "..."
TYPESAFE_API_KEY=... go run ./cmd/olxjev -serve 127.0.0.1:8080
```

Module: `github.com/mixadior/olxjev`. Extracted on 2026-10-19 from `feodosian.com/olxsearch`
(`olxjev/`, `cmd/olxjev/`, `search_olxua/olxform/`, `search_olxua.OlxBuildUrl`, `request.Request` + `request.Local`);
the history before that lives there. olxsearch still has its own copy.

### Layout
| Path | What |
|------|------|
| `.` (package `olxjev`) | Resolver: free-text query → bfc values, gazetteer, catalog, URLs |
| `decision/` | `Decider` interface, `Batched`; adapters `typesafe/`, `subq/`, shared `internal/systemone` |
| `cmd/olxjev/` | CLI + web UI (embedded templates/assets) |
| `olxform/` | bfc composer for the OLX real-estate wizard, YAMLs embedded in `olxform/resources/` |
| `olxurl/` | `Build`: bfc values → OLX search URL (via OLX's breadcrumbs API) |
| `request/` | `Request` interface (`Q`) and `Local` (gorequest, 3 retries) |
| `testdata/golden.yaml` | Golden corpus for the live eval |

### Code formatting
- use spaces, not tabs
- use 4-space indents

---

## OlxJev

The root package `olxjev` resolves a free-text request («хочу зняти однушку квартиру в Харкові до 10к гривень в центрі»)
into the `/olxwizard` bfc values, using a decision model — [Jev](https://github.com/mattn/go-jev) (TypeSafe API)
by default, SubQ's Snap with `-backend subq` — to make the wizard's choices.
Spec: `docs/superpowers/specs/2026-10-14-olxjev-design.md`; backends: `docs/superpowers/specs/2026-10-18-olxjev-decision-backends-design.md`.

- Composer: `olxform.NewComposer()` (YAMLs embedded in `olxform/resources/`) — a copy of olxsearch's
  `search_olxua/olxform`, which olxsearch's Telegram bot uses; refresh both together (see below).
- Three Jev calls max: stage 1 = category + city (gazetteer shortlist) + region; stage 2 = every element the
  bfc order tree reaches (`choice` for single-choice and numeric fields, one `noul` per bool filter, one gate
  `noul` per multi-choice field: «does the query say anything about it», listing its options); stage 3 (only
  when a gate reaches `Thresholds.Gate`, 0.3) = one `noul` per option of those fields. Golden ×2 (2026-10-17):
  wanted fields' gates 0.85–0.99, untouched ones ≤0.13; ~9% of gates pass, stage 2+3 fell from ~8.7k to ~4.5k
  tokens per query. The gate needs the option list: with the title alone wanted fields scored 0.35–0.44.
  100-case corpus (×2): 42 of 43 wanted gates ≥0.85, the exception «свердловиною» at 0.32–0.35 (OLX's option
  label is Russian, «Скважина»); 4–5 of 30 must-stay-unset gates passed, which costs stage-3 tokens only, except
  «газ підведений» → heating individual_gas (gate 0.78, option 0.52).
  `-threshold` leaves the gate cutoff alone (it picks questions, it accepts nothing).
- `Result.Values` has the bfc `State.Values` shape. The CLI builds the URL with `olxjev.SearchURL`, which wraps
  `olxurl.Build` (a copy of olxsearch's `search_olxua.OlxBuildUrl`) and adds `?currency=` (Build omits it —
  olxsearch's bot URLs have the same gap);
  `owner_type` has no URL parameter yet and is reported as "not in url".
- City is single-choice. When OLX lists the same place twice in one region (Яремче / Яремча) and Jev gives the
  other spelling ≥0.2, it lands in `Result.CityAlternatives`; `olxjev.SearchURLs` returns the resolved city's URL
  first, then one per alternative, and the user picks.
- Place named but unresolved («в Іванівці»: no single city, no region): stage 1 also asks a `place` noul
  (the gazetteer shortlists villages for ordinary words — «тур» → Тур — so a shortlist alone proves nothing);
  when Jev says a place is named and neither city nor region is accepted, `Resolve` returns
  `ErrLocationUnresolved` with `Result.CityCandidates` and no URL is built. The web UI asks to add the region
  and offers the candidates as one-click refinements.
- Category unresolved but Jev leaning somewhere («купити оселю» — flat or house): `ErrCategoryUnresolved` carries
  `Result.CategoryCandidates` (≥0.15, top 3); the web UI offers them as buttons that append the category's name to
  the query («…, продаж квартир»), which Jev then resolves at ~1.0.
- Confusable categories (hotel stay/sale/tour, long-term vs daily rent) get structured option descriptions
  (`categoryNotes`: for / not_for / examples). Describe both sides of a pair: describing hotels alone pulled
  «тур … на море» away from tour operators.
- The category question shows Jev short paths (`jevCategoryLabel`: no «Нерухомість ›» root, commercial leaves by
  their own name, «Оренда офісів»); results keep the full label. Golden ×2 (2026-10-17): 5102 → 3415 tokens, 62/62.
  A category tree (one choice per level, even with speculative level-2 questions in stage 1) saved less, needed a
  second call for daily/commercial and dropped «готель на морі» to 0.46–0.63: a confusable pair split across
  branches must be decided at the top with less detail.
- Instructions are Ukrainian on purpose. A/B on the golden corpus (2026-10-15, English instructions, Ukrainian
  data/labels): English widened category/place margins (min right 0.88→0.93, 0.69→0.82) but lost the misspelled
  «у єсоеля» in both runs (0.46→0.25) and pushed the wrong floor-count number from 0.66 to 0.74 (cutoff 0.8).
  A hybrid (English stage 1 only) is untested.
- Option groups (`optionGroups`): a multi-choice filter whose options are too specific for a general request
  gets one extra noul. Pets: «з питомцем» names no species, so an «any pet» noul selects every «yes» species
  (OLX ORs multi-choice values); a named species («з котом») wins, and each species question says a generic
  pet is no (without that, «котик» drifted to ~0.4 and beat the group).
- A price without «від» is the maximum (`barePriceIsMax`); other number families keep Jev's bounds.
- Answers are accepted by Jev's **probability** for the chosen option, not its `confidence` (which is normalized
  by the option count: one confidence cutoff demanded 75% from 2-option questions, 51% from the 42 categories).
  `Resolver.Thresholds` has a cutoff per question type (Category, Place, Presence, Choice, Number, Multi); unset
  ones fall back to `Threshold` / `DefaultThreshold`. `NewResolver` uses `DefaultThresholds` (calibrated: Number
  0.8, Multi 0.4, the rest 0.5 — see its comment for the evidence); the CLI's `-threshold` replaces them for
  every type. Calibrate them on the golden corpus: the live `TestGolden` logs a calibration table (per type and cutoff: right answers kept : wrong ones let through), graded on Jev's raw
  answers. Cases expecting `""` (filter must stay unset) are what measure the wrong ones — keep adding them.
- Backends (`decision/`): the resolver talks to a `decision.Decider` (provider-neutral question/answer types
  whose JSON is the System One wire format); adapters `decision/typesafe` (Jev, `TYPESAFE_API_KEY`, $0.042/Mtok) and
  `decision/subq` (SubQ Decisions API, `subq-snap-preview`, `SUBQ_API_KEY`, $0.05/Mtok, docs.subq.ai 2026-10-17).
  Both use go-jev's transport; a test pins the typesafe request bytes to what go-jev sent before. SubQ takes at most
  16 questions per request, so `decision.Batched` splits stages 2/3 into concurrent requests, and `NewResolver`
  groups them by field (`questionGroup`: a field's gate and options, a number family's from/to): SubQ's answers shift
  with the questions sharing a request (sorted chunks of 16 vs 5 moved gates by up to 0.17); grouped, 4 queries ×
  stages 2+3 (~420 answers) flipped 0 accept/reject outcomes (`TestLiveBatchInvariance`). Thresholds per backend in
  `olxjev.CalibratedThresholds` (by `Decider.Name()`). SubQ golden ×2 (2026-10-18, 100 cases): category 91/90
  (8 confident daily-rent confusions, rooms vs flats and houses vs hotels at 0.82–0.93 — prompt work, not a
  cutoff), city 89/88 of 90, every other floor met; thresholds Category 0.55, Multi 0.5, the rest as Jev; ~5.1k
  input tokens, ~3 calls, 1.1 s, ≈$0.00026 per query (Jev: ~9.4k tokens, ≈$0.0004). Confirmation run with those
  thresholds: category 90/100 — exactly the 90% floor, no margin — and city 86/90 (a rejected category returns
  before the city is set).
- CLI: `TYPESAFE_API_KEY=... go run ./cmd/olxjev -q "..."` (`-json`, `-debug`, `-no-url`, `-threshold`);
  `-backend typesafe|subq` (key from that backend's env var), `-model` and `-endpoint` override its defaults
  (a custom endpoint needs no key).
- OpenCode Zen: `-via opencode` serves the typesafe backend from OpenCode Zen (`typesafe.OpenCodeURL`, the same
  System One protocol) with `jev-1.13-free` by default (`-model jev-1.13` = paid, $0.042/Mtok); `OPENCODE_API_KEY`
  is optional (the free model answers without one) and `TYPESAFE_API_KEY` is not used. Same Jev, so the same name
  and thresholds; with `-serve` the switch's typesafe button goes via OpenCode too. opencode.ai/docs/zen
  (2026-10-18): free "for a limited time", no published rate limits, no data policy stated for this model — don't
  send what must not leave the company. Live eval: `OLXJEV_LIVE=1 OLXJEV_VIA=opencode`. Golden (2026-10-18,
  jev-1.13-free, $0): the same answers as paid Jev — every model miss is one of typesafe's three, category right min
  0.70, none wrong — but 3 of 100 sequential queries (~1.5 s each) failed with `429 FreeUsageLimitError` after
  go-jev's 3 retries (category 98/100, city 88/90): fine for development, not for real traffic.
- Web cache: an in-memory LRU (`cmd/olxjev/cache.go`, `-cache-size`, default 100000, 0 = off) keyed by backend and
  `normalizeQuery` (case, whitespace, apostrophes, punctuation at the ends) serves the page, `/result` and
  `/api/resolve`. It keeps answers — resolved queries and the «clarify» outcomes — never Jev/OLX failures.
  Hits log «cache hit» and show «з кешу». ~2.3 KB per entry: 100000 ≈ 230 MB. No TTL: restart after a data refresh.
  With `-web-debug` an entry also keeps the resolve's questions and raw answers (~30 KB), so the default drops to
  2000 (`debugCacheSize`; an explicit `-cache-size` wins).
- Web limits: a query over 500 characters (`maxQueryRunes`; golden's longest is 88) is a 400 on every endpoint,
  before any model call. `/api/resolve` answers 502 when the decision model or OLX failed, 200 for a result and
  for the «clarify» outcomes (`output.answered`, the same rule as the cache).
- Cost: `Result.Usage` (`decision.Usage`) sums the decision calls' reported tokens and names the model; the adapter
  prices each call by the model the response names: input tokens × its price (typesafe `Price`: $0.042/Mtok —
  docs.typesafe.ai/models, 2026-10-15 — and $0 for OpenCode's `-free` models; subq $0.05/Mtok; output free;
  update the constants if pricing changes).
  The CLI prints `cost: ≈$… (model, N calls, M input tokens)` per query, the web server logs one line per resolve
  to stdout and shows cost and model in the card. `FieldInfo.Source` is `"model"` for answered fields.
  Golden average (2026-10-17, short category labels + gate): 2.5 calls, ~9.4k input tokens, ≈$0.0004 per query
  (was ~15.3k, ≈$0.00064); the live golden log reports tokens per stage, calls and time per query.
- Web UI: `TYPESAFE_API_KEY=... go run ./cmd/olxjev -serve 127.0.0.1:8080` — stdlib `net/http`, `html/template`;
  templates and assets are embedded (`cmd/olxjev/templates/`, `cmd/olxjev/assets/`).
  `GET /?q=` renders the result server-side (shareable link, works without JS), `GET /result?q=` is the card
  fragment the page script swaps in, `POST /api/resolve {"q": "..."}` returns the `-json` output.
- Web backend switch: `-serve` builds a resolver for `-backend` (the default) and for every other backend whose key
  is set (`TYPESAFE_API_KEY`, `SUBQ_API_KEY`); `-endpoint`/`-model` are the default's, `-threshold`/`-debug` apply to
  all. With two or more the page shows «Модель: typesafe | subq» (radios of the GET form, so it works without JS);
  every endpoint takes the backend (`?backend=subq`, the API's `"backend"`), absent = the default, and an unknown or
  key-less one is a 400 saying why — never a fallback to another model. The script puts it in the address bar,
  re-runs the query when the switch changes and keeps it in the recent-queries history.
- Web debug panel: `-serve … -web-debug` adds under each result every filter of the accepted category and of the
  low-confidence ones (`Resolver.CategoryCandidates`), each with its Jev questions (instructions, options with
  probabilities), the raw Jev request for that filter alone and Jev's raw answer; a multi-choice filter shows its
  gate and, as a separate stage-3 request, its options («не згадано» when the gate said no). Exchanges are captured per request
  with `olxjev.WithTrace(ctx, …)` and cached with the answer; the candidates' filters come from
  `Resolver.Stage2Plan` (no Jev call; a test pins it to what `Resolve` sends). Exposes the prompts — never on a
  public server.
- Live accuracy eval (spends credits): `OLXJEV_LIVE=1 TYPESAFE_API_KEY=... go test -run TestGolden -v .`;
  SubQ: `OLXJEV_LIVE=1 OLXJEV_BACKEND=subq SUBQ_API_KEY=... go test -run 'TestGolden|TestLiveBatchInvariance' -v .`.
  The SubQ category score sits on its 90% floor (90–91/100), so a live SubQ `TestGolden` can fail on noise alone.

#### Refreshing the OLX form data

The wizard's categories, filters and locations (`olxform/resources/*.yml`) are generated from OLX's partner
API by `cmd/olxtoolkit` (OLX moves categories: the 2023 data built category-less URLs for daily rentals).
The toolkit stays in olxsearch (it needs olxsearch's app/request stack). Into an empty work dir, from the
olxsearch checkout:

```bash
go run ./cmd/olxtoolkit -stage fetch   -dir /tmp/olxdata   # categories, cities, filters, regions, districts
go run ./cmd/olxtoolkit -stage filters -dir /tmp/olxdata   # out_order_filters / out_data_filters / out_elements_filters
go run ./cmd/olxtoolkit -stage data    -dir /tmp/olxdata   # out_data.yml, out_data_order_tree.yml
cp /tmp/olxdata/{out_data.yml,out_elements_filters.yml,out_data_order_tree.yml} search_olxua/olxform/resources/
# then, in this repo:
cp /tmp/olxdata/{out_data.yml,out_elements_filters.yml,out_data_order_tree.yml} olxform/resources/
OLXJEV_LIVE_URLS=1 go test -run TestLiveLeafURLs -v .   # every leaf still builds a URL with its category
```

Filters follow OLX's display order, so a refresh is reproducible. Expect the `olxform` guard test
(wizard step order), catalog counts and golden corpus ids to need updating when OLX changes its tree.

