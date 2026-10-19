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

    "github.com/mixadior/olxjev/decision"
)

func nouls(n int) map[string]decision.Question {
    qs := map[string]decision.Question{}
    for i := 0; i < n; i++ {
        qs[fmt.Sprintf("q%02d", i)] = decision.Question{Kind: decision.KindNoul, Instructions: "?"}
    }
    return qs
}

// fakeSubq answers every noul with 0.5 and records each request; like the
// real API it rejects more than MaxQuestions questions.
type fakeSubq struct {
    mu         sync.Mutex
    auth       []string
    models     []string
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
