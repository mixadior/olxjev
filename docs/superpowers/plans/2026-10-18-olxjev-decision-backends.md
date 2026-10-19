# OlxJev Decision Backends Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** olxjev makes its wizard decisions through a provider-neutral `decision.Decider`, with TypeSafe Jev (default) and SubQ Snap (`-backend subq`) as adapters, and SubQ thresholds calibrated on the golden corpus.

**Architecture:** New package `olxjev/decision` (types with the System One wire JSON, `Decider`, `Batched`), an internal `systemone` client shared by two thin adapters (`decision/typesafe`, `decision/subq`; both reuse `go-jev`'s HTTP transport), then a mechanical migration of `olxjev` and `cmd/olxjev` from `jev.*` to `decision.*`, then the CLI and web features, then the live runs.

**Tech Stack:** Go 1.27, `github.com/mattn/go-jev` v0.0.3 (transport only), `golang.org/x/sync/errgroup`, stdlib `net/http/httptest`.

**Spec:** `docs/superpowers/specs/2026-10-18-olxjev-decision-backends-design.md`

## Global Constraints

- Code formatting: **spaces, 4-space indents, no tabs** (CLAUDE.md). Do **not** run `gofmt -w`/`go fmt` (they convert to tabs); keep the surrounding style by hand.
- Module path `feodosian.com/olxsearch`; new packages: `feodosian.com/olxsearch/olxjev/decision`, `.../olxjev/decision/typesafe`, `.../olxjev/decision/subq`, `.../olxjev/decision/internal/systemone`.
- SubQ: URL `https://api.subq.ai/v1/systemone`, model `subq-snap-preview`, `MaxQuestions = 16`, `PricePerMInputTokens = 0.05`, key env `SUBQ_API_KEY`.
- TypeSafe: URL `jev.DefaultURL`, model `jev.DefaultModel`, `PricePerMInputTokens = 0.042`, key env `TYPESAFE_API_KEY`.
- Backend names: `"typesafe"`, `"subq"`. `FieldInfo.Source` value `"jev"` becomes `"model"` (`SourceModel`).
- `Result.Usage` JSON keys stay `calls`, `input_tokens`, `output_tokens`, `cost_usd`; `model` is added (omitempty).
- Question text, option order and descriptions sent to the model must not change (the wire-bytes pin in Task 3 guards it).
- **API keys are never written to any file**, test, commit, spec or log. Pass them as env vars on the command line only.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Live tests (`OLXJEV_LIVE=1`) spend credits; run them only in Tasks 7–8.

## Review Focus

1. **Exactly 16 vs 17 questions.** At 16 SubQ must get one request; at 17, two. Pinned in Task 2 (`TestBatchedBoundary`).
2. **Context cancelled or timing out mid-batch** (the web's 60 s `resolveTimeout`). `Decide` must return the context error and no partial `Decision`. Pinned in Task 2 (`TestBatchedCancelledContext`).
3. **A non-JSON error body** (a proxy's 502 HTML page). The subq adapter must still return a readable error that includes the HTTP status and must not panic. Pinned in Task 4 (`TestSubqNonJSONError`).
4. **Instructions containing `<`, `>`, `&`, `«»` or quotes.** The bytes must match go-jev's encoding exactly, including JSON HTML-escaping. Pinned in Task 3 (`TestWireBytesMatchGoJev` uses such text).
5. **`-backend subq -endpoint http://local` without a key** (a local or test server). This must be allowed, as it is for typesafe today. Pinned in Task 6 (`TestRunSubqCustomEndpointNoKey`).

---

### Task 1: `olxjev/decision` types

**Files:**
- Create: `olxjev/decision/decision.go`
- Test: `olxjev/decision/decision_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Kind string`; `KindNoul`, `KindChoice`
  - `type Option struct{ Name string; Desc any }`
  - `type Question struct{ Kind Kind; Instructions string; Options []Option }` with `MarshalJSON` → `{"type","instructions","criteria"}`
  - `type Answer struct{ Kind Kind; Yes float64; Choice string; Probabilities map[string]float64; Confidence float64; Raw json.RawMessage }` with `UnmarshalJSON` (wire → fields, keeps `Raw`), `MarshalJSON` (returns `Raw` when set, else wire form), `(*Answer) Probability(n int) float64`
  - `type Usage struct{ Calls, InputTokens, OutputTokens int; CostUSD float64; Model string }` with `(*Usage) Add(Usage)`
  - `type Decision struct{ Answers map[string]*Answer; Usage Usage; Raw []json.RawMessage }`
  - `type Decider interface{ Decide(ctx, state any, questions map[string]Question) (*Decision, error); Name() string; Model() string }`

- [ ] **Step 1: Write the failing tests**

`olxjev/decision/decision_test.go`:

```go
package decision

import (
    "encoding/json"
    "math"
    "testing"
)

type note struct {
    For      string   `json:"for"`
    Examples []string `json:"examples"`
}

// A choice keeps its options in order, a nil description is null and a
// struct description is an object; a noul has no criteria.
func TestQuestionWireJSON(t *testing.T) {
    choice := Question{Kind: KindChoice, Instructions: "Яка категорія?", Options: []Option{
        {Name: "b", Desc: "друга"},
        {Name: "a", Desc: note{For: "готель", Examples: []string{"на морі"}}},
        {Name: "none", Desc: nil},
    }}
    got, err := json.Marshal(choice)
    if err != nil {
        t.Fatal(err)
    }
    want := `{"type":"choice","instructions":"Яка категорія?","criteria":{"b":"друга","a":{"for":"готель","examples":["на морі"]},"none":null}}`
    if string(got) != want {
        t.Errorf("choice:\n got %s\nwant %s", got, want)
    }
    got, _ = json.Marshal(Question{Kind: KindNoul, Instructions: "Так?"})
    if want := `{"type":"noul","instructions":"Так?"}`; string(got) != want {
        t.Errorf("noul: got %s, want %s", got, want)
    }
}

func TestAnswerUnmarshalKeepsRaw(t *testing.T) {
    raw := `{"type":"choice","choice":"a","probabilities":{"a":0.8,"b":0.2},"confidence":0.6}`
    var a Answer
    if err := json.Unmarshal([]byte(raw), &a); err != nil {
        t.Fatal(err)
    }
    if a.Kind != KindChoice || a.Choice != "a" || a.Probabilities["a"] != 0.8 || a.Confidence != 0.6 || string(a.Raw) != raw {
        t.Errorf("choice answer = %+v", a)
    }
    var n Answer
    if err := json.Unmarshal([]byte(`{"type":"noul","noul":0.92}`), &n); err != nil {
        t.Fatal(err)
    }
    if n.Kind != KindNoul || n.Yes != 0.92 {
        t.Errorf("noul answer = %+v", n)
    }
}

// MarshalJSON returns the provider's answer when there is one, the wire
// form otherwise (fakes in tests).
func TestAnswerMarshal(t *testing.T) {
    raw := json.RawMessage(`{"type":"noul","noul":0.5,"extra":1}`)
    got, _ := json.Marshal(&Answer{Kind: KindNoul, Yes: 0.1, Raw: raw})
    if string(got) != string(raw) {
        t.Errorf("with Raw: got %s", got)
    }
    got, _ = json.Marshal(&Answer{Kind: KindNoul, Yes: 0.25})
    if want := `{"type":"noul","noul":0.25}`; string(got) != want {
        t.Errorf("noul: got %s, want %s", got, want)
    }
    got, _ = json.Marshal(&Answer{Kind: KindChoice, Choice: "x", Confidence: 0.9})
    if want := `{"type":"choice","choice":"x","confidence":0.9}`; string(got) != want {
        t.Errorf("choice: got %s, want %s", got, want)
    }
}

// Probability is the chosen option's probability; without probabilities it
// inverts confidence = (p − 1/n)/(1 − 1/n).
func TestAnswerProbability(t *testing.T) {
    a := &Answer{Kind: KindChoice, Choice: "x", Probabilities: map[string]float64{"x": 0.43}, Confidence: 0.99}
    if p := a.Probability(42); p != 0.43 {
        t.Errorf("with probabilities: %v, want 0.43", p)
    }
    for _, c := range []struct {
        conf float64
        n    int
        want float64
    }{{0.5, 2, 0.75}, {0, 4, 0.25}, {1, 42, 1}, {0.7, 1, 0.7}, {0.7, 0, 0.7}} {
        a := &Answer{Kind: KindChoice, Choice: "x", Confidence: c.conf}
        if p := a.Probability(c.n); math.Abs(p-c.want) > 1e-9 {
            t.Errorf("confidence %v, n %d: %v, want %v", c.conf, c.n, p, c.want)
        }
    }
}

func TestUsageAdd(t *testing.T) {
    u := Usage{Calls: 1, InputTokens: 100, OutputTokens: 1, CostUSD: 0.001, Model: "m1"}
    u.Add(Usage{Calls: 2, InputTokens: 50, CostUSD: 0.0005, Model: "m2"})
    u.Add(Usage{Calls: 1})
    if u.Calls != 4 || u.InputTokens != 150 || u.OutputTokens != 1 || math.Abs(u.CostUSD-0.0015) > 1e-12 || u.Model != "m2" {
        t.Errorf("Usage = %+v", u)
    }
    got, _ := json.Marshal(Usage{Calls: 1})
    if want := `{"calls":1,"input_tokens":0,"output_tokens":0,"cost_usd":0}`; string(got) != want {
        t.Errorf("JSON %s, want %s", got, want)
    }
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./olxjev/decision/`
Expected: FAIL (build error, package has no non-test files / undefined: Question).

- [ ] **Step 3: Implement**

`olxjev/decision/decision.go`:

```go
// Package decision is olxjev's provider-neutral view of a System One
// decision model: typed questions about a state in, probabilities out.
// Adapters (decision/typesafe for TypeSafe's Jev, decision/subq for SubQ's
// Snap) talk to the providers; the JSON forms here are the System One wire
// format both of them accept.
package decision

import (
    "bytes"
    "context"
    "encoding/json"
)

// Kind is a question type.
type Kind string

const (
    KindNoul   Kind = "noul"   // yes/no: the answer is P(yes)
    KindChoice Kind = "choice" // one of named options
)

// Option is one choice option. Desc is a string, a struct (sent as a JSON
// object, e.g. the structured category notes) or nil (sent as null).
type Option struct {
    Name string
    Desc any
}

// Question is one typed question; Options are used by choices only.
type Question struct {
    Kind         Kind
    Instructions string
    Options      []Option
}

// options marshals as a JSON object that keeps the options in order.
type options []Option

func (o options) MarshalJSON() ([]byte, error) {
    var b bytes.Buffer
    b.WriteByte('{')
    for i, opt := range o {
        if i > 0 {
            b.WriteByte(',')
        }
        k, err := json.Marshal(opt.Name)
        if err != nil {
            return nil, err
        }
        v, err := json.Marshal(opt.Desc)
        if err != nil {
            return nil, err
        }
        b.Write(k)
        b.WriteByte(':')
        b.Write(v)
    }
    b.WriteByte('}')
    return b.Bytes(), nil
}

// MarshalJSON writes the wire form {"type", "instructions", "criteria"};
// criteria is omitted when there are no options.
func (q Question) MarshalJSON() ([]byte, error) {
    return json.Marshal(struct {
        Type         string  `json:"type"`
        Instructions string  `json:"instructions"`
        Criteria     options `json:"criteria,omitempty"`
    }{string(q.Kind), q.Instructions, options(q.Options)})
}

// Answer is one typed answer.
type Answer struct {
    Kind          Kind
    Yes           float64 // noul: the probability of yes
    Choice        string
    Probabilities map[string]float64
    Confidence    float64
    Raw           json.RawMessage // the provider's answer as received
}

type wireAnswer struct {
    Type          string             `json:"type"`
    Noul          *float64           `json:"noul,omitempty"`
    Choice        string             `json:"choice,omitempty"`
    Probabilities map[string]float64 `json:"probabilities,omitempty"`
    Confidence    float64            `json:"confidence,omitempty"`
}

func (a *Answer) UnmarshalJSON(b []byte) error {
    var w wireAnswer
    if err := json.Unmarshal(b, &w); err != nil {
        return err
    }
    *a = Answer{Kind: Kind(w.Type), Choice: w.Choice, Probabilities: w.Probabilities, Confidence: w.Confidence, Raw: append(json.RawMessage(nil), b...)}
    if w.Noul != nil {
        a.Yes = *w.Noul
    }
    return nil
}

// MarshalJSON returns Raw when the answer came from a provider, the wire
// form otherwise.
func (a Answer) MarshalJSON() ([]byte, error) {
    if len(a.Raw) > 0 {
        return a.Raw, nil
    }
    w := wireAnswer{Type: string(a.Kind), Choice: a.Choice, Probabilities: a.Probabilities, Confidence: a.Confidence}
    if a.Kind == KindNoul {
        w.Noul = &a.Yes
    }
    return json.Marshal(w)
}

// Probability is the probability of the chosen option of a question with n
// options. Without probabilities it inverts the documented
// confidence = (p − 1/n) / (1 − 1/n).
func (a *Answer) Probability(n int) float64 {
    if p, ok := a.Probabilities[a.Choice]; ok {
        return p
    }
    if n < 2 {
        return a.Confidence
    }
    u := 1 / float64(n)
    return a.Confidence*(1-u) + u
}

// Usage is what decisions cost, as the provider reported it.
type Usage struct {
    Calls        int     `json:"calls"` // HTTP calls, after batching
    InputTokens  int     `json:"input_tokens"`
    OutputTokens int     `json:"output_tokens"`
    CostUSD      float64 `json:"cost_usd"` // estimate: input tokens × the provider's price
    Model        string  `json:"model,omitempty"`
}

// Add counts o into u; a non-empty o.Model replaces u.Model.
func (u *Usage) Add(o Usage) {
    u.Calls += o.Calls
    u.InputTokens += o.InputTokens
    u.OutputTokens += o.OutputTokens
    u.CostUSD += o.CostUSD
    if o.Model != "" {
        u.Model = o.Model
    }
}

// Decision is the answer to one Decide call.
type Decision struct {
    Answers map[string]*Answer
    Usage   Usage
    Raw     []json.RawMessage // one response body per HTTP call
}

// Decider answers typed questions about a state.
type Decider interface {
    Decide(ctx context.Context, state any, questions map[string]Question) (*Decision, error)
    // Name identifies the backend ("typesafe", "subq") and selects its
    // calibrated thresholds.
    Name() string
    // Model is the model ID the backend sends.
    Model() string
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./olxjev/decision/ -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add olxjev/decision/decision.go olxjev/decision/decision_test.go docs/superpowers/specs/2026-10-18-olxjev-decision-backends-design.md docs/superpowers/plans/2026-10-18-olxjev-decision-backends.md
git commit -m "olxjev/decision: provider-neutral question, answer and usage types

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: `decision.Batched`

**Files:**
- Create: `olxjev/decision/batched.go`
- Test: `olxjev/decision/batched_test.go`

**Interfaces:**
- Consumes: Task 1 types.
- Produces: `func Batched(d Decider, max int) Decider`. Returns `d` itself when `max <= 0`. Otherwise it sends at most `max` questions per `d.Decide` call: keys sorted and cut into consecutive chunks, chunks run concurrently, the first error cancels the rest and is returned with a nil `*Decision`, and answers, `Usage` and `Raw` are merged in chunk order. `Name()`/`Model()` delegate to `d`.

- [ ] **Step 1: Write the failing tests**

`olxjev/decision/batched_test.go`:

```go
package decision

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "slices"
    "sort"
    "sync"
    "testing"
)

// recorder answers every question with noul 0.5 and records each call's keys.
type recorder struct {
    mu    sync.Mutex
    calls [][]string
    fail  map[string]error // a call containing this key fails
    wait  bool             // calls block until ctx is done (after recording)
}

func (r *recorder) Name() string  { return "rec" }
func (r *recorder) Model() string { return "rec-1" }

func (r *recorder) Decide(ctx context.Context, _ any, qs map[string]Question) (*Decision, error) {
    keys := make([]string, 0, len(qs))
    for k := range qs {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    r.mu.Lock()
    r.calls = append(r.calls, keys)
    r.mu.Unlock()
    for _, k := range keys {
        if err := r.fail[k]; err != nil {
            return nil, err
        }
    }
    if r.wait {
        <-ctx.Done()
        return nil, ctx.Err()
    }
    d := &Decision{Answers: map[string]*Answer{}, Usage: Usage{Calls: 1, InputTokens: 10, CostUSD: 0.5, Model: "rec-1"}, Raw: []json.RawMessage{json.RawMessage(`"` + keys[0] + `"`)}}
    for _, k := range keys {
        d.Answers[k] = &Answer{Kind: KindNoul, Yes: 0.5}
    }
    return d, nil
}

func questions(n int) map[string]Question {
    qs := map[string]Question{}
    for i := 0; i < n; i++ {
        qs[fmt.Sprintf("q%02d", i)] = Question{Kind: KindNoul, Instructions: "?"}
    }
    return qs
}

func TestBatchedSplitsSortedChunks(t *testing.T) {
    rec := &recorder{}
    d, err := Batched(rec, 16).Decide(context.Background(), "s", questions(21))
    if err != nil {
        t.Fatal(err)
    }
    sort.Slice(rec.calls, func(i, j int) bool { return rec.calls[i][0] < rec.calls[j][0] })
    if len(rec.calls) != 2 || len(rec.calls[0]) != 16 || len(rec.calls[1]) != 5 || rec.calls[0][15] != "q15" || rec.calls[1][0] != "q16" {
        t.Fatalf("calls = %v, want q00..q15 then q16..q20", rec.calls)
    }
    if len(d.Answers) != 21 || d.Usage.Calls != 2 || d.Usage.InputTokens != 20 || d.Usage.CostUSD != 1 || d.Usage.Model != "rec-1" {
        t.Errorf("merged = %d answers, usage %+v", len(d.Answers), d.Usage)
    }
    if len(d.Raw) != 2 || string(d.Raw[0]) != `"q00"` || string(d.Raw[1]) != `"q16"` {
        t.Errorf("Raw = %s, want chunk order", d.Raw)
    }
}

func TestBatchedBoundary(t *testing.T) {
    for _, c := range []struct{ n, calls int }{{16, 1}, {17, 2}, {32, 2}, {33, 3}, {1, 1}} {
        rec := &recorder{}
        if _, err := Batched(rec, 16).Decide(context.Background(), "s", questions(c.n)); err != nil {
            t.Fatal(err)
        }
        if len(rec.calls) != c.calls {
            t.Errorf("%d questions: %d calls, want %d", c.n, len(rec.calls), c.calls)
        }
    }
}

func TestBatchedPassThrough(t *testing.T) {
    rec := &recorder{}
    if b := Batched(rec, 0); b != Decider(rec) {
        t.Error("max 0 must return the Decider itself")
    }
    b := Batched(rec, 16)
    if b.Name() != "rec" || b.Model() != "rec-1" {
        t.Errorf("Name/Model = %s/%s", b.Name(), b.Model())
    }
}

func TestBatchedErrorCancelsAndReturnsNoAnswers(t *testing.T) {
    boom := errors.New("boom")
    rec := &recorder{fail: map[string]error{"q00": boom}, wait: true} // the other chunk waits for cancellation
    d, err := Batched(rec, 16).Decide(context.Background(), "s", questions(21))
    if !errors.Is(err, boom) || d != nil {
        t.Errorf("Decide = %v, %v; want nil, boom", d, err)
    }
}

func TestBatchedCancelledContext(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    rec := &recorder{wait: true}
    d, err := Batched(rec, 16).Decide(ctx, "s", questions(21))
    if !errors.Is(err, context.Canceled) || d != nil {
        t.Errorf("Decide = %v, %v; want nil, context.Canceled", d, err)
    }
    if slices.ContainsFunc(rec.calls, func(c []string) bool { return len(c) > 16 }) {
        t.Error("a call exceeded the limit")
    }
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./olxjev/decision/ -run Batched`
Expected: FAIL (undefined: Batched).

- [ ] **Step 3: Implement**

`olxjev/decision/batched.go`:

```go
package decision

import (
    "context"
    "maps"
    "slices"

    "golang.org/x/sync/errgroup"
)

// Batched returns a Decider that sends d at most max questions per call
// (SubQ accepts 16). Over max, the question keys are sorted and cut into
// consecutive chunks — the same questions always make the same chunks —
// which run concurrently; the first error cancels the rest and is returned
// with no Decision. The merged Decision has every chunk's answers, the
// summed usage and the chunks' Raw bodies in chunk order. max <= 0 returns d.
func Batched(d Decider, max int) Decider {
    if max <= 0 {
        return d
    }
    return &batched{Decider: d, max: max}
}

type batched struct {
    Decider // Name and Model
    max     int
}

func (b *batched) Decide(ctx context.Context, state any, questions map[string]Question) (*Decision, error) {
    if len(questions) <= b.max {
        return b.Decider.Decide(ctx, state, questions)
    }
    keys := slices.Sorted(maps.Keys(questions))
    var chunks [][]string
    for len(keys) > 0 {
        n := min(b.max, len(keys))
        chunks = append(chunks, keys[:n])
        keys = keys[n:]
    }
    results := make([]*Decision, len(chunks))
    g, gctx := errgroup.WithContext(ctx)
    for i, chunk := range chunks {
        g.Go(func() error {
            qs := make(map[string]Question, len(chunk))
            for _, k := range chunk {
                qs[k] = questions[k]
            }
            d, err := b.Decider.Decide(gctx, state, qs)
            if err != nil {
                return err
            }
            results[i] = d
            return nil
        })
    }
    if err := g.Wait(); err != nil {
        return nil, err
    }
    merged := &Decision{Answers: map[string]*Answer{}}
    for _, d := range results {
        if d == nil {
            continue
        }
        maps.Copy(merged.Answers, d.Answers)
        merged.Usage.Add(d.Usage)
        merged.Raw = append(merged.Raw, d.Raw...)
    }
    return merged, nil
}
```

- [ ] **Step 4: Run the tests (with the race detector) to verify they pass**

Run: `go test -race ./olxjev/decision/ -v`
Expected: PASS. If `go` reports `golang.org/x/sync` as only indirect, run `go mod tidy` and include `go.mod`/`go.sum` in the commit.

- [ ] **Step 5: Commit**

```bash
git add olxjev/decision/batched.go olxjev/decision/batched_test.go go.mod go.sum
git commit -m "olxjev/decision: Batched splits a question set into concurrent chunks

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: shared `systemone` client and the `typesafe` adapter

**Files:**
- Create: `olxjev/decision/internal/systemone/systemone.go`
- Create: `olxjev/decision/typesafe/typesafe.go`
- Test: `olxjev/decision/typesafe/typesafe_test.go`

**Interfaces:**
- Consumes: Task 1 types; `go-jev`: `jev.NewClient`, `jev.ClientOption`, `(*jev.Client).Evaluate(ctx, state, questions any) (*jev.Response, error)` (`resp.Raw` is the body; `resp.Model`, `resp.Usage.InputTokens/OutputTokens`), `(*jev.Client).Model()`.
- Produces:
  - `systemone.New(name string, price float64, client *jev.Client, wrapErr func(error) error) *systemone.Client` (implements `decision.Decider`; `Usage.Calls = 1`, `CostUSD = input × price / 1e6`, `Model` = response `model` or the client's model)
  - `typesafe.Name = "typesafe"`, `typesafe.DefaultURL = jev.DefaultURL`, `typesafe.DefaultModel = jev.DefaultModel`, `typesafe.PricePerMInputTokens = 0.042`
  - `typesafe.New(apiKey string, opts ...jev.ClientOption) decision.Decider`

- [ ] **Step 1: Write the failing tests**

`olxjev/decision/typesafe/typesafe_test.go`:

```go
package typesafe

import (
    "context"
    "io"
    "math"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/mattn/go-jev"

    "feodosian.com/olxsearch/olxjev/decision"
)

type note struct {
    Category string   `json:"category"`
    For      string   `json:"for"`
    Examples []string `json:"examples"`
}

// sameQuestions are one stage-like question set in both type systems, with
// text JSON escapes (<, >, &, quotes, «»).
func sameQuestions() (map[string]decision.Question, map[string]jev.Question) {
    instr := `Яка категорія? Оренда <без> «подобово» & "погодинно" — довгострокова.`
    desc := note{Category: "Готелі", For: "переночувати", Examples: []string{"готель на морі"}}
    ours := map[string]decision.Question{
        "category": {Kind: decision.KindChoice, Instructions: instr, Options: []decision.Option{
            {Name: "category_3713", Desc: desc}, {Name: "category_1760", Desc: "Квартири › Довгострокова"}, {Name: "none", Desc: "не вказано в запиті"},
        }},
        "place":       {Kind: decision.KindNoul, Instructions: "Чи названо місце?"},
        "gate:pets":   {Kind: decision.KindNoul, Instructions: "Тварини (варіанти: кіт; собака): чи каже запит?"},
        "nulloption":  {Kind: decision.KindChoice, Instructions: "x", Options: []decision.Option{{Name: "a", Desc: nil}, {Name: "b", Desc: "b"}}},
    }
    theirs := map[string]jev.Question{
        "category": {Type: "choice", Instructions: instr, Criteria: jev.Options{
            {Name: "category_3713", Desc: desc}, {Name: "category_1760", Desc: "Квартири › Довгострокова"}, {Name: "none", Desc: "не вказано в запиті"},
        }},
        "place":       {Type: "noul", Instructions: "Чи названо місце?"},
        "gate:pets":   {Type: "noul", Instructions: "Тварини (варіанти: кіт; собака): чи каже запит?"},
        "nulloption":  {Type: "choice", Instructions: "x", Criteria: jev.Options{{Name: "a", Desc: nil}, {Name: "b", Desc: "b"}}},
    }
    return ours, theirs
}

// capture serves one canned answer and records each request body.
func capture(t *testing.T, bodies *[]string) *httptest.Server {
    t.Helper()
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        b, _ := io.ReadAll(r.Body)
        *bodies = append(*bodies, string(b))
        w.Write([]byte(`{"model":"jev-1.14","answers":{"place":{"type":"noul","noul":0.92},"category":{"type":"choice","choice":"category_1760","probabilities":{"category_1760":0.9},"confidence":0.85}},"usage":{"input_tokens":2000,"output_tokens":0}}`))
    }))
    t.Cleanup(srv.Close)
    return srv
}

// The adapter must send exactly what go-jev sent before the refactor.
func TestWireBytesMatchGoJev(t *testing.T) {
    var bodies []string
    srv := capture(t, &bodies)
    ours, theirs := sameQuestions()
    state := map[string]any{"query": "зняти <квартиру> & кімнату", "resolved": map[string]string{"Категорія": "Квартири"}}
    if _, err := jev.NewClient(jev.WithURL(srv.URL)).Evaluate(context.Background(), state, theirs); err != nil {
        t.Fatal(err)
    }
    if _, err := New("", jev.WithURL(srv.URL)).Decide(context.Background(), state, ours); err != nil {
        t.Fatal(err)
    }
    if len(bodies) != 2 || bodies[0] != bodies[1] {
        t.Errorf("request bodies differ:\ngo-jev:  %s\nadapter: %s", bodies[0], bodies[1])
    }
}

func TestDecideTranslatesAnswersAndUsage(t *testing.T) {
    var bodies []string
    srv := capture(t, &bodies)
    ours, _ := sameQuestions()
    d, err := New("", jev.WithURL(srv.URL)).Decide(context.Background(), "q", ours)
    if err != nil {
        t.Fatal(err)
    }
    if a := d.Answers["place"]; a == nil || a.Kind != decision.KindNoul || a.Yes != 0.92 {
        t.Errorf("place = %+v", a)
    }
    if a := d.Answers["category"]; a == nil || a.Choice != "category_1760" || a.Probability(3) != 0.9 || len(a.Raw) == 0 {
        t.Errorf("category = %+v", a)
    }
    u := d.Usage
    if u.Calls != 1 || u.InputTokens != 2000 || u.Model != "jev-1.14" || math.Abs(u.CostUSD-2000*PricePerMInputTokens/1e6) > 1e-12 {
        t.Errorf("Usage = %+v", u)
    }
    if len(d.Raw) != 1 {
        t.Errorf("Raw = %d bodies, want 1", len(d.Raw))
    }
}

func TestNameModelAndAuth(t *testing.T) {
    var auth string
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        auth = r.Header.Get("Authorization")
        w.Write([]byte(`{"answers":{},"usage":{"input_tokens":1}}`))
    }))
    defer srv.Close()
    d := New("k-1", jev.WithURL(srv.URL))
    if d.Name() != "typesafe" || d.Model() != jev.DefaultModel {
        t.Errorf("Name/Model = %s/%s", d.Name(), d.Model())
    }
    got, err := d.Decide(context.Background(), "q", map[string]decision.Question{"x": {Kind: decision.KindNoul, Instructions: "?"}})
    if err != nil {
        t.Fatal(err)
    }
    if auth != "Bearer k-1" || got.Usage.Model != jev.DefaultModel {
        t.Errorf("auth %q, usage model %q (want the client's model when the response has none)", auth, got.Usage.Model)
    }
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./olxjev/decision/typesafe/`
Expected: FAIL (undefined: New / PricePerMInputTokens).

- [ ] **Step 3: Implement the shared client**

`olxjev/decision/internal/systemone/systemone.go`:

```go
// Package systemone talks the System One protocol that TypeSafe (Jev) and
// SubQ (Snap) share — POST {model, state, questions} → {answers, usage} —
// over go-jev's HTTP transport (timeouts, retries on 429/529).
package systemone

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/mattn/go-jev"

    "feodosian.com/olxsearch/olxjev/decision"
)

// Client is a decision.Decider for one System One provider.
type Client struct {
    name    string
    price   float64 // USD per million input tokens
    jev     *jev.Client
    wrapErr func(error) error // provider-specific error text; nil keeps errors as they are
}

// New returns a Decider named name that posts through client and prices
// input tokens at price USD per million.
func New(name string, price float64, client *jev.Client, wrapErr func(error) error) *Client {
    return &Client{name: name, price: price, jev: client, wrapErr: wrapErr}
}

func (c *Client) Name() string  { return c.name }
func (c *Client) Model() string { return c.jev.Model() }

func (c *Client) Decide(ctx context.Context, state any, questions map[string]decision.Question) (*decision.Decision, error) {
    resp, err := c.jev.Evaluate(ctx, state, questions)
    if err != nil {
        if c.wrapErr != nil {
            err = c.wrapErr(err)
        }
        return nil, err
    }
    var body struct {
        Answers map[string]*decision.Answer `json:"answers"`
    }
    if err := json.Unmarshal(resp.Raw, &body); err != nil {
        return nil, fmt.Errorf("%s: unexpected response: %w", c.name, err)
    }
    model := resp.Model
    if model == "" {
        model = c.Model()
    }
    return &decision.Decision{
        Answers: body.Answers,
        Usage: decision.Usage{
            Calls:        1,
            InputTokens:  resp.Usage.InputTokens,
            OutputTokens: resp.Usage.OutputTokens,
            CostUSD:      float64(resp.Usage.InputTokens) * c.price / 1e6,
            Model:        model,
        },
        Raw: []json.RawMessage{resp.Raw},
    }, nil
}
```

- [ ] **Step 4: Implement the adapter**

`olxjev/decision/typesafe/typesafe.go`:

```go
// Package typesafe is the decision.Decider for TypeSafe's Jev
// (docs.typesafe.ai), the default olxjev backend.
package typesafe

import (
    "github.com/mattn/go-jev"

    "feodosian.com/olxsearch/olxjev/decision"
    "feodosian.com/olxsearch/olxjev/decision/internal/systemone"
)

const (
    Name         = "typesafe"
    DefaultURL   = jev.DefaultURL
    DefaultModel = jev.DefaultModel
    // PricePerMInputTokens is Jev's price in USD per million input tokens;
    // output tokens are free (docs.typesafe.ai/models: Jev 1.13 and 1.14,
    // $0.042/Mtok, as of 2026-10-15).
    PricePerMInputTokens = 0.042
)

// New returns a Jev Decider; opts (URL, model, timeout, retries) override
// the TypeSafe defaults. An empty apiKey sends no Authorization header,
// which suits open servers such as `tensai serve`.
func New(apiKey string, opts ...jev.ClientOption) decision.Decider {
    opts = append([]jev.ClientOption{jev.WithAPIKey(apiKey)}, opts...)
    return systemone.New(Name, PricePerMInputTokens, jev.NewClient(opts...), nil)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./olxjev/decision/... -v`
Expected: PASS. If `TestWireBytesMatchGoJev` fails, compare the two bodies: the usual cause is a field order or `omitempty` difference in `Question.MarshalJSON`. Fix `decision.go`, not the test.

- [ ] **Step 6: Commit**

```bash
git add olxjev/decision/internal olxjev/decision/typesafe
git commit -m "olxjev/decision/typesafe: Jev adapter over a shared System One client

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: the `subq` adapter

**Files:**
- Create: `olxjev/decision/subq/subq.go`
- Test: `olxjev/decision/subq/subq_test.go`

**Interfaces:**
- Consumes: `decision.Batched`, `systemone.New`, `jev.APIError{Status int; Body string}`.
- Produces: `subq.Name = "subq"`, `subq.DefaultURL`, `subq.DefaultModel`, `subq.MaxQuestions = 16`, `subq.PricePerMInputTokens = 0.05`, `subq.New(apiKey string, opts ...jev.ClientOption) decision.Decider`, `type subq.Error struct{ Status int; Type, Message string }` (implements `error`, unwraps to the `*jev.APIError`).

- [ ] **Step 1: Write the failing tests**

`olxjev/decision/subq/subq_test.go`:

```go
package subq

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "math"
    "net/http"
    "net/http/httptest"
    "strings"
    "sync"
    "testing"

    "github.com/mattn/go-jev"

    "feodosian.com/olxsearch/olxjev/decision"
)

func nouls(n int) map[string]decision.Question {
    qs := map[string]decision.Question{}
    for i := 0; i < n; i++ {
        qs[fmt.Sprintf("q%02d", i)] = decision.Question{Kind: decision.KindNoul, Instructions: "?"}
    }
    return qs
}

// fakeSubq answers every noul with 0.5 and records each request.
type fakeSubq struct {
    mu       sync.Mutex
    auth     []string
    models   []string
    nQuestions []int
}

func (f *fakeSubq) handler(w http.ResponseWriter, r *http.Request) {
    var body struct {
        Model     string                     `json:"model"`
        Questions map[string]json.RawMessage `json:"questions"`
    }
    json.NewDecoder(r.Body).Decode(&body)
    f.mu.Lock()
    f.auth = append(f.auth, r.Header.Get("Authorization"))
    f.models = append(f.models, body.Model)
    f.nQuestions = append(f.nQuestions, len(body.Questions))
    f.mu.Unlock()
    if len(body.Questions) > MaxQuestions {
        w.WriteHeader(http.StatusUnprocessableEntity)
        w.Write([]byte(`{"detail":{"error_type":"validation_error","message":"questions: must NOT have more than 16 properties"}}`))
        return
    }
    answers := map[string]any{}
    for k := range body.Questions {
        answers[k] = map[string]any{"type": "noul", "noul": 0.5}
    }
    json.NewEncoder(w).Encode(map[string]any{"model": body.Model, "answers": answers, "usage": map[string]int{"input_tokens": 300, "output_tokens": 0}})
}

func TestSubqBatchesAndPrices(t *testing.T) {
    f := &fakeSubq{}
    srv := httptest.NewServer(http.HandlerFunc(f.handler))
    defer srv.Close()
    d := New("sk-test", jev.WithURL(srv.URL))
    if d.Name() != "subq" || d.Model() != "subq-snap-preview" {
        t.Errorf("Name/Model = %s/%s", d.Name(), d.Model())
    }
    got, err := d.Decide(context.Background(), "q", nouls(21))
    if err != nil {
        t.Fatal(err)
    }
    if len(f.nQuestions) != 2 || f.nQuestions[0]+f.nQuestions[1] != 21 || max(f.nQuestions[0], f.nQuestions[1]) != 16 {
        t.Errorf("requests carried %v questions, want 16 + 5", f.nQuestions)
    }
    for i := range f.auth {
        if f.auth[i] != "Bearer sk-test" || f.models[i] != "subq-snap-preview" {
            t.Errorf("request %d: auth %q, model %q", i, f.auth[i], f.models[i])
        }
    }
    u := got.Usage
    if len(got.Answers) != 21 || u.Calls != 2 || u.InputTokens != 600 || u.Model != "subq-snap-preview" || math.Abs(u.CostUSD-600*0.05/1e6) > 1e-12 {
        t.Errorf("answers %d, usage %+v", len(got.Answers), u)
    }
}

func TestSubqDefaultsAndOverrides(t *testing.T) {
    if DefaultURL != "https://api.subq.ai/v1/systemone" || DefaultModel != "subq-snap-preview" || MaxQuestions != 16 || PricePerMInputTokens != 0.05 {
        t.Error("SubQ constants changed: check docs.subq.ai and update the spec")
    }
    if m := New("", jev.WithModel("subq-snap-2")).Model(); m != "subq-snap-2" {
        t.Errorf("model override: %s", m)
    }
}

func TestSubqDetailErrors(t *testing.T) {
    for _, c := range []struct {
        status int
        body   string
        want   []string
    }{
        {401, `{"detail":{"error_type":"authentication_error","message":"Invalid API key."}}`, []string{"subq: HTTP 401 authentication_error: Invalid API key."}},
        {402, `{"detail":{"error_type":"billing_error","message":"Out of credits."}}`, []string{"HTTP 402", "Out of credits.", "insufficient credits"}},
        {422, `{"detail":{"error_type":"validation_error","message":"questions/x: must have required property 'criteria'"}}`, []string{"validation_error", "criteria"}},
    } {
        srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.WriteHeader(c.status)
            w.Write([]byte(c.body))
        }))
        _, err := New("k", jev.WithURL(srv.URL)).Decide(context.Background(), "q", nouls(1))
        srv.Close()
        var se *Error
        if !errors.As(err, &se) || se.Status != c.status {
            t.Errorf("%d: err = %v, want *subq.Error", c.status, err)
            continue
        }
        var api *jev.APIError
        if !errors.As(err, &api) {
            t.Errorf("%d: must unwrap to *jev.APIError", c.status)
        }
        for _, w := range c.want {
            if !strings.Contains(err.Error(), w) {
                t.Errorf("%d: %q lacks %q", c.status, err.Error(), w)
            }
        }
    }
}

// A proxy's HTML error page still yields a readable error with the status.
func TestSubqNonJSONError(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusBadGateway)
        w.Write([]byte("<html>502 Bad Gateway</html>"))
    }))
    defer srv.Close()
    _, err := New("k", jev.WithURL(srv.URL)).Decide(context.Background(), "q", nouls(1))
    if err == nil || !strings.HasPrefix(err.Error(), "subq: ") || !strings.Contains(err.Error(), "502") {
        t.Errorf("err = %v, want «subq: … 502 …»", err)
    }
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./olxjev/decision/subq/`
Expected: FAIL (undefined: New).

- [ ] **Step 3: Implement**

`olxjev/decision/subq/subq.go`:

```go
// Package subq is the decision.Decider for SubQ's Decisions API
// (docs.subq.ai/endpoints/decisions-api): the System One protocol with at
// most 16 questions per request, so larger question sets are batched.
package subq

import (
    "encoding/json"
    "errors"
    "fmt"
    "net/http"

    "github.com/mattn/go-jev"

    "feodosian.com/olxsearch/olxjev/decision"
    "feodosian.com/olxsearch/olxjev/decision/internal/systemone"
)

const (
    Name         = "subq"
    DefaultURL   = "https://api.subq.ai/v1/systemone"
    DefaultModel = "subq-snap-preview"
    // MaxQuestions is the Decisions API's per-request limit (1–16).
    MaxQuestions = 16
    // PricePerMInputTokens is SubQ Snap's price in USD per million input
    // tokens; output tokens are free (docs.subq.ai/using-the-api/models,
    // $0.05/Mtok, as of 2026-10-17).
    PricePerMInputTokens = 0.05
)

// New returns a SubQ Decider; opts (URL, model, timeout, retries) override
// the SubQ defaults.
func New(apiKey string, opts ...jev.ClientOption) decision.Decider {
    opts = append([]jev.ClientOption{jev.WithAPIKey(apiKey), jev.WithURL(DefaultURL), jev.WithModel(DefaultModel)}, opts...)
    return decision.Batched(systemone.New(Name, PricePerMInputTokens, jev.NewClient(opts...), apiError), MaxQuestions)
}

// Error is a SubQ error response: {"detail": {"error_type", "message"}}.
type Error struct {
    Status  int
    Type    string
    Message string
    err     error
}

func (e *Error) Error() string {
    msg := fmt.Sprintf("subq: HTTP %d %s: %s", e.Status, e.Type, e.Message)
    if e.Status == http.StatusPaymentRequired {
        msg += " (insufficient credits)"
    }
    return msg
}

func (e *Error) Unwrap() error { return e.err }

// apiError turns a SubQ error body into an *Error; other errors get the
// "subq: " prefix.
func apiError(err error) error {
    var api *jev.APIError
    if !errors.As(err, &api) {
        return fmt.Errorf("subq: %w", err)
    }
    var body struct {
        Detail struct {
            ErrorType string `json:"error_type"`
            Message   string `json:"message"`
        } `json:"detail"`
    }
    if json.Unmarshal([]byte(api.Body), &body) != nil || body.Detail.Message == "" {
        return fmt.Errorf("subq: %w", err)
    }
    return &Error{Status: api.Status, Type: body.Detail.ErrorType, Message: body.Detail.Message, err: err}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./olxjev/decision/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add olxjev/decision/subq
git commit -m "olxjev/decision/subq: SubQ Decisions API adapter, batched at 16 questions

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: migrate `olxjev` and `cmd/olxjev` to `decision` (no new features)

The behavior stays the same except `FieldInfo.Source` (`"jev"` → `"model"`) and the new `usage.model` JSON field. The package can't compile half-migrated, so this is one task with one commit.

**Files:**
- Modify: `olxjev/resolver.go`, `olxjev/questions.go`, `olxjev/debug.go`
- Delete: `olxjev/usage.go`
- Rename + rewrite: `olxjev/fakejev_test.go` → `olxjev/fakedecider_test.go`
- Modify (mechanical): `olxjev/usage_test.go`, `olxjev/resolver_test.go`, `olxjev/thresholds_test.go`, `olxjev/calibration_test.go`, `olxjev/calibration_util_test.go`, `olxjev/category_candidates_test.go`, `olxjev/debug_test.go`, `olxjev/gate_test.go`, `olxjev/groups_test.go`, `olxjev/place_test.go`, `olxjev/pricebound_test.go`, `olxjev/questions_test.go`, `olxjev/siblings_test.go`, `olxjev/golden_test.go`
- Modify: `cmd/olxjev/main.go`, `cmd/olxjev/debug.go`, `cmd/olxjev/web.go`, `cmd/olxjev/web_debug_test.go`

**Interfaces:**
- Consumes: Tasks 1–3 (`decision.*`, `typesafe.New`, `typesafe.Name`).
- Produces (used by Tasks 6–8):
  - `olxjev.Resolver.Decider decision.Decider` (replaces `Jev Evaluator`; `Evaluator` is removed)
  - `type TraceFunc func(stage string, state any, questions map[string]decision.Question, d *decision.Decision)`
  - `FilterPlan.Questions`, `FilterPlan.Options map[string]decision.Question`
  - `Result.Usage decision.Usage`
  - `SourceModel = "model"` (replaces `SourceJev`)
  - `var CalibratedThresholds = map[string]Thresholds{"typesafe": DefaultThresholds}`
  - `NewResolver(composer, decider decision.Decider)` picks `CalibratedThresholds[decider.Name()]`, else `DefaultThresholds` (also when `decider` is nil)
  - `(*Resolver).CategoryCandidates(answer *decision.Answer) []FieldInfo`
  - test helper `liveDecider(t) decision.Decider` in `golden_test.go` (reads `OLXJEV_LIVE`, `OLXJEV_BACKEND`, the backend's key env)

**Mechanical substitution table** (apply in every listed file):

| Old | New |
|---|---|
| import `"github.com/mattn/go-jev"` (in `olxjev/*` and `cmd/olxjev/debug.go`, `web.go`) | `"feodosian.com/olxsearch/olxjev/decision"` |
| `jev.Question{Type: "choice", Instructions: X, Criteria: opts}` | `decision.Question{Kind: decision.KindChoice, Instructions: X, Options: opts}` |
| `jev.Question{Type: "noul", Instructions: X}` / `{Type: "noul"}` | `decision.Question{Kind: decision.KindNoul, Instructions: X}` / `{Kind: decision.KindNoul}` |
| `{Type: "choice"}` (question literal) | `{Kind: decision.KindChoice}` |
| `jev.Options{}` / `jev.Options` | `[]decision.Option{}` / `[]decision.Option` |
| `jev.Option{Name: …, Desc: …}` | `decision.Option{Name: …, Desc: …}` |
| `q.Criteria.(jev.Options)` (incl. `options, _ := q.Criteria.(jev.Options)`) | `q.Options` (`options := q.Options`) |
| `q.Type == "noul"` / `question.Type` | `q.Kind == decision.KindNoul` / `string(question.Kind)` |
| `jev.Answer{Type: "choice", …}` | `decision.Answer{Kind: decision.KindChoice, …}` |
| `jev.Answer{Type: "noul", Noul: p}` / `{Type: "noul", Noul: 0.98}` | `decision.Answer{Kind: decision.KindNoul, Yes: p}` / `{Kind: decision.KindNoul, Yes: 0.98}` |
| `a.Noul` / `answers[k].Noul` | `a.Yes` / `answers[k].Yes` |
| `a.Type` (answer) | `a.Kind` |
| `*jev.Answer`, `map[string]*jev.Answer` | `*decision.Answer`, `map[string]*decision.Answer` |
| `map[string]jev.Question` | `map[string]decision.Question` |
| `*jev.Response`, `&jev.Response{Answers: x}` | `*decision.Decision`, `&decision.Decision{Answers: x}` |
| trace param `resp *jev.Response` | `d *decision.Decision` (rename uses of `resp` inside the closure to `d`) |
| `answerProbability(a, n)` | `a.Probability(n)` |
| `SourceJev` | `SourceModel` |
| `fakeJev` | `fakeDecider` |
| `hasOption(options jev.Options, …)` | `hasOption(options []decision.Option, …)` |

- [ ] **Step 1: Rewrite the fake (test-first: the package stops compiling until Step 3)**

Delete `olxjev/fakejev_test.go`; create `olxjev/fakedecider_test.go`:

```go
package olxjev

import (
    "context"

    "feodosian.com/olxsearch/olxjev/decision"
)

// fakePrice prices fake input tokens like Jev, so costs read as before.
const fakePrice = 0.042

// fakeDecider answers every question with "none" (choice) or 0.01 (noul)
// unless a canned answer for that call index and question key is given.
type fakeDecider struct {
    name    string                        // "" means "fake"
    answers []map[string]*decision.Answer // per call
    errs    []error                       // per call
    tokens  [][2]int                      // per call: input, output tokens reported
    calls   []fakeCall
}

type fakeCall struct {
    state     any
    questions map[string]decision.Question
}

func (f *fakeDecider) Name() string {
    if f.name == "" {
        return "fake"
    }
    return f.name
}

func (f *fakeDecider) Model() string { return "fake-1" }

func (f *fakeDecider) Decide(_ context.Context, state any, qs map[string]decision.Question) (*decision.Decision, error) {
    i := len(f.calls)
    f.calls = append(f.calls, fakeCall{state: state, questions: qs})
    if i < len(f.errs) && f.errs[i] != nil {
        return nil, f.errs[i]
    }
    d := &decision.Decision{Answers: map[string]*decision.Answer{}, Usage: decision.Usage{Calls: 1, Model: "fake-1"}}
    if i < len(f.tokens) {
        d.Usage.InputTokens, d.Usage.OutputTokens = f.tokens[i][0], f.tokens[i][1]
        d.Usage.CostUSD = float64(f.tokens[i][0]) * fakePrice / 1e6
    }
    for key, q := range qs {
        if q.Kind == decision.KindNoul {
            d.Answers[key] = noul(0.01)
        } else {
            d.Answers[key] = choice(NoneOption, 0.9)
        }
    }
    if i < len(f.answers) {
        for key, a := range f.answers[i] {
            d.Answers[key] = a
        }
    }
    return d, nil
}

func choice(option string, confidence float64) *decision.Answer {
    return &decision.Answer{Kind: decision.KindChoice, Choice: option, Confidence: confidence}
}

func noul(p float64) *decision.Answer {
    return &decision.Answer{Kind: decision.KindNoul, Yes: p}
}
```

Replace `olxjev/usage_test.go` with:

```go
package olxjev

import (
    "context"
    "errors"
    "math"
    "testing"

    "feodosian.com/olxsearch/olxjev/decision"
)

// Result.Usage sums the decision calls of one Resolve, as the Decider
// priced them, and names the model.
func TestResolveUsageSumsCalls(t *testing.T) {
    f := &fakeDecider{
        answers: []map[string]*decision.Answer{flatRentKharkiv(), {}},
        tokens:  [][2]int{{3000, 7}, {2000, 13}},
    }
    res, err := newTestResolver(t, f).Resolve(context.Background(), "зняти квартиру в Харкові")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    u := res.Usage
    if u.Calls != 2 || u.InputTokens != 5000 || u.OutputTokens != 20 || u.Model != "fake-1" {
        t.Errorf("Usage = %+v, want 2 calls, 5000 input, 20 output tokens, model fake-1", u)
    }
    if want := 5000 * fakePrice / 1e6; math.Abs(u.CostUSD-want) > 1e-12 {
        t.Errorf("CostUSD = %v, want %v", u.CostUSD, want)
    }
}

func TestResolveUsageStage1Only(t *testing.T) {
    f := &fakeDecider{tokens: [][2]int{{4200, 3}}} // category "none": no stage 2
    res, err := newTestResolver(t, f).Resolve(context.Background(), "щось незрозуміле")
    if !errors.Is(err, ErrCategoryUnresolved) {
        t.Fatalf("err = %v, want ErrCategoryUnresolved", err)
    }
    if res.Usage.Calls != 1 || res.Usage.InputTokens != 4200 {
        t.Errorf("Usage = %+v, want the stage-1 call only", res.Usage)
    }
}
```

Add to `olxjev/resolver_test.go` (after `newTestResolver`; its parameter type becomes `*fakeDecider`):

```go
// NewResolver takes the thresholds calibrated for the Decider's backend,
// DefaultThresholds for an unknown one.
func TestNewResolverCalibratedThresholds(t *testing.T) {
    custom := Thresholds{Category: 0.6, Place: 0.6, Presence: 0.6, Choice: 0.6, Number: 0.9, Multi: 0.45, Gate: 0.35}
    CalibratedThresholds["test-backend"] = custom
    defer delete(CalibratedThresholds, "test-backend")
    r, err := NewResolver(testComposer(t), &fakeDecider{name: "test-backend"})
    if err != nil {
        t.Fatal(err)
    }
    if r.Thresholds != custom {
        t.Errorf("Thresholds = %+v, want %+v", r.Thresholds, custom)
    }
    for _, d := range []decision.Decider{&fakeDecider{name: "unknown"}, nil} {
        r, err := NewResolver(testComposer(t), d)
        if err != nil {
            t.Fatal(err)
        }
        if r.Thresholds != DefaultThresholds {
            t.Errorf("%v: Thresholds = %+v, want DefaultThresholds", d, r.Thresholds)
        }
    }
    if CalibratedThresholds["typesafe"] != DefaultThresholds {
        t.Error("typesafe must keep the Jev-calibrated DefaultThresholds")
    }
}
```

Note: `NewResolver(…, nil)` with a typed nil `*fakeDecider` is **not** nil; the loop above passes an untyped `nil` interface on purpose.

Add to `olxjev/resolver_test.go`:

```go
// Every accepted field says it came from the model.
func TestResolveSourceIsModel(t *testing.T) {
    res, err := newTestResolver(t, &fakeDecider{answers: []map[string]*decision.Answer{flatRentKharkiv()}}).Resolve(context.Background(), exampleQuery)
    if err != nil {
        t.Fatal(err)
    }
    if f := res.Fields[QuestionCategory]; f.Source != "model" {
        t.Errorf("category source = %q, want model", f.Source)
    }
}
```

Apply the substitution table to every other `olxjev/*_test.go` file listed above. In `olxjev/thresholds_test.go`, the test that calls `answerProbability` (around line 74) now calls `a.Probability(c.n)`, or delete it, since `decision.TestAnswerProbability` covers the same table. In `olxjev/calibration_util_test.go`, `pick()` and the bool/gate/group branches read `a.Yes` and `q.Options`.

Replace `TestGolden`'s client setup in `olxjev/golden_test.go` (imports: add `"feodosian.com/olxsearch/olxjev/decision"`, `".../olxjev/decision/subq"`, `".../olxjev/decision/typesafe"`; keep `"github.com/mattn/go-jev"` for the client options):

```go
// liveDecider is the backend OLXJEV_BACKEND names (typesafe by default),
// with its API key from the environment; the test skips without
// OLXJEV_LIVE=1 or the key, since live runs spend credits.
func liveDecider(t *testing.T) decision.Decider {
    t.Helper()
    if os.Getenv("OLXJEV_LIVE") != "1" {
        t.Skip("set OLXJEV_LIVE=1 and the backend's API key to run the live eval (spends API credits)")
    }
    opts := []jev.ClientOption{jev.WithTimeout(30 * time.Second), jev.WithMaxRetries(3)}
    switch b := os.Getenv("OLXJEV_BACKEND"); b {
    case "", typesafe.Name:
        if os.Getenv("TYPESAFE_API_KEY") == "" {
            t.Skip("set TYPESAFE_API_KEY")
        }
        return typesafe.New(os.Getenv("TYPESAFE_API_KEY"), opts...)
    case subq.Name:
        if os.Getenv("SUBQ_API_KEY") == "" {
            t.Skip("set SUBQ_API_KEY")
        }
        return subq.New(os.Getenv("SUBQ_API_KEY"), opts...)
    default:
        t.Fatalf("OLXJEV_BACKEND=%q: want typesafe or subq", b)
    }
    return nil
}
```

and in `TestGolden`:

```go
// TestGolden calls the live decision API and reports per-field accuracy.
// Run: OLXJEV_LIVE=1 TYPESAFE_API_KEY=... go test -run TestGolden -v ./olxjev/
// SubQ: OLXJEV_LIVE=1 OLXJEV_BACKEND=subq SUBQ_API_KEY=... go test -run TestGolden -v ./olxjev/
func TestGolden(t *testing.T) {
    decider := liveDecider(t)
    r, err := NewResolver(testComposer(t), decider)
    if err != nil {
        t.Fatalf("NewResolver: %v", err)
    }
    t.Logf("backend %s, model %s, thresholds %+v", decider.Name(), decider.Model(), r.Thresholds)

    // every question and raw answer of the current case, for calibration
    asked, answered := map[string]decision.Question{}, map[string]*decision.Answer{}
    stageTokens := map[string]int{} // input tokens per stage, the whole corpus
    r.Trace = func(stage string, _ any, questions map[string]decision.Question, d *decision.Decision) {
        maps.Copy(asked, questions)
        if d != nil {
            maps.Copy(answered, d.Answers)
            stageTokens[stage] += d.Usage.InputTokens
        }
    }
    samples := []calSample{}
    var usage decision.Usage
```

The rest of `TestGolden` is unchanged; it reads `usage.Calls` and `usage.InputTokens`. Also add the cost to the final usage log line: append `, $%.4f total` with `usage.CostUSD`, and accumulate `usage.Add(res.Usage)` in place of the two manual sums.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./olxjev/ 2>&1 | head -20`
Expected: FAIL to compile (`fakeDecider` does not implement `Evaluator`; `undefined: CalibratedThresholds`; `decision` types vs `jev` types in production code).

- [ ] **Step 3: Migrate `olxjev` production code**

In `olxjev/resolver.go`:
- Replace the import `"github.com/mattn/go-jev"` with `"feodosian.com/olxsearch/olxjev/decision"`.
- Package doc, first paragraph: "letting Jev make the wizard's choices" → "letting a decision model make the wizard's choices (TypeSafe's Jev or SubQ's Snap, see olxjev/decision)".
- `SourceJev = "jev"` → `SourceModel = "model" // answered by the decision model`.
- Delete the `Evaluator` interface. `TraceFunc`:

```go
// TraceFunc observes every decision exchange, once per stage with the
// stage's questions and merged answers; d is nil when the call failed.
type TraceFunc func(stage string, state any, questions map[string]decision.Question, d *decision.Decision)
```

- `FieldInfo.Probability` comment: "Jev's probability" → "the model's probability".
- `Result.Usage`: `Usage decision.Usage \`json:"usage"\`` with comment "what this Resolve spent on decisions, with an estimated cost".
- `Resolver` field `Jev Evaluator` → `Decider decision.Decider`.
- After `DefaultThresholds` add:

```go
// CalibratedThresholds are the thresholds per decision backend (Decider
// Name), each calibrated on the golden corpus; NewResolver falls back to
// DefaultThresholds for a backend missing here.
var CalibratedThresholds = map[string]Thresholds{
    "typesafe": DefaultThresholds,
}
```

- `NewResolver`:

```go
// NewResolver builds the catalog and gazetteer from the composer's data and
// takes the thresholds calibrated for decider's backend.
func NewResolver(composer *botform.BotFormComposer, decider decision.Decider) (*Resolver, error) {
    catalog, err := NewCatalog(composer.Data)
    if err != nil {
        return nil, err
    }
    gazetteer, err := NewGazetteer(composer.Data)
    if err != nil {
        return nil, err
    }
    thresholds := DefaultThresholds
    if decider != nil {
        if t, ok := CalibratedThresholds[decider.Name()]; ok {
            thresholds = t
        }
    }
    return &Resolver{Composer: composer, Catalog: catalog, Gazetteer: gazetteer, Decider: decider, Thresholds: thresholds}, nil
}
```

- `evaluate`:

```go
func (r *Resolver) evaluate(ctx context.Context, stage string, state any, questions map[string]decision.Question) (*decision.Decision, error) {
    d, err := r.Decider.Decide(ctx, state, questions)
    if err != nil {
        d = nil
    }
    if r.Trace != nil {
        r.Trace(stage, state, questions, d)
    }
    if trace, ok := ctx.Value(traceKey{}).(TraceFunc); ok {
        trace(stage, state, questions, d)
    }
    if err != nil {
        return nil, err
    }
    if d == nil || d.Answers == nil {
        return nil, errors.New("the decision model returned no answers")
    }
    return d, nil
}
```

- In `Resolve`: `res.Usage.add(respN)` → `res.Usage.Add(respN.Usage)` (three places); `q3 := map[string]decision.Question{}`; `&jev.Response{Answers: answers}` → `&decision.Decision{Answers: answers}`; `a.Noul` → `a.Yes` (place and gate checks).
- `acceptChoice(resp *decision.Decision, …)`: `options := q.Options`; `p := a.Probability(len(options))`.
- `applyStage2(…, resp *decision.Decision)`: `a.Noul` → `a.Yes` everywhere; `SourceJev` → `SourceModel`.
- `hasOption(options []decision.Option, name string)`.
- Delete `answerProbability` (now `(*decision.Answer).Probability`).
- `CategoryCandidates(answer *decision.Answer)`, `cityCandidates(cities []CityCandidate, answer *decision.Answer)`: `SourceJev` → `SourceModel`.

In `olxjev/questions.go`: replace the import and apply the table (`stage1Questions`, `stage2Questions`, `multiOptionQuestions`, `gateQuestion` return `map[string]decision.Question` / `decision.Question`). Strings, option order and descriptions don't change.

In `olxjev/debug.go`: replace the import; `FilterPlan.Questions` and `.Options` become `map[string]decision.Question`; comment "what stage 2 asks Jev" → "what stage 2 asks the decision model"; `WithTrace` comment "every Jev exchange" → "every decision exchange".

Delete `olxjev/usage.go`.

- [ ] **Step 4: Migrate `cmd/olxjev` to keep it compiling (Jev only, same output)**

`cmd/olxjev/main.go`:
- Imports: add `"feodosian.com/olxsearch/olxjev/decision"` and `"feodosian.com/olxsearch/olxjev/decision/typesafe"`; keep `"github.com/mattn/go-jev"` (client options).
- `-debug` trace:

```go
        resolver.Trace = func(stage string, state any, questions map[string]decision.Question, d *decision.Decision) {
            dump, _ := json.MarshalIndent(map[string]any{"stage": stage, "state": state, "questions": questions}, "", "  ")
            fmt.Fprintln(stderr, string(dump))
            if d != nil {
                for _, raw := range d.Raw {
                    fmt.Fprintln(stderr, string(raw))
                }
            }
        }
```

- `newResolver` body: `client := typesafe.New(apiKey, jev.WithURL(endpoint), jev.WithTimeout(30*time.Second), jev.WithMaxRetries(3))`; the rest is unchanged.
- `costLine(u olxjev.Usage)` → `costLine(u decision.Usage)` (format unchanged in this task).

`cmd/olxjev/web.go`: replace the `go-jev` import with `decision`; the `WithTrace` closure:

```go
        ctx = olxjev.WithTrace(ctx, func(stage string, state any, questions map[string]decision.Question, d *decision.Decision) {
            exchanges = append(exchanges, exchange{stage: stage, state: state, questions: questions, d: d})
        })
```

`cmd/olxjev/debug.go`:
- Replace the `go-jev` import with `decision`; `exchange.questions map[string]decision.Question`; `resp *jev.Response` → `d *decision.Decision // nil when the call failed`; every `.resp` → `.d`, and `&jev.Response{Answers: answers}` → `&decision.Decision{Answers: answers}`.
- `dv.Stage1 = append(…, s.filterView(sq.key, sq.title, string(question.Kind), map[string]decision.Question{sq.key: question}, nil, …))`.
- `filterView(…, questions, options map[string]decision.Question, …)`: `var answers map[string]*decision.Answer`; `answers = ex.d.Answers`; the loop's set type is `[]map[string]decision.Question{questions, options}`; `optionsAnswered = optionsAnswered || options[k].Kind != ""`; the request struct field is `Questions map[string]decision.Question \`json:"questions"\``.
- `questionOf`:

```go
func questionOf(key string, q decision.Question, a *decision.Answer) questionView {
    qv := questionView{Key: key, Type: string(q.Kind), Instructions: q.Instructions}
    for _, o := range q.Options {
        ov := optionView{Name: o.Name, Desc: textOf(o.Desc)}
        if a != nil {
            if p, ok := a.Probabilities[o.Name]; ok {
                ov.Probability = fmt.Sprintf("%d%%", int(p*100+0.5))
            }
            ov.Chosen = a.Choice == o.Name
        }
        qv.Options = append(qv.Options, ov)
    }
    if a != nil {
        switch a.Kind {
        case decision.KindNoul:
            qv.Answer = fmt.Sprintf("так · %d%%", int(a.Yes*100+0.5))
        case decision.KindChoice:
            if p, ok := a.Probabilities[a.Choice]; ok {
                qv.Answer = fmt.Sprintf("%s · %d%%", a.Choice, int(p*100+0.5))
            } else {
                qv.Answer = fmt.Sprintf("%s · confidence %d%%", a.Choice, int(a.Confidence*100+0.5))
            }
        default:
            qv.Answer = string(a.Kind)
        }
    }
    return qv
}
```

- `answerJSON`:

```go
func answerJSON(a *decision.Answer) json.RawMessage {
    b, _ := json.Marshal(a) // the provider's answer when there is one
    return b
}
```

- `model()`:

```go
// model is the model ID the resolver's backend sends.
func (s *web) model() string {
    return s.resolver.Decider.Model()
}
```

`cmd/olxjev/web_debug_test.go`: apply the table. `ex := &exchange{d: &decision.Decision{Answers: map[string]*decision.Answer{…}}}`, `{Kind: decision.KindNoul, Yes: 0.98}`, and `map[string]decision.Question{…: {Kind: decision.KindChoice}}`.

- [ ] **Step 5: Verify nothing else imports the removed names**

Run: `grep -rn "SourceJev\|olxjev.Evaluator\|PricePerMInputTokens\|answerProbability\|fakeJev\b" --include=*.go . | grep -v "decision/"`
Expected: no output, apart from `fakeJevServer`/`fakeJevHandler` in `cmd/olxjev` (HTTP fakes, which keep their names).

Run: `grep -rln "go-jev" --include=*.go olxjev cmd/olxjev`
Expected: only `olxjev/decision/typesafe/*`, `olxjev/decision/subq/*`, `olxjev/decision/internal/systemone/*`, `olxjev/golden_test.go`, `cmd/olxjev/main.go`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go build ./... && go vet ./olxjev/... ./cmd/olxjev/ && go test -race ./olxjev/... ./cmd/olxjev/`
Expected: PASS. In particular, `TestRunText` still prints `cost: 2 Jev calls, 3000 input tokens, ≈$0.000126` (typesafe price, per-call costs summed), the `Stage2Plan` ⇔ `Resolve` pin passes, and so do the new `TestNewResolverCalibratedThresholds` and `TestResolveSourceIsModel`.

- [ ] **Step 7: Commit**

```bash
git add -A olxjev cmd/olxjev
git commit -m "olxjev: decide through decision.Decider; Jev becomes the typesafe adapter

FieldInfo.Source is \"model\" (was \"jev\"); usage reports the model.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: `-backend`, `-model`, model in the cost line, web wording

**Files:**
- Modify: `cmd/olxjev/main.go`, `cmd/olxjev/web.go`, `cmd/olxjev/debug.go`, `cmd/olxjev/templates/index.html`, `cmd/olxjev/templates/debug.html`
- Modify tests: `cmd/olxjev/main_test.go`, `cmd/olxjev/web_test.go`, `cmd/olxjev/web_cache_test.go`, `cmd/olxjev/web_debug_test.go`

**Interfaces:**
- Consumes: `typesafe.New/Name/DefaultURL`, `subq.New/Name/DefaultURL`, `olxjev.NewResolver`, `olxjev.CalibratedThresholds`.
- Produces:
  - `type backendOptions struct{ name, apiKey, endpoint, model string }`
  - `newResolver(b backendOptions, threshold float64) (*olxjev.Resolver, error)`
  - `costLine(u decision.Usage) string` returning `≈$%.6f (<model>, N call(s), M input tokens)`
  - flags `-backend` (default `typesafe`), `-endpoint` (default `""` = the backend's URL), `-model` (default `""`)

- [ ] **Step 1: Write the failing tests**

Update every `newResolver("", URL, th)` call in the cmd tests to `newResolver(backendOptions{name: typesafe.Name, endpoint: URL}, th)` (`main_test.go:28`, `web_cache_test.go:24`, `web_test.go:30`, `web_test.go:169`). Import `"feodosian.com/olxsearch/olxjev/decision/typesafe"` where needed.

In `main_test.go` `TestRunText`, change the expected cost line to `"cost: ≈$0.000126 (fake, 2 calls, 3000 input tokens)"`. The fake server reports `"model": "fake"`.

In `web_debug_test.go`, change the expected strings `"Запит Jev · етап 3"` → `"Запит моделі · етап 3"` and `"Відповідь Jev"` → `"Відповідь моделі"` (both places, lines ~48–50 and ~94).

Add to `main_test.go`:

```go
func TestRunBackendErrors(t *testing.T) {
    for _, c := range []struct {
        args []string
        want string
    }{
        {[]string{"-backend", "subq", "-q", "x"}, "SUBQ_API_KEY is not set"},
        {[]string{"-q", "x"}, "TYPESAFE_API_KEY is not set"},
        {[]string{"-backend", "nope", "-q", "x"}, `unknown -backend "nope" (typesafe or subq)`},
    } {
        var stdout, stderr bytes.Buffer
        code := run(c.args, nil, &stdout, &stderr, func(string) string { return "" })
        if code != 2 || !strings.Contains(stderr.String(), c.want) {
            t.Errorf("%v: exit %d, stderr %q; want 2 and %q", c.args, code, stderr.String(), c.want)
        }
    }
}

// A custom endpoint (a local or test server) needs no key, for either backend.
func TestRunSubqCustomEndpointNoKey(t *testing.T) {
    srv := fakeJevServer(t)
    defer srv.Close()
    var stdout, stderr bytes.Buffer
    code := run([]string{"-backend", "subq", "-endpoint", srv.URL, "-no-url", "-q", "зняти квартиру в Харкові"}, nil, &stdout, &stderr, func(string) string { return "" })
    if code != 0 || !strings.Contains(stdout.String(), "category = category_1760") {
        t.Errorf("exit %d:\n%s\n%s", code, stdout.String(), stderr.String())
    }
}

// -backend subq sends SubQ's model with SUBQ_API_KEY; -model overrides it.
func TestRunSubqModelAndKey(t *testing.T) {
    var mu sync.Mutex
    models, auths := map[string]bool{}, map[string]bool{}
    inner := fakeJevHandler(t, "category_1760")
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        body, _ := io.ReadAll(r.Body)
        var req struct {
            Model string `json:"model"`
        }
        json.Unmarshal(body, &req)
        mu.Lock()
        models[req.Model], auths[r.Header.Get("Authorization")] = true, true
        mu.Unlock()
        r.Body = io.NopCloser(bytes.NewReader(body))
        inner(w, r)
    }))
    defer srv.Close()
    getenv := func(k string) string {
        if k == "SUBQ_API_KEY" {
            return "sk-test"
        }
        return ""
    }
    var stdout, stderr bytes.Buffer
    run([]string{"-backend", "subq", "-endpoint", srv.URL, "-no-url", "-q", "зняти квартиру в Харкові"}, nil, &stdout, &stderr, getenv)
    run([]string{"-backend", "subq", "-model", "subq-snap-2", "-endpoint", srv.URL, "-no-url", "-q", "зняти квартиру в Харкові"}, nil, &stdout, &stderr, getenv)
    if !models["subq-snap-preview"] || !models["subq-snap-2"] || !auths["Bearer sk-test"] || len(auths) != 1 {
        t.Errorf("models %v, auths %v", models, auths)
    }
}

// Every backend the CLI offers has calibrated thresholds.
func TestBackendsAreCalibrated(t *testing.T) {
    for name := range backends {
        if _, ok := olxjev.CalibratedThresholds[name]; !ok && name != subq.Name {
            t.Errorf("backend %q has no CalibratedThresholds entry", name)
        }
    }
}
```

Imports for `main_test.go`: add `"io"`, `"sync"`, `"feodosian.com/olxsearch/olxjev/decision/subq"`, `"feodosian.com/olxsearch/olxjev/decision/typesafe"`. (`TestBackendsAreCalibrated` exempts `subq` until Task 8 adds its thresholds, and Task 8 removes the exemption.)

Add to `web_test.go`:

```go
// The card's cost names the model.
func TestWebCostShowsModel(t *testing.T) {
    w := get(t, newTestWeb(t, "category_1760", fakeBreadcrumbs{}), "/result?q="+url.QueryEscape("зняти квартиру в Харкові"))
    if body := w.Body.String(); !strings.Contains(body, "≈$0.000126 · fake") {
        t.Errorf("card cost lacks the model:\n%s", body)
    }
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/olxjev/ 2>&1 | head -20`
Expected: FAIL (undefined: backendOptions / backends; cost line and wording mismatches).

- [ ] **Step 3: Implement in `cmd/olxjev/main.go`**

Package doc usage lines:

```go
// Command olxjev resolves free-text real-estate queries into OLX.ua search
// URLs with a decision model (TypeSafe's Jev by default, SubQ's Snap with
// -backend subq). Usage:
//
//     TYPESAFE_API_KEY=... olxjev -q "хочу зняти однушку в Харкові до 10к"
//     SUBQ_API_KEY=... olxjev -backend subq -q "..."
//     olxjev -json < queries.txt
//     olxjev -serve 127.0.0.1:8080   # web UI, see web.go
```

Add after `output`:

```go
// backend is one decision provider the CLI can use.
type backend struct {
    keyEnv string // the API key's environment variable
    url    string // the default endpoint
    new    func(apiKey string, opts ...jev.ClientOption) decision.Decider
}

var backends = map[string]backend{
    typesafe.Name: {keyEnv: "TYPESAFE_API_KEY", url: typesafe.DefaultURL, new: typesafe.New},
    subq.Name:     {keyEnv: "SUBQ_API_KEY", url: subq.DefaultURL, new: subq.New},
}

// backendOptions select and configure the decision backend; empty endpoint
// and model mean the backend's defaults.
type backendOptions struct {
    name, apiKey, endpoint, model string
}
```

Flags (replace the `endpoint` flag; the `threshold` and `debug` help texts say "model" instead of "Jev"):

```go
    backendName := fs.String("backend", typesafe.Name, "decision backend: typesafe (Jev) or subq (SubQ Snap)")
    endpoint := fs.String("endpoint", "", "decision API endpoint (default: the backend's)")
    model := fs.String("model", "", "model ID (default: the backend's)")
```

Key check (replaces the `TYPESAFE_API_KEY` block):

```go
    b, ok := backends[*backendName]
    if !ok {
        fmt.Fprintf(stderr, "olxjev: unknown -backend %q (typesafe or subq)\n", *backendName)
        return 2
    }
    apiKey := getenv(b.keyEnv)
    if apiKey == "" && (*endpoint == "" || *endpoint == b.url) {
        fmt.Fprintf(stderr, "olxjev: %s is not set\n", b.keyEnv)
        return 2
    }

    resolver, err := newResolver(backendOptions{name: *backendName, apiKey: apiKey, endpoint: *endpoint, model: *model}, *threshold)
```

`newResolver`:

```go
// newResolver builds the composer, the decision backend and the resolver.
// A threshold > 0 replaces the backend's calibrated per-type cutoffs for
// every type but the multi-choice gate.
func newResolver(bo backendOptions, threshold float64) (*olxjev.Resolver, error) {
    b, ok := backends[bo.name]
    if !ok {
        return nil, fmt.Errorf("unknown backend %q", bo.name)
    }
    composer, err := olxform.NewComposer()
    if err != nil {
        return nil, err
    }
    opts := []jev.ClientOption{jev.WithTimeout(30 * time.Second), jev.WithMaxRetries(3)}
    if bo.endpoint != "" {
        opts = append(opts, jev.WithURL(bo.endpoint))
    }
    if bo.model != "" {
        opts = append(opts, jev.WithModel(bo.model))
    }
    resolver, err := olxjev.NewResolver(composer, b.new(bo.apiKey, opts...))
    if err != nil {
        return nil, err
    }
    if threshold > 0 {
        // every type falls back to Threshold; the gate only decides which
        // options are asked, so it keeps its calibrated cutoff
        resolver.Thresholds = olxjev.Thresholds{Gate: resolver.Thresholds.Gate}
        resolver.Threshold = threshold
    }
    return resolver, nil
}
```

`costLine`:

```go
// costLine summarises what a resolve spent on decisions:
// "≈$0.000126 (jev-1.14, 2 calls, 3000 input tokens)".
func costLine(u decision.Usage) string {
    calls := "calls"
    if u.Calls == 1 {
        calls = "call"
    }
    model := ""
    if u.Model != "" {
        model = u.Model + ", "
    }
    return fmt.Sprintf("≈$%.6f (%s%d %s, %d input tokens)", u.CostUSD, model, u.Calls, calls, u.InputTokens)
}
```

Imports: add `"feodosian.com/olxsearch/olxjev/decision/subq"`.

- [ ] **Step 4: Implement the web wording**

`cmd/olxjev/web.go`:
- `resolve()`: the cost case becomes

```go
    case out.Result != nil && out.Result.Usage.Calls > 0:
        view.Cost = fmt.Sprintf("≈$%.6f", out.Result.Usage.CostUSD)
        if m := out.Result.Usage.Model; m != "" {
            view.Cost += " · " + m
        }
```

- `logResolve`: `"no Jev answer"` → `"no model answer"`.
- `pageView`: add `Backend string // the decision backend, for the debug badge`; set it where `pageView{Debug: s.debug, …}` is built: `Backend: s.resolver.Decider.Name()`.
- Comments that say "Jev calls" or "Jev usage" now say "decision calls" / "decision usage".

`cmd/olxjev/templates/index.html` line 16:

```html
        {{if .Debug}}<span class="dbg-badge" title="-web-debug: під результатом — усі фільтри і питання моделі ({{.Backend}})">debug · {{.Backend}}</span>{{end}}
```

`cmd/olxjev/templates/debug.html`: line 47 `Jev про це поле не питають: у запиті немає чисел.` → `Про це поле не питають: у запиті немає чисел.`; lines 51, 58 and 65: `Запит Jev` → `Запит моделі`, `Відповідь Jev` → `Відповідь моделі`.

`cmd/olxjev/debug.go` notes:
- `"Етап 2 не виконувався (…): це питання, які отримав би Jev."` → `"…: це питання, які отримала б модель."`
- `"Jev ще не питали: це питання, які він отримає, якщо обрати цю категорію."` → `"Модель ще не питали: це питання, які вона отримає, якщо обрати цю категорію."`

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go vet ./cmd/olxjev/ && go test -race ./cmd/olxjev/ ./olxjev/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/olxjev
git commit -m "olxjev cmd: -backend typesafe|subq and -model; cost and debug panel name the model

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: live batch-invariance check on SubQ

**Files:**
- Create: `olxjev/batch_live_test.go`

**Interfaces:**
- Consumes: `subq.New`, `decision.Batched`, `(*Resolver).Stage2Plan`, `testComposer`.
- Produces: a live gate. Task 8 runs only if this passes.

- [ ] **Step 1: Write the live test**

`olxjev/batch_live_test.go`:

```go
package olxjev

import (
    "context"
    "math"
    "os"
    "sort"
    "testing"
    "time"

    "github.com/mattn/go-jev"

    "feodosian.com/olxsearch/olxjev/decision"
    "feodosian.com/olxsearch/olxjev/decision/subq"
)

// TestLiveBatchInvariance checks that Batched may split a stage: the same
// questions answered in chunks of 16 and of 5 must agree within run-to-run
// noise (Jev's observed spread, 0.07), i.e. SubQ judges each question on
// its own. It also measures the noise by repeating the 16-chunk run.
// Run: OLXJEV_LIVE=1 SUBQ_API_KEY=... go test -run TestLiveBatchInvariance -v ./olxjev/
func TestLiveBatchInvariance(t *testing.T) {
    key := os.Getenv("SUBQ_API_KEY")
    if os.Getenv("OLXJEV_LIVE") != "1" || key == "" {
        t.Skip("set OLXJEV_LIVE=1 and SUBQ_API_KEY (spends API credits)")
    }
    r, err := NewResolver(testComposer(t), nil)
    if err != nil {
        t.Fatal(err)
    }
    big := subq.New(key, jev.WithTimeout(60*time.Second), jev.WithMaxRetries(3)) // chunks of 16
    small := decision.Batched(big, 5)                                          // chunks of 5
    cases := []struct{ q, leaf string }{
        {"хочу зняти однушку квартиру в Харкові до 10к гривень в центрі з котом", "category_1760"},
        {"купити трикімнатну квартиру в Дніпрі від 60 до 80 тис доларів з ремонтом і балконом", "category_1758"},
        {"зняти двушку на Оболоні до 20 тисяч без комісії, можна з дітьми", "category_1760"},
    }
    worst, noise := 0.0, 0.0
    for _, c := range cases {
        state, plans, err := r.Stage2Plan(c.q, c.leaf, nil)
        if err != nil {
            t.Fatal(err)
        }
        qs := map[string]decision.Question{}
        for _, p := range plans {
            for k, q := range p.Questions {
                qs[k] = q
            }
            for k, q := range p.Options {
                qs[k] = q
            }
        }
        ctx := context.Background()
        a16, err := big.Decide(ctx, state, qs)
        if err != nil {
            t.Fatal(err)
        }
        b16, err := big.Decide(ctx, state, qs)
        if err != nil {
            t.Fatal(err)
        }
        a5, err := small.Decide(ctx, state, qs)
        if err != nil {
            t.Fatal(err)
        }
        d, top := maxDelta(a16, a5)
        n, _ := maxDelta(a16, b16)
        worst, noise = math.Max(worst, d), math.Max(noise, n)
        t.Logf("%q: %d questions, calls 16→%d 5→%d, max Δ(16 vs 5) %.3f, max Δ(16 vs 16) %.3f; largest: %v",
            c.q, len(qs), a16.Usage.Calls, a5.Usage.Calls, d, n, top)
    }
    t.Logf("overall: max Δ(16 vs 5) %.3f, run-to-run noise %.3f", worst, noise)
    if worst > 0.07 {
        t.Errorf("batching changes answers by %.3f (> 0.07): do not calibrate, report back", worst)
    }
}

// maxDelta is the largest probability difference between two decisions
// over every answer (a noul's Yes, every option of a choice), with the five
// largest as "key=Δ".
func maxDelta(a, b *decision.Decision) (float64, []string) {
    type kd struct {
        key string
        d   float64
    }
    all := []kd{}
    for k, x := range a.Answers {
        y := b.Answers[k]
        if y == nil {
            all = append(all, kd{k + "(missing)", 1})
            continue
        }
        d := math.Abs(x.Yes - y.Yes)
        for o, p := range x.Probabilities {
            d = math.Max(d, math.Abs(p-y.Probabilities[o]))
        }
        all = append(all, kd{k, d})
    }
    sort.Slice(all, func(i, j int) bool { return all[i].d > all[j].d })
    top := []string{}
    for i := 0; i < len(all) && i < 5; i++ {
        top = append(top, all[i].key+"="+strconv.FormatFloat(all[i].d, 'f', 3, 64))
    }
    if len(all) == 0 {
        return 0, top
    }
    return all[0].d, top
}
```

Add `"strconv"` to the imports.

- [ ] **Step 2: Verify it compiles and skips without credentials**

Run: `go test -run TestLiveBatchInvariance -v ./olxjev/`
Expected: `--- SKIP` with "set OLXJEV_LIVE=1 and SUBQ_API_KEY".

- [ ] **Step 3: Run it live** (key from the user, typed on the command line only)

Run: `OLXJEV_LIVE=1 SUBQ_API_KEY=<key> go test -run TestLiveBatchInvariance -v -timeout 10m ./olxjev/`
Expected: PASS, with the log showing `max Δ(16 vs 5)` ≤ 0.07 and the measured noise. **If it fails, stop:** report the deltas to the user and do not start Task 8.

- [ ] **Step 4: Commit** (record the measured numbers in the commit message)

```bash
git add olxjev/batch_live_test.go
git commit -m "olxjev: live check that SubQ answers do not depend on batching

Measured 2026-10-18: max Δ(16 vs 5) <X>, run-to-run noise <Y>.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

Replace `<X>` and `<Y>` with the logged values before committing.

---

### Task 8: calibrate SubQ thresholds, smoke-test, document

**Files:**
- Modify: `olxjev/resolver.go` (`CalibratedThresholds`, comment)
- Modify: `cmd/olxjev/main_test.go` (drop the subq exemption in `TestBackendsAreCalibrated`)
- Modify: `CLAUDE.md` (OlxJev section), `docs/superpowers/specs/2026-10-14-olxjev-design.md` (pointer)

**Interfaces:**
- Consumes: `TestGolden` with `OLXJEV_BACKEND=subq` (Task 5), the CLI and web from Task 6.
- Produces: `CalibratedThresholds["subq"]`.

- [ ] **Step 1: Run the golden corpus twice on SubQ**

Run (twice, saving each log to the scratchpad, never to the repo):

```bash
OLXJEV_LIVE=1 OLXJEV_BACKEND=subq SUBQ_API_KEY=<key> go test -run TestGolden -v -timeout 30m ./olxjev/ > "$SCRATCH/golden-subq-1.log" 2>&1
OLXJEV_LIVE=1 OLXJEV_BACKEND=subq SUBQ_API_KEY=<key> go test -run TestGolden -v -timeout 30m ./olxjev/ > "$SCRATCH/golden-subq-2.log" 2>&1
```

`$SCRATCH` is the session scratchpad directory. Expected: each log has "field accuracy:", the calibration table (`calibration — per type: …`) and the usage line. The test may fail on `goldenFloors` with thresholds not yet calibrated; that's fine, since the calibration table is graded on raw answers.

- [ ] **Step 2: Choose the SubQ thresholds by the Jev rules**

From the two calibration tables (rows `Category`, `Gate`, `Multi`, `Number`, `Place`, `Presence`, `Choice`, each `cutoff=kept:through`), pick per type, taking the stricter of the two runs:

- **Number:** the lowest cutoff where `through` = 0 in both runs. A wrong number narrows the search.
- **Category, Place:** the lowest cutoff where `through` = 0 in both runs. If none reaches 0, take the cutoff that minimizes `through` and then maximizes `kept`.
- **Choice, Presence:** the lowest cutoff with `through` ≤ 1 in both runs, and never below 0.5 unless kept rises by ≥ 2 and through stays 0.
- **Multi:** the cutoff halfway between the highest wrong-yes probability and the lowest right-yes probability ("wrong max" / "right min" in the row header), rounded to 0.05. If they overlap, the cutoff that minimizes `through` + (right answers lost).
- **Gate:** the highest cutoff that keeps every wanted field's gate in both runs (no "gate said no" `missed` lines that a lower cutoff would fix), capped at 0.3 (Jev's value), since a wrong yes costs only stage-3 tokens.

Add to `olxjev/resolver.go` the entry and the evidence comment (fill in the measured values, written the way the `DefaultThresholds` comment is):

```go
// CalibratedThresholds are the thresholds per decision backend (Decider
// Name), each calibrated on the golden corpus; NewResolver falls back to
// DefaultThresholds for a backend missing here.
//
// subq (subq-snap-preview), golden ×2, 2026-10-18: <per type: right answers
// kept / wrong let through at the chosen cutoff and the nearest wrong
// answer; anything that differs from Jev>.
var CalibratedThresholds = map[string]Thresholds{
    "typesafe": DefaultThresholds,
    "subq":     {Category: <c>, Place: <p>, Presence: <pr>, Choice: <ch>, Number: <n>, Multi: <m>, Gate: <g>},
}
```

Every `<…>` here stands for a measured value and must be replaced before committing. Nothing is left as a placeholder.

In `cmd/olxjev/main_test.go` `TestBackendsAreCalibrated`, remove `&& name != subq.Name` (and the `subq` import if it's now unused).

- [ ] **Step 3: Confirm the floors with the new thresholds**

Run: `OLXJEV_LIVE=1 OLXJEV_BACKEND=subq SUBQ_API_KEY=<key> go test -run TestGolden -v -timeout 30m ./olxjev/ > "$SCRATCH/golden-subq-3.log" 2>&1; tail -40 "$SCRATCH/golden-subq-3.log"`
Expected: PASS (all `goldenFloors` met). If a floor fails, report the field accuracy against Jev's to the user instead of loosening floors.

- [ ] **Step 4: Smoke-test the CLI and the web UI**

```bash
SUBQ_API_KEY=<key> go run ./cmd/olxjev -backend subq -q "хочу зняти однушку квартиру в Харкові до 10к гривень в центрі"
```

Expected: `category = category_1760`, `city = 280`, a `url:` line and `cost: ≈$… (subq-snap-preview, N calls, … input tokens)`.

```bash
SUBQ_API_KEY=<key> go run ./cmd/olxjev -backend subq -serve 127.0.0.1:8099 -web-debug &
curl -s 'http://127.0.0.1:8099/result?q=купити%20трикімнатну%20квартиру%20в%20Дніпрі%20від%2060%20до%2080%20тис%20доларів' | grep -o '≈\$[0-9.]* · [a-z-]*'
curl -s 'http://127.0.0.1:8099/' | grep -o 'debug · subq'
kill %1
```

Expected: `≈$0.000… · subq-snap-preview` and `debug · subq`.

- [ ] **Step 5: Run the whole suite**

Run: `go build ./... && go vet ./olxjev/... ./cmd/olxjev/ && go test -race ./olxjev/... ./cmd/olxjev/`
Expected: PASS.

- [ ] **Step 6: Document**

In `CLAUDE.md` § OlxJev:
- Retitle the intro so it isn't Jev-only: "using a decision model (TypeSafe's Jev by default, SubQ's Snap with `-backend subq`) to make the wizard's choices".
- Add a bullet group after the Thresholds bullet:

```markdown
- Backends (`olxjev/decision`, spec `docs/superpowers/specs/2026-10-18-olxjev-decision-backends-design.md`): the resolver talks to a
  `decision.Decider`; adapters `decision/typesafe` (Jev, `TYPESAFE_API_KEY`, $0.042/Mtok) and `decision/subq` (SubQ Decisions
  API, `subq-snap-preview`, `SUBQ_API_KEY`, $0.05/Mtok). Both speak the same System One protocol over go-jev's transport;
  SubQ accepts at most 16 questions per request, so `decision.Batched` splits stages 2/3 into concurrent chunks (sorted keys;
  live check 2026-10-18: max Δ <X> between chunks of 16 and 5, noise <Y>). `CLI -backend typesafe|subq`, `-model`, `-endpoint`.
  Thresholds per backend in `olxjev.CalibratedThresholds` (by `Decider.Name()`); SubQ golden ×2 (2026-10-18): <accuracy, tokens,
  calls, time and cost per query vs Jev>.
- Live golden on SubQ: `OLXJEV_LIVE=1 OLXJEV_BACKEND=subq SUBQ_API_KEY=... go test -run TestGolden -v ./olxjev/`.
```

- Update the CLI bullet (add `-backend`, `-model`), the Cost bullet (prices per backend; `Result.Usage.Model`; `CostUSD` is computed by the adapter), and `FieldInfo.Source` wherever it is mentioned.
- Add `subq` to the `cmd/` table row for `olxjev`: "… via Jev or SubQ (`-backend`)".

In `docs/superpowers/specs/2026-10-14-olxjev-design.md`, under the header, add:

```markdown
Backends: see `2026-10-18-olxjev-decision-backends-design.md` (Jev or SubQ via `decision.Decider`).
```

Fill in every `<…>` with measured values from Steps 1–3 and Task 7.

- [ ] **Step 7: Verify no secret leaked, then commit**

Run: `git diff --cached -U0 | grep -n "sk-" ; git diff -U0 | grep -n "sk-"`
Expected: no output.

```bash
git add olxjev/resolver.go cmd/olxjev/main_test.go CLAUDE.md docs/superpowers/specs/2026-10-14-olxjev-design.md
git commit -m "olxjev: SubQ thresholds calibrated on the golden corpus; docs for backends

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
