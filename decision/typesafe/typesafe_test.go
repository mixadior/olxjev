package typesafe

import (
    "context"
    "io"
    "math"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/mattn/go-jev"

    "github.com/mixadior/olxjev/decision"
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
        "place":      {Kind: decision.KindNoul, Instructions: "Чи названо місце?"},
        "gate:pets":  {Kind: decision.KindNoul, Instructions: "Тварини (варіанти: кіт; собака): чи каже запит?"},
        "nulloption": {Kind: decision.KindChoice, Instructions: "x", Options: []decision.Option{{Name: "a", Desc: nil}, {Name: "b", Desc: "b"}}},
    }
    theirs := map[string]jev.Question{
        "category": {Type: "choice", Instructions: instr, Criteria: jev.Options{
            {Name: "category_3713", Desc: desc}, {Name: "category_1760", Desc: "Квартири › Довгострокова"}, {Name: "none", Desc: "не вказано в запиті"},
        }},
        "place":      {Type: "noul", Instructions: "Чи названо місце?"},
        "gate:pets":  {Type: "noul", Instructions: "Тварини (варіанти: кіт; собака): чи каже запит?"},
        "nulloption": {Type: "choice", Instructions: "x", Criteria: jev.Options{{Name: "a", Desc: nil}, {Name: "b", Desc: "b"}}},
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

// The cost follows the model that answered: OpenCode's "-free" models cost
// nothing, the paid Jev (from TypeSafe or OpenCode) $0.042/Mtok; without a
// model in the response the client's model is priced.
func TestCostFollowsModel(t *testing.T) {
    for _, tc := range []struct {
        respModel, clientModel string
        want                   float64
    }{
        {"jev-1.13-free", "", 0},
        {"jev-1.13", OpenCodeModel, 2000 * PricePerMInputTokens / 1e6},
        {"", OpenCodeModel, 0},
        {"", "jev-1.13", 2000 * PricePerMInputTokens / 1e6},
    } {
        srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.Write([]byte(`{"model":"` + tc.respModel + `","answers":{},"usage":{"input_tokens":2000,"output_tokens":60}}`))
        }))
        opts := []jev.ClientOption{jev.WithURL(srv.URL)}
        if tc.clientModel != "" {
            opts = append(opts, jev.WithModel(tc.clientModel))
        }
        d, err := New("", opts...).Decide(context.Background(), "q", map[string]decision.Question{"x": {Kind: decision.KindNoul, Instructions: "?"}})
        srv.Close()
        if err != nil {
            t.Fatal(err)
        }
        if math.Abs(d.Usage.CostUSD-tc.want) > 1e-12 {
            t.Errorf("response model %q, client model %q: cost %g, want %g", tc.respModel, tc.clientModel, d.Usage.CostUSD, tc.want)
        }
    }
}

// OpenCode Zen serves Jev over the same protocol (opencode.ai/docs/zen, 2026-10-18).
func TestOpenCodeConstants(t *testing.T) {
    if OpenCodeURL != "https://opencode.ai/inference/systemone/v1/systemone" || OpenCodeModel != "jev-1.13-free" {
        t.Errorf("OpenCodeURL %q, OpenCodeModel %q", OpenCodeURL, OpenCodeModel)
    }
}

// A response without answers is an error, as Resolve treats a single call:
// merged into a batch it would otherwise leave every filter silently unset
// (and the web cache would keep that as the query's answer).
func TestDecideRejectsMissingAnswers(t *testing.T) {
    for _, body := range []string{`{"usage":{"input_tokens":5}}`, `{"answers":null,"usage":{"input_tokens":5}}`} {
        srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.Write([]byte(body))
        }))
        d, err := New("", jev.WithURL(srv.URL)).Decide(context.Background(), "q", map[string]decision.Question{"x": {Kind: decision.KindNoul, Instructions: "?"}})
        srv.Close()
        if err == nil || d != nil {
            t.Errorf("%s: Decide = %v, %v; want an error", body, d, err)
        }
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
