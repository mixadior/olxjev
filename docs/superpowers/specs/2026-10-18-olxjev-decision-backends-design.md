# OlxJev — switchable decision backends (TypeSafe Jev, SubQ Snap)

Date: 2026-10-18
Status: approved design, pending implementation plan
Builds on: `2026-10-14-olxjev-design.md`

## Amendment (2026-10-18, during implementation, approved by the user)

The live batch-invariance check failed with sorted-key chunks: SubQ's
answers depend on the questions sharing a request (stage-2 gates moved by up
to 0.17 between chunks of 16 and of 5; repeat noise ≤0.011 for most). What
shipped instead of the "Batched semantics" and "Live 1" below:

- `decision.Batched` returns a `decision.Groupable`; `WithGroups(group)` packs
  whole groups (sorted group names, sorted keys inside) into each request and
  splits a group larger than the limit on its own. Without groups, every key is
  its own group, which is the original sorted-key behaviour.
- `olxjev.NewResolver` wraps a Groupable backend with `questionGroup`: a
  field's gate and options (`gate:X`, `X#option` → `X`) and a number family's
  bounds (`X:from`, `X:to` → `X`) share a request. Thresholds are still
  looked up by `Name()` (the wrapper delegates it). A `Resolver` built as a
  literal gets neither: use `NewResolver`.
- The live check judges **accept/reject outcome flips** at the cutoffs
  (Gate, Multi, Choice, Number) instead of raw probability Δ: it answers each
  stage-2 and stage-3 set grouped in requests of 16, again (repeat), and in
  requests of 5, and fails when other neighbours flip more outcomes than a
  repeat does. Measured: 4 queries × stages 2+3, ~420 answers, 0 flips either
  way; max Δ still 0.148, equal for repeat and other neighbours (stage-3 noise).
- A response without an `answers` object is an error (`systemone`), so a
  batched stage fails like a single call instead of leaving every question
  silently unset.
- CLI cost line as shipped: `cost: ≈$0.000412 (subq-snap-preview, 3 calls, 9412 input tokens)`.
- SubQ calibration (golden ×2 + confirmation, see `CalibratedThresholds`):
  Category 0.55, Multi 0.5, the rest as Jev. The confirmation run scored
  category 90/100, exactly the 90% floor.

## Goal

Let olxjev make its wizard decisions with either TypeSafe's Jev (today's
only backend) or SubQ's Decisions API (`subq-snap-preview`), chosen per run
by a flag, behind a provider-neutral interface owned by olxjev. Jev stays the
default; SubQ gets its own thresholds, calibrated on the golden corpus.

## Background: the SubQ Decisions API

Source: docs.subq.ai (`/endpoints/decisions-api/`, `/using-the-api/models/`,
`/endpoints/models-api/`), read 2026-10-17.

- `POST https://api.subq.ai/v1/systemone`, `Authorization: Bearer $SUBQ_API_KEY`,
  body `{model, state, questions}`; model `subq-snap-preview` (public preview;
  `GET /v1/models` lists the IDs a key may use).
- Question types `noul` (optional `criteria.true/false`), `choice` (2–255
  options as an object `name → description|null`), `score` (2–10 levels).
  Answers: `noul` (P(yes)); `choice`, `probabilities`, `confidence`.
  `usage.input_tokens`, `output_tokens` always 0.
- **1–16 questions per request**; names up to 100 characters; 384K-token
  context.
- Price: $0.05 per 1M input tokens, output free (Jev: $0.042).
- Errors: `{"detail": {"error_type", "message"}}`; 400 (over limit / rejected),
  401, 402 (insufficient credits), 403 (model not allowed for the key),
  422 (invalid request), 429, 5xx / 503.

The wire protocol is the same as TypeSafe's: path, body, question and answer
shapes all match, so `go-jev`'s transport works with a different URL and model.
What does not match:

| | Jev | SubQ |
|---|---|---|
| Questions per request | unlimited | 16 |
| Price, $/Mtok input | 0.042 | 0.05 |
| Thresholds | calibrated (`DefaultThresholds`) | not yet |

Measured on the current YAMLs (2026-10-17): stage 1 sends 4 questions;
stage 2 up to **21** (flat/house sales, offices; 15–19 for most residential
leaves); stage 3 one noul per option of every passed gate (up to 121 options
for a single leaf with all gates open). Stages 2 and 3 must be batched.

## Scope

In scope:

- New package `olxjev/decision`: neutral types, `Decider` interface,
  `Batched` wrapper.
- Adapters `olxjev/decision/typesafe` and `olxjev/decision/subq`.
- `olxjev` and `cmd/olxjev` move from `jev.*` types to `decision.*`.
- `-backend` / `-model` flags, per-backend API key env vars and thresholds.
- Live batch-invariance check and 2× golden calibration on SubQ.
- CLAUDE.md "Backends" notes.

Out of scope:

- `score` questions, images (neither is used by olxjev).
- Running both backends on one query (fallback, A/B, shadow mode).
- Choosing the backend per request in the web UI (one backend per process).
- Using SubQ's chat models (`subq-preview`).

## Architecture

```
cmd/olxjev ──-backend──▶ typesafe.New(...) ─┐
                         subq.New(...)  ────┤ decision.Decider
                                            ▼
                         olxjev.Resolver (stages 1–3, unchanged logic)
                                            │ Decide(ctx, state, questions)
                         decision.Batched (subq: max 16) ──▶ go-jev transport ──▶ HTTP
```

### Package `olxjev/decision`

No `go-jev` import.

```go
type Kind string

const (
    KindNoul   Kind = "noul"
    KindChoice Kind = "choice"
)

// Option is one choice option; Desc is a string, a struct marshaled as a
// JSON object (categoryNotes) or nil (sent as null).
type Option struct {
    Name string
    Desc any
}

type Question struct {
    Kind         Kind
    Instructions string
    Options      []Option // choice only; marshaled as an object keeping order
}

type Answer struct {
    Kind          Kind
    Yes           float64 // noul: probability of yes
    Choice        string
    Probabilities map[string]float64
    Confidence    float64
    Raw           json.RawMessage // the provider's answer as received
}

// Probability is the probability of the chosen option of a question with n
// options; without probabilities it inverts confidence = (p − 1/n)/(1 − 1/n).
func (a *Answer) Probability(n int) float64

type Usage struct {
    Calls        int     `json:"calls"` // HTTP calls, after batching
    InputTokens  int     `json:"input_tokens"`
    OutputTokens int     `json:"output_tokens"`
    CostUSD      float64 `json:"cost_usd"` // input tokens × the provider's price
    Model        string  `json:"model,omitempty"`
}

func (u *Usage) Add(o Usage)

type Decision struct {
    Answers map[string]*Answer
    Usage   Usage
    Raw     []json.RawMessage // one response body per HTTP call
}

type Decider interface {
    Decide(ctx context.Context, state any, questions map[string]Question) (*Decision, error)
    // Name identifies the backend ("typesafe", "subq") and selects its
    // calibrated thresholds.
    Name() string
    // Model is the model ID the backend sends (the web debug panel shows
    // it in each raw request).
    Model() string
}

// Batched returns d when max <= 0; otherwise a Decider that sends at most
// max questions per call.
func Batched(d Decider, max int) Decider
```

`Batched` semantics:

- Questions at or under `max` pass straight through as one call.
- Over `max`: question keys are sorted and cut into consecutive chunks of at
  most `max`, so the same question set always produces the same chunks.
- Chunks run concurrently in one `errgroup` with a derived context; the first
  error cancels the rest and `Decide` returns that error, no partial
  `Decision`.
- The merged `Decision` has every chunk's answers, the summed `Usage` (`Calls`
  = number of chunks) and every chunk's `Raw`, in chunk order.
- `Name()` delegates to the wrapped Decider.

### Adapter `olxjev/decision/typesafe`

- `New(apiKey string, opts ...jev.ClientOption) decision.Decider` wraps a
  `*jev.Client` (TypeSafe URL and `jev-latest` unless opts override them);
  `Name()` is `"typesafe"`.
- Translates `decision.Question` → `jev.Question` (`Options` → `jev.Options`)
  and `jev.Response` → `decision.Decision` (`Noul` → `Yes`, `Raw` kept).
- `PricePerMInputTokens = 0.042` moves here from `olxjev/usage.go`, with
  its source comment. `Usage.Model` is the client's model.
- No batching.

### Adapter `olxjev/decision/subq`

- `DefaultURL = "https://api.subq.ai/v1/systemone"`,
  `DefaultModel = "subq-snap-preview"`, `MaxQuestions = 16`,
  `PricePerMInputTokens = 0.05` (docs.subq.ai/using-the-api/models,
  2026-10-17).
- `New(apiKey string, opts ...jev.ClientOption) decision.Decider` returns
  `decision.Batched(inner, MaxQuestions)`; `inner` uses `go-jev`'s client with
  the SubQ URL and model (overridable by opts) and shares the translation
  code with the typesafe adapter (one unexported helper package,
  `olxjev/decision/internal/systemone`).
- `Name()` is `"subq"`.
- Errors: a `*jev.APIError` whose body parses as `{"detail": …}` becomes
  `subq: HTTP <status> <error_type>: <message>`; 402 adds "insufficient
  credits". Retries stay go-jev's (429/529, exponential backoff).

### Changes in `olxjev`

- `Resolver.Jev Evaluator` → `Resolver.Decider decision.Decider`;
  `Evaluator` is removed.
- `TraceFunc` becomes
  `func(stage string, state any, questions map[string]decision.Question, d *decision.Decision)`;
  `d` is nil when the call failed. One trace per stage, with the merged
  questions and answers, whether or not it was batched.
- `FilterPlan.Questions` / `.Options` are `map[string]decision.Question`.
- `questions.go` builds `decision.Question` values; question text, option
  order and descriptions are unchanged.
- `Result.Usage` is `decision.Usage`. The JSON keys stay the same; `model` is added.
  `olxjev/usage.go` is deleted (cost comes from the adapter).
- `SourceJev = "jev"` → `SourceModel = "model"` (JSON value of
  `FieldInfo.Source` changes; no template or JS reads it).
- `CalibratedThresholds map[string]Thresholds`: `"typesafe"` →
  `DefaultThresholds` (unchanged values), `"subq"` → set by the calibration
  run. `NewResolver(composer, decider)` uses
  `CalibratedThresholds[decider.Name()]`, falling back to `DefaultThresholds`
  for an unknown name.
- Comments and log messages that say "Jev" about the decision call say "the
  model" / the backend; Jev-specific evidence (calibration history) keeps its
  name.

### Changes in `cmd/olxjev`

- `-backend typesafe|subq` (default `typesafe`). Key env: `TYPESAFE_API_KEY`
  / `SUBQ_API_KEY`; missing key (with the backend's default endpoint) or an
  unknown backend → exit 2 with a message naming the variable or the valid
  values.
- `-endpoint` default becomes empty = the backend's URL; `-model` (empty = the
  backend's default model). Both work for either backend.
- `-debug` prints each `Decision.Raw` body.
- CLI cost line: `cost: ≈$0.000412 (subq-snap-preview, 3 calls, 9412 in)`.
- Web: the card cost shows the model (`≈$0.000412 · subq-snap-preview`); the
  per-resolve log line includes the model; the debug panel says «Запит
  моделі» / «Відповідь моделі», and its badge names the backend. The product
  name «OlxJev» stays.
- The web cache key is unchanged (one backend per process).

## Error handling

- Adapter errors propagate out of `Resolve` unchanged in kind; a failed stage
  fails `Resolve` as today, and the web cache never stores it.
- A batched stage with one failed chunk fails as a whole (no partial answers
  reach the resolver).
- An answer naming an option that was not offered is still rejected by
  `acceptChoice`; a missing answer key is still "unset".

## Testing

Unit (no network, TDD):

- `decision`: `Question`/`Option` JSON (ordered options, `null` description,
  struct description); `Answer.Probability` (tests moved from
  `answerProbability`); `Usage.Add`.
- `Batched`: 21 questions → calls of 16 + 5 with sorted, stable chunks;
  merged answers, `Usage.Calls == 2`, summed tokens and cost, `Raw` in
  chunk order; one chunk error → error returned, sibling context cancelled,
  nil Decision; `len ≤ max` and `max ≤ 0` → single pass-through call.
- `typesafe`: **wire-bytes pin**: an `httptest` server receives a request body
  byte-identical to what `jev.Client.Evaluate` sends for the same questions
  (a stage-1 set with structured category notes and a stage-2 set);
  translation of noul/choice answers; cost at $0.042; `Usage.Model`.
- `subq`: URL, `Authorization`, model `subq-snap-preview`; 21 questions → 2
  HTTP requests of ≤16; 401/402/422 `detail` bodies → readable errors; cost
  at $0.05.
- `olxjev`: `fakeJev` → `fakeDecider` (decision types; `choice()`/`noul()`
  helpers keep their signatures); existing assertions unchanged except
  `Source == "model"`; `NewResolver` picks thresholds by `Name()` with
  fallback; the `Stage2Plan` ⇔ `Resolve` pin stays.
- `cmd/olxjev`: `-backend subq` without `SUBQ_API_KEY` → exit 2;
  `-backend nope` → exit 2; cost line and web card show the model.

Live (opt-in, spends credits; key passed via env only, never committed):

1. **Batch invariance**, `OLXJEV_LIVE=1 OLXJEV_BACKEND=subq … -run TestLiveBatchInvariance`:
   for a handful of golden queries, send the same stage-2 questions to SubQ
   in chunks of 16 and of 5; log the largest absolute probability change.
   Pass: within run-to-run noise (≤ 0.07, Jev's observed spread). Otherwise
   stop and report before calibrating.
2. **Calibration**, `OLXJEV_LIVE=1 OLXJEV_BACKEND=subq SUBQ_API_KEY=… go test -run TestGolden -v ./olxjev/`,
   two runs over the 100-case corpus. `TestGolden` builds its Decider from
   `OLXJEV_BACKEND` (default `typesafe`) and labels its calibration table
   with the backend. Set `CalibratedThresholds["subq"]` per type by the
   same rule as Jev's (keep right answers, minimize wrong ones let through,
   with the cost of a wrong answer per type: a wrong number narrows the
   search). Record the evidence in the code comment and CLAUDE.md:
   accuracy, gate behaviour, tokens/calls/time/cost per query vs Jev.
   Estimated cost ≈ 9.4k tokens × 100 × 2 × $0.05/Mtok ≈ $0.10.
3. Smoke: one CLI query and one web query with `-backend subq`.

## Documentation

- CLAUDE.md, OlxJev section: a "Backends" bullet group (flags, key env,
  prices, 16-question batching, calibrated thresholds and their evidence);
  the cost bullet names both prices.
- `2026-10-14-olxjev-design.md`: a one-line pointer to this spec.
