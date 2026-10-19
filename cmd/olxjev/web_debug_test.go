package main

import (
    "io"
    "net/http"
    "net/url"
    "strings"
    "testing"

    "github.com/mixadior/olxjev"
    "github.com/mixadior/olxjev/decision/typesafe"
    "github.com/mixadior/olxjev/decision"
)

func newDebugWeb(t *testing.T, debug bool) http.Handler {
    t.Helper()
    srv := fakeJevServer(t)
    t.Cleanup(srv.Close)
    resolver, err := newResolver(backendOptions{name: typesafe.Name, endpoint: srv.URL}, 0)
    if err != nil {
        t.Fatalf("newResolver: %v", err)
    }
    h, err := newWeb([]*olxjev.Resolver{resolver}, fakeBreadcrumbs{}, io.Discard, webOptions{cacheSize: 10, debug: debug})
    if err != nil {
        t.Fatalf("newWeb: %v", err)
    }
    return h
}

// The debug panel lists every filter of the resolved category with its
// description, the raw Jev request and Jev's answer.
func TestWebDebugListsFilters(t *testing.T) {
    h := newDebugWeb(t, true)
    for _, target := range []string{"/result?q=", "/?q="} {
        body := get(t, h, target+url.QueryEscape("зняти квартиру в Харкові до 10к")).Body.String()
        for _, want := range []string{
            `class="debug"`,
            "Етап 1",
            "Довгострокова оренда квартир",
            "filter_float_price:to", // a number field, asked: the query has a number
            "filter_enum_pets#yes_cat",
            "gate:filter_enum_pets",
            "Ціна, до",
            "який варіант відповідає запиту",     // a choice's instructions
            "чи каже запит хоч щось",           // a multi-choice gate
            "чи підходить користувачу варіант", // a multi option's noul, listed though not asked
            "не згадано · так · 0%",            // the pets gate said no
            "Запит моделі · етап 3",               // the options' request
            "jev-latest",                       // the raw request
            "Відповідь моделі",                    // the raw answer
        } {
            if !strings.Contains(body, want) {
                t.Errorf("%s: debug panel lacks %q", target, want)
            }
        }
    }
}

// A passed gate: the options' stage-3 answers reach the panel.
func TestWebDebugShowsStage3(t *testing.T) {
    body := get(t, newDebugWeb(t, true), "/result?q="+url.QueryEscape("зняти квартиру в Харкові з котом")).Body.String()
    for _, want := range []string{"Так, котик 😸 · 90%", "filter_enum_pets#yes_cat</code> <span class=\"dbg-kind\">noul</span> <span class=\"dbg-answer\">так · 90%"} {
        if !strings.Contains(body, want) {
            t.Errorf("debug panel lacks %q", want)
        }
    }
}

// Low-confidence categories get their filters listed too, as the questions
// Jev would get, without answers.
func TestWebDebugListsCandidateCategories(t *testing.T) {
    body := get(t, newDebugWeb(t, true), "/result?q="+url.QueryEscape("купити оселю в Києві")).Body.String()
    for _, want := range []string{
        "Квартири › Продаж квартир",
        "Будинки › Продаж будинків",
        "низька ймовірність",
        "filter_float_total_area:from",
    } {
        if !strings.Contains(body, want) {
            t.Errorf("debug panel lacks %q", want)
        }
    }
    if strings.Count(body, `class="dbg-category`) != 2 {
        t.Errorf("want 2 candidate categories, got %d", strings.Count(body, `class="dbg-category`))
    }
}

// A cached answer keeps its Jev exchanges.
func TestWebDebugSurvivesCache(t *testing.T) {
    h := newDebugWeb(t, true)
    q := "/result?q=" + url.QueryEscape("зняти квартиру в Харкові")
    get(t, h, q)
    body := get(t, h, q).Body.String()
    if !strings.Contains(body, "з кешу") || !strings.Contains(body, "Відповідь моделі") {
        t.Errorf("cached card lacks the cache mark or the Jev answers:\n%s", body)
    }
}

func TestWebNoDebugByDefault(t *testing.T) {
    body := get(t, newDebugWeb(t, false), "/result?q="+url.QueryEscape("зняти квартиру в Харкові")).Body.String()
    if strings.Contains(body, `class="debug"`) || strings.Contains(body, "jev-latest") {
        t.Error("the debug panel must be off without -web-debug")
    }
}

func TestRunWebDebugFlag(t *testing.T) {
    var stdout, stderr strings.Builder
    code := run([]string{"-web-debug", "-endpoint", "http://127.0.0.1:0", "-serve", "127.0.0.1:-1"}, nil, &stdout, &stderr, func(string) string { return "" })
    if code == 2 || strings.Contains(stderr.String(), "flag provided but not defined") {
        t.Errorf("-web-debug must be a known flag: exit %d, %s", code, stderr.String())
    }
}

// A number shows its value, not the query window it was read from; the
// place noul is no field, so it is shown as Jev's answer alone.
func TestDebugOutcomes(t *testing.T) {
    resolver, err := newResolver(backendOptions{name: typesafe.Name, endpoint: "http://127.0.0.1:0"}, 0)
    if err != nil {
        t.Fatalf("newResolver: %v", err)
    }
    s := &web{}
    res := &olxjev.Result{Fields: map[string]olxjev.FieldInfo{
        "filter_float_price:to": {Value: "10000", Label: "Харкові до 10к, з", Probability: 0.99},
    }}
    ex := &exchange{d: &decision.Decision{Answers: map[string]*decision.Answer{
        "filter_float_price:to": {Kind: decision.KindChoice, Choice: "n10000", Probabilities: map[string]float64{"n10000": 0.99}},
        olxjev.QuestionPlace:     {Kind: decision.KindNoul, Yes: 0.98},
    }}}
    price := s.filterView(resolver, "filter_float_price:to", "Ціна, до", "number",
        map[string]decision.Question{"filter_float_price:to": {Kind: decision.KindChoice}}, nil, nil, ex, res)
    if price.Outcome != "10\u00a0000 · 99%" {
        t.Errorf("price outcome = %q, want «10 000 · 99%%»", price.Outcome)
    }
    place := s.filterView(resolver, olxjev.QuestionPlace, "Чи названо місце", "noul",
        map[string]decision.Question{olxjev.QuestionPlace: {Kind: decision.KindNoul}}, nil, nil, ex, res)
    if place.Outcome != "так · 98%" {
        t.Errorf("place outcome = %q, want «так · 98%%»", place.Outcome)
    }
}
