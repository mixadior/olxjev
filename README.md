# olxjev

Turn a free-text real-estate request into an [OLX.ua](https://www.olx.ua) search URL.

```
«хочу зняти однушку квартиру в Харкові до 10к гривень в центрі»
        ↓
https://www.olx.ua/uk/nedvizhimost/kvartiry/dolgosrochnaya-arenda-kvartir/kharkov/?currency=UAH&search[filter_enum_number_of_rooms_string][0]=odnokomnatnye&search[filter_float_price:to]=10000
```

> **This is a study project.** I wrote it to learn how far small "decision" models (typed judgments
> with probabilities, instead of free-form LLM text) can go on a real, messy task: everyday
> phrasing, spelling variants, misspellings, near-duplicate place names, 42 categories and their filters. It is not
> affiliated with OLX, TypeSafe or SubQ, it has no SLA, and OLX can change its category tree or URLs
> at any time.

## Live demo

**[olxjev.ggg.org.ua](https://olxjev.ggg.org.ua)**: type a request in Ukrainian and get the OLX
search link.

![olxjev web UI: a free-text request turns into an OLX search link](https://chiprocky.eu-central-1.linodeobjects.com/chiprocky/Recording%202026-10-20%20at%2022-21-10.gif)

> [!NOTE]
> The demo runs Jev through [OpenCode Zen's free tier](https://opencode.ai/docs/zen) (`-via opencode`,
> `jev-1.13-free`). The free tier is rate-limited, and the limits aren't published. When many people use the
> demo at once, a query can fail with `429 FreeUsageLimitError`; wait a little and try again. Answers that
> were already resolved come from the cache and don't count against the limit. For steady use, run your own
> copy with a paid key (see [Usage](#usage)). OpenCode doesn't state a data policy for the free model, so
> don't type anything private.

## How it works

OLX's real-estate search is a wizard: pick a category, a city, then the filters that category has. olxjev
walks that wizard (the form data is generated from OLX's partner API and embedded in the binary) and
asks a decision model to make each choice. A decision model doesn't generate text. It gets a question
with a fixed list of options and returns a probability for each one. The resolver accepts an answer only
when its probability clears a cutoff for that question type.

A query takes at most three model calls:

1. **Category, city and region.** A local gazetteer shortlists the cities the query might name, and the
   model picks among them. If the query names a place that can't be resolved («в Іванівці»: many
   villages, no region), the resolver asks for the region instead of guessing.
2. **Every filter the category has.** One question per single-choice or numeric field, one yes/no per
   boolean filter, and one *gate* per multi-choice field: "does the query say anything about this at all?"
3. **Options of the gated fields only.** About 9% of gates pass, which halves the tokens compared with
   asking every option.

The result is the wizard's state. `olxurl` turns it into the OLX search URL, using OLX's breadcrumbs API
for the path.

Design notes, with the measurements behind each decision, are in
[`docs/superpowers/specs/`](docs/superpowers/specs/).

## Usage

You need Go 1.27+ (or Docker) and an API key for one of the decision backends, or none at all if you
use [OpenCode Zen's free Jev](#models-comparison).

### CLI

```bash
go install github.com/mixadior/olxjev/cmd/olxjev@latest

export TYPESAFE_API_KEY=...
olxjev -q "зняти однушку в Харкові до 10к"
```

```
query: зняти однушку в Харкові до 10к
  category = category_1760  [Нерухомість › Квартири › Довгострокова оренда квартир, 1.00]
  city = 280  [Харків, Харківська область, 1.00]
  currency = UAH  [грн., 0.59]
  filter_enum_number_of_rooms_string = odnokomnatnye  [1 кімната, 0.94]
  filter_float_price:to = 10000  [Харкові до 10к, 0.99]
  region = 8  [Харківська область, 1.00]
  cost: ≈$0.000000 (jev-1.13-free, 3 calls, 10765 input tokens)
  skipped: district, filter_float_price:from, filter_bool_commission, …
  url: https://www.olx.ua/uk/nedvizhimost/kvartiry/dolgosrochnaya-arenda-kvartir/kharkov/?currency=UAH&…
```

Each line shows the value, its label and the model's probability. `skipped` lists the filters the query
didn't mention.

More ways to run it:

```bash
# No key: Jev through OpenCode Zen's free model (rate-limited, see below)
olxjev -via opencode -q "купити будинок в Ірпені до 100 тис доларів"

# SubQ instead of TypeSafe
SUBQ_API_KEY=... olxjev -backend subq -q "..."

# Many queries, one per line, as JSON lines
olxjev -json < queries.txt

# See every question, option and raw answer
olxjev -debug -q "..."
```

| Flag | Meaning |
|------|---------|
| `-q` | Query text. Without it, queries are read from stdin, one per line. |
| `-backend` | `typesafe` (Jev, default) or `subq` (SubQ Snap). |
| `-via` | `opencode` serves the typesafe backend from OpenCode Zen (`jev-1.13-free` by default). |
| `-model`, `-endpoint` | Override the backend's model and endpoint. A custom endpoint needs no key. |
| `-threshold` | One acceptance cutoff for every question type, instead of the calibrated per-type ones. |
| `-json` | JSON lines output: values, labels, probabilities, URLs, cost. |
| `-no-url` | Don't call OLX. Print the resolved values only. |
| `-debug` | Log the state, questions and raw answers (debug level). |
| `-log-format` | Logs on stderr: `auto` (console on a terminal, JSON lines otherwise), `console` or `json`. |
| `-serve` | Run the web UI on this address, e.g. `127.0.0.1:8080`. |
| `-cache-size` | With `-serve`: LRU cache size in answered queries (default 100000, about 230 MB; 0 turns it off). |
| `-web-debug` | With `-serve`: show every question and raw model exchange under each result. **Exposes the prompts. Don't use it on a public server.** |

| Environment variable | Backend |
|----------------------|---------|
| `TYPESAFE_API_KEY` | `typesafe` (TypeSafe's API) |
| `SUBQ_API_KEY` | `subq` |
| `OPENCODE_API_KEY` | `-via opencode` (optional: the free model answers without one) |
| `OLXJEV_SECRET` | With `-serve`: the key page tokens are signed with (at least 16 characters, see below). Random per process when unset. |

### Web UI and API

```bash
TYPESAFE_API_KEY=... olxjev -serve 127.0.0.1:8080
```

- `GET /?q=...` renders the result on the server: the link is shareable and works without JavaScript.
- `GET /result?q=...` (the card the page's script swaps in) and `POST /api/resolve` with
  `{"q": "...", "backend": "subq"}` (the same shape as `-json`; `backend` is optional) serve the web page
  only, see below. `/api/resolve` answers 200 for a result or a "please clarify" outcome, 400 for a bad
  request (an unknown backend, or a query over 500 characters), and 502 when the model or OLX failed.
- When more than one backend has a key set, the page shows a switch between them. An unknown backend, or
  one without a key, is an error; olxjev never quietly falls back to another model.

**Page-only endpoints.** Every model call costs credits, so `/result` and `/api/resolve` answer only the
page's own script. The page carries a signed token that expires after 12 hours, and the script sends it
back in the `X-OlxJev-Token` header. A request without a valid token, or a browser request from another
site, gets a 403. When a token expires, the script reloads the result as a page, which brings a new one.
Set `OLXJEV_SECRET` to the same value on every replica behind one address. If it's unset, each process
picks a random key, and tokens stop working when it restarts. This keeps other sites and casual scripts
off your credits, but it isn't authentication: a script that loads the page first can read the token. On a
public server, also put a rate limit in front. For scripted use, run the CLI with `-json`.

When the query is ambiguous, the page asks for more instead of building a wrong URL. A place it can't
resolve gets a "which region?" prompt with candidate cities. When the category is unclear («купити
оселю»: a flat or a house?), the page offers the likely categories as buttons.

### Docker

The image is a multi-stage build: a static binary on `distroless/static` (about 25 MB), running as a
non-root user. Templates, assets and OLX form data are embedded in the binary.

```bash
docker build -t olxjev .

# Web UI on http://localhost:8080
docker run --rm -p 8080:8080 -e TYPESAFE_API_KEY olxjev

# Free Jev through OpenCode, no key needed
docker run --rm -p 8080:8080 olxjev -via opencode -serve 0.0.0.0:8080

# One query from the CLI
docker run --rm -e TYPESAFE_API_KEY olxjev -q "зняти двушку у Львові до 15000 грн"
```

Without a terminal, the container logs JSON lines. Pass `-e OLXJEV_SECRET` when you run several
replicas. The cache lives in memory and has no TTL. Restart the container after you refresh the OLX data.

### Taskfile

With [Task](https://taskfile.dev) installed, `task --list` shows the shortcuts. API keys come from the
environment, and `FLAGS` passes extra olxjev flags.

```bash
task build                                    # ./bin/olxjev
task test                                     # go vet + offline tests
task run Q="зняти однушку в Харкові до 10к"   # one query with the local binary
task run Q="..." FLAGS="-via opencode"        # free Jev, no key
task serve ADDR=127.0.0.1:8080                # web UI with the local binary

task docker:build                             # olxjev:dev (IMAGE=…, TAG=…)
task docker:run Q="..."                       # one query in the container
task docker:serve PORT=8080                   # web UI on http://localhost:8080
```

### As a library

```go
composer, _ := olxform.NewComposer()
decider := typesafe.New(os.Getenv("TYPESAFE_API_KEY"))
resolver, _ := olxjev.NewResolver(composer, decider)

res, err := resolver.Resolve(ctx, "куплю квартиру в Одесі до 50000$")
urls, notInURL, err := olxjev.SearchURLs(ctx, res, request.NewLocalRequest())
```

`Resolve` returns `ErrLocationUnresolved` or `ErrCategoryUnresolved` together with a partial result
whose candidates you can offer to the user. See [`cmd/olxjev/web.go`](cmd/olxjev/web.go).

## Models comparison

Both backends speak the same System One protocol (typed questions in, probabilities out), so they plug in
behind one `decision.Decider` interface. The numbers come from the golden corpus
([`testdata/golden.yaml`](testdata/golden.yaml): 100 real-world-style queries, 90 of them name a city), run
twice per backend, October 2026.

| | TypeSafe Jev | Jev via OpenCode Zen | SubQ Snap |
|---|---|---|---|
| Flag | `-backend typesafe` (default) | `-via opencode` | `-backend subq` |
| Model | `jev-latest` | `jev-1.13-free` | `subq-snap-preview` |
| Price (input; output is free) | $0.042 / 1M tokens | $0 | $0.05 / 1M tokens |
| Input tokens per query | ~9.4k | ~10k | ~5.1k |
| Calls per query | ~2.5 | ~2.5 | ~3 (batched, see below) |
| Cost per query | ≈ $0.0004 | $0 | ≈ $0.00026 |
| Latency per query | — | ~1.5 s | ~1.1 s |
| Category accuracy | same answers as OpenCode ↓ | 98 / 100, no confident wrong pick (right ones ≥ 0.70) | 90–91 / 100 |
| City accuracy | same answers as OpenCode ↓ | 88 / 90 | 86–89 / 90 |
| Limits | | 3 of 100 queries hit `429 FreeUsageLimitError` | ≤ 16 questions per request |

What the comparison taught me:

- **Jev is more accurate. SubQ is cheaper and faster.** SubQ uses about half the tokens per query. Its
  category misses are confident ones (0.82–0.93): daily rent of rooms vs flats, houses vs hotels. No
  cutoff can separate a confident wrong answer, so fixing these takes prompt work, not tuning.
- **Accept answers by probability, not by "confidence".** A model's confidence score is normalized by the
  number of options, so a single cutoff asked for 75% on a yes/no question and 51% on the 42-way category
  question. Each question type gets its own probability cutoff instead, calibrated per backend on the
  golden corpus (`olxjev.CalibratedThresholds`).
- **Batching changes answers unless you group the questions.** SubQ takes at most 16 questions per
  request. Splitting a stage into sorted chunks moved some answers by up to 0.17. When a field's questions
  stay together in one request (a gate with its options, a price's from/to), about 420 answers flipped no
  accept/reject outcome (`TestLiveBatchInvariance`).
- **Ukrainian instructions beat English ones** for Ukrainian queries. English widened the category
  margins, but it lost a misspelled query and let a wrong number through.
- **The free OpenCode model is fine for development** but not for real traffic: no published rate limits,
  and no data policy stated for it.

Prices and limits are as published by the providers in October 2026. Check them before you rely on them.

## Development

```bash
go build ./...
go test ./...            # offline: a fake decision model, no keys or network needed
```

The live evaluation calls the real models and spends credits (one golden run is roughly $0.04 on Jev):

```bash
OLXJEV_LIVE=1 TYPESAFE_API_KEY=... go test -run TestGolden -v .
OLXJEV_LIVE=1 OLXJEV_VIA=opencode go test -run TestGolden -v .
OLXJEV_LIVE=1 OLXJEV_BACKEND=subq SUBQ_API_KEY=... go test -run 'TestGolden|TestLiveBatchInvariance' -v .
```

`TestGolden` fails when category or city accuracy drops below 90%. It also logs a calibration table:
right answers kept vs wrong answers let through, per question type and cutoff. Cases that expect a filter
to stay unset are what measure the wrong answers, so new ones are the most useful contribution to the
corpus.

### Layout

| Path | What |
|------|------|
| `.` (package `olxjev`) | Resolver: query → wizard values, gazetteer, catalog, URLs |
| `decision/` | `Decider` interface, batching; adapters `typesafe/` and `subq/` |
| `cmd/olxjev/` | CLI and web UI (embedded templates and assets) |
| `olxform/` | OLX wizard composer; generated form data in `olxform/resources/` |
| `olxurl/` | Wizard values → OLX search URL |
| `request/` | HTTP client (`net/http`) with retries |
| `testdata/golden.yaml` | Golden corpus for the live eval |

### OLX form data

The categories, filters and locations in `olxform/resources/*.yml` are a snapshot of OLX's partner API
(October 2026), generated by a toolkit that lives outside this repository. When OLX moves a category, URLs
built from the old snapshot break. `OLXJEV_LIVE_URLS=1 go test -run TestLiveLeafURLs -v .` checks that
every category still builds a URL.

## Acknowledgements

- [go-jev](https://github.com/mattn/go-jev) by Yasuhiro Matsumoto: the System One client both backends use.
- [TypeSafe](https://typesafe.ai) (Jev), [SubQ](https://subq.ai) (Snap) and [OpenCode Zen](https://opencode.ai/docs/zen).
- OLX is a trademark of its owner. This project only builds links to its public search pages.
