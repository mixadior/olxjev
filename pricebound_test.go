package olxjev

import (
    "context"
    "strings"
    "testing"

    "github.com/mixadior/olxjev/decision"
)

func TestExtractNumbersLowerBound(t *testing.T) {
    cases := []struct {
        text  string
        lower map[string]bool // number -> marked as a lower bound
    }{
        {"квартира в Харкові за 10к", map[string]bool{"10000": false}},
        {"квартира 10000 грн", map[string]bool{"10000": false}},
        {"квартира до 10000 грн", map[string]bool{"10000": false}},
        {"квартира від 8000 грн", map[string]bool{"8000": true}},
        {"квартира от 8000 грн", map[string]bool{"8000": true}},
        {"квартира не менше 8000 грн", map[string]bool{"8000": true}},
        {"площа більше 50 м2", map[string]bool{"50": true}},
        {"дорожче 50 тис", map[string]bool{"50000": true}},
        {"від 8 до 12 тис", map[string]bool{"8000": true, "12000": false}},
        {"8-12к", map[string]bool{"8000": true, "12000": false}},
    }
    for _, c := range cases {
        got := map[string]bool{}
        for _, m := range ExtractNumbers(c.text) {
            got[m.Name()] = m.lowerBound
        }
        for name, want := range c.lower {
            if v, ok := got[name]; !ok || v != want {
                t.Errorf("%q: %s lowerBound = %v (found %v), want %v", c.text, name, v, ok, want)
            }
        }
    }
}

// A price without «від»/«до» is a budget: the user wants it or cheaper.
func TestResolveBarePriceIsMax(t *testing.T) {
    for _, stage2 := range []map[string]*decision.Answer{
        {"filter_float_price:from": choice("10000", 0.7)},
        {"filter_float_price:from": choice("10000", 0.7), "filter_float_price:to": choice("10000", 0.6)},
    } {
        f := &fakeDecider{answers: []map[string]*decision.Answer{flatRentKharkiv(), stage2}}
        res, err := newTestResolver(t, f).Resolve(context.Background(), "квартира в Харкові за 10000 грн")
        if err != nil {
            t.Fatalf("Resolve: %v", err)
        }
        if res.Values["filter_float_price:to"] != "10000" {
            t.Errorf("jev %v: price:to = %q, want 10000", keysOf(stage2), res.Values["filter_float_price:to"])
        }
        if v, ok := res.Values["filter_float_price:from"]; ok {
            t.Errorf("jev %v: price:from = %q, want unset", keysOf(stage2), v)
        }
        if f := res.Fields["filter_float_price:to"]; f.Label == "" || f.Probability == 0 {
            t.Errorf("price:to field = %+v, want label and confidence carried over", f)
        }
    }
}

func TestResolveMarkedPriceStaysMin(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{
        flatRentKharkiv(),
        {"filter_float_price:from": choice("10000", 0.7), "filter_float_price:to": choice("10000", 0.6)},
    }}
    res, err := newTestResolver(t, f).Resolve(context.Background(), "квартира в Харкові від 10000 грн")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if res.Values["filter_float_price:from"] != "10000" {
        t.Errorf("price:from = %q, want 10000", res.Values["filter_float_price:from"])
    }
    if v, ok := res.Values["filter_float_price:to"]; ok {
        t.Errorf("price:to = %q, want unset: «від 10000» is a minimum", v)
    }
}

// Only price reads a bare number as a maximum; «40 м2» is not «до 40 м2».
func TestResolveBareAreaKeepsJevBound(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{
        flatRentKharkiv(),
        {"filter_float_total_area:from": choice("40", 0.7)},
    }}
    res, err := newTestResolver(t, f).Resolve(context.Background(), "квартира в Харкові 40 м2")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if res.Values["filter_float_total_area:from"] != "40" {
        t.Errorf("total_area:from = %q, want 40", res.Values["filter_float_total_area:from"])
    }
}

func TestStage2PriceInstructionsReadBareNumberAsMax(t *testing.T) {
    elements := []pendingElement{
        {Key: "filter_float_price:from", Title: "Ціна , від", Kind: kindNumber},
        {Key: "filter_float_price:to", Title: "Ціна , до", Kind: kindNumber},
    }
    qs := stage2Questions(elements, ExtractNumbers("за 10к"))
    to := qs["filter_float_price:to"].Instructions
    from := qs["filter_float_price:from"].Instructions
    if !strings.Contains(to, "без «від»") || !strings.Contains(from, "лише") {
        t.Errorf("price bounds must say a bare price is the maximum:\nto: %s\nfrom: %s", to, from)
    }
}

func keysOf(m map[string]*decision.Answer) []string {
    keys := []string{}
    for k := range m {
        keys = append(keys, k)
    }
    return keys
}
