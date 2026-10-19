package main

import (
    "bytes"
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "sync"
    "testing"

    "github.com/mixadior/olxjev"
    "github.com/mixadior/olxjev/decision/typesafe"
)

// -threshold overrides the calibrated per-type cutoffs for every type.
func TestRunThresholdOverridesAllTypes(t *testing.T) {
    srv := fakeJevServer(t)
    defer srv.Close()
    var stdout, stderr bytes.Buffer
    code := run([]string{"-endpoint", srv.URL, "-no-url", "-threshold", "0.99", "-q", "зняти квартиру в Харкові"}, nil, &stdout, &stderr, func(string) string { return "" })
    if code != 1 || !strings.Contains(stdout.String(), "category unresolved") {
        t.Errorf("exit %d, want 1 and the category (0.95) rejected at 0.99:\n%s", code, stdout.String())
    }
}

// The gate cutoff decides which options are asked, not which answers are
// accepted: -threshold leaves it calibrated.
func TestThresholdKeepsGate(t *testing.T) {
    resolver, err := newResolver(backendOptions{name: typesafe.Name, endpoint: "http://127.0.0.1:0"}, 0.7)
    if err != nil {
        t.Fatalf("newResolver: %v", err)
    }
    if resolver.Thresholds.Gate != olxjev.DefaultThresholds.Gate || resolver.Threshold != 0.7 || resolver.Thresholds.Multi != 0 {
        t.Errorf("Threshold %v, Thresholds %+v: want 0.7 for every type but Gate %v", resolver.Threshold, resolver.Thresholds, olxjev.DefaultThresholds.Gate)
    }
}

func TestRunCategoryCandidates(t *testing.T) {
    srv := fakeJevServer(t)
    defer srv.Close()
    var stdout, stderr bytes.Buffer
    code := run([]string{"-endpoint", srv.URL, "-no-url", "-q", "купити оселю в Києві"}, nil, &stdout, &stderr, func(string) string { return "" })
    out := stdout.String()
    if code != 1 || !strings.Contains(out, "category candidate = category_1758  [Нерухомість › Квартири › Продаж квартир, 0.44]") ||
        !strings.Contains(out, "category candidate = category_1602") {
        t.Errorf("exit %d, want 1 and both candidates listed:\n%s", code, out)
    }
}

func TestRunLocationUnresolved(t *testing.T) {
    srv := fakeJevServer(t)
    defer srv.Close()
    var stdout, stderr bytes.Buffer
    code := run([]string{"-endpoint", srv.URL, "-no-url", "-q", "зняти квартиру в Іванівці"}, nil, &stdout, &stderr, func(string) string { return "" })
    out := stdout.String()
    if code != 1 || !strings.Contains(out, "error: ") || !strings.Contains(out, "область") {
        t.Errorf("exit %d, want 1 and an error asking for the region:\n%s", code, out)
    }
    if !strings.Contains(out, "city candidate = ") || !strings.Contains(out, "[Іванів") {
        t.Errorf("output must list the shortlisted places:\n%s", out)
    }
}

// fakeJevServer answers category=category_1760 and city=280 when offered,
// "none" for every other choice and 0 for every noul.
func fakeJevServer(t *testing.T) *httptest.Server {
    t.Helper()
    return fakeJevServerCategory(t, "category_1760")
}

// fakeJevServerCategory is fakeJevServer answering category with the given choice.
func fakeJevServerCategory(t *testing.T, category string) *httptest.Server {
    t.Helper()
    return httptest.NewServer(fakeJevHandler(t, category))
}

// fakeJevHandler is fakeJevServerCategory's handler.
func fakeJevHandler(t *testing.T, category string) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var body struct {
            State     json.RawMessage `json:"state"`
            Questions map[string]struct {
                Type     string          `json:"type"`
                Criteria json.RawMessage `json:"criteria"`
            } `json:"questions"`
        }
        if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
            t.Errorf("decode request: %v", err)
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        answers := map[string]map[string]any{}
        for key, q := range body.Questions {
            switch {
            case key == olxjev.QuestionCategory && strings.Contains(string(body.State), "оселю"):
                // flat or house: split below the cutoff
                answers[key] = map[string]any{"type": "choice", "choice": "category_1758", "confidence": 0.42,
                    "probabilities": map[string]float64{"category_1758": 0.44, "category_1602": 0.38, "none": 0.18}}
            case key == olxjev.QuestionPlace && strings.Contains(string(body.State), "Іванівці"):
                answers[key] = map[string]any{"type": "noul", "noul": 0.9} // a place no city matches
            case (key == "gate:filter_enum_pets" || key == "filter_enum_pets#yes_cat") && strings.Contains(string(body.State), "котом"):
                answers[key] = map[string]any{"type": "noul", "noul": 0.9} // the pets gate passes, stage 3 asks the species
            case q.Type == "noul":
                answers[key] = map[string]any{"type": "noul", "noul": 0.0}
            case key == "category":
                answers[key] = map[string]any{"type": "choice", "choice": category, "confidence": 0.95}
            case key == "city" && strings.Contains(string(q.Criteria), `"280"`):
                answers[key] = map[string]any{"type": "choice", "choice": "280", "confidence": 0.9}
            default:
                answers[key] = map[string]any{"type": "choice", "choice": "none", "confidence": 0.9}
            }
        }
        json.NewEncoder(w).Encode(map[string]any{"model": "fake", "answers": answers,
            "usage": map[string]int{"input_tokens": 1500, "output_tokens": 5}})
    }
}

func TestRunText(t *testing.T) {
    srv := fakeJevServer(t)
    defer srv.Close()
    var stdout, stderr bytes.Buffer
    code := run([]string{"-endpoint", srv.URL, "-no-url", "-q", "зняти квартиру в Харкові"}, nil, &stdout, &stderr, func(string) string { return "" })
    if code != 0 {
        t.Fatalf("exit %d, stderr: %s", code, stderr.String())
    }
    out := stdout.String()
    for _, want := range []string{"category = category_1760", "city = 280  [Харків, Харківська область", "region = 8",
        "cost: ≈$0.000126 (fake, 2 calls, 3000 input tokens)"} {
        if !strings.Contains(out, want) {
            t.Errorf("output missing %q:\n%s", want, out)
        }
    }
}

func TestRunJSONFromStdin(t *testing.T) {
    srv := fakeJevServer(t)
    defer srv.Close()
    var stdout, stderr bytes.Buffer
    stdin := strings.NewReader("зняти квартиру в Харкові\n\nзняти квартиру у Львові\n")
    code := run([]string{"-endpoint", srv.URL, "-no-url", "-json"}, stdin, &stdout, &stderr, func(string) string { return "" })
    if code != 0 {
        t.Fatalf("exit %d, stderr: %s", code, stderr.String())
    }
    lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
    if len(lines) != 2 {
        t.Fatalf("want 2 JSON lines (blank line skipped), got %d:\n%s", len(lines), stdout.String())
    }
    var first output
    if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if first.Result == nil || first.Result.Values["city"] != "280" {
        t.Errorf("first result = %+v", first.Result)
    }
    if u := first.Result.Usage; u.Calls != 2 || u.InputTokens != 3000 || u.CostUSD == 0 {
        t.Errorf("usage = %+v, want 2 calls, 3000 input tokens and a cost", u)
    }
}

func TestRunRequiresAPIKeyForTypeSafe(t *testing.T) {
    var stdout, stderr bytes.Buffer
    code := run([]string{"-q", "x"}, nil, &stdout, &stderr, func(string) string { return "" })
    if code != 2 || !strings.Contains(stderr.String(), "TYPESAFE_API_KEY") {
        t.Errorf("exit %d, stderr %q; want 2 and a TYPESAFE_API_KEY hint", code, stderr.String())
    }
}

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
        if _, ok := olxjev.CalibratedThresholds[name]; !ok {
            t.Errorf("backend %q has no CalibratedThresholds entry", name)
        }
    }
}
