package olxjev

import (
    "context"
    "errors"
    "math"
    "testing"

    "github.com/mixadior/olxjev/decision"
)

// choiceP is a choice answer carrying probabilities, as the live API returns;
// confidence follows the docs: (p − 1/n) / (1 − 1/n).
func choiceP(option string, p float64, n int) *decision.Answer {
    u := 1 / float64(n)
    return &decision.Answer{
        Kind: decision.KindChoice, Choice: option,
        Confidence:    (p - u) / (1 - u),
        Probabilities: map[string]float64{option: p, NoneOption: 1 - p},
    }
}

// One number in the query means two options (it or none): confidence 0.4 is
// Jev saying 70% — a confidence cutoff of 0.5 demanded 75%.
func TestAcceptByProbabilityNotConfidence(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{
        flatRentKharkiv(),
        {"filter_float_price:to": choiceP("10000", 0.7, 2)},
    }}
    r := newTestResolver(t, f)
    r.Thresholds = Thresholds{} // uncalibrated 0.5 for every type
    res, err := r.Resolve(context.Background(), "квартира в Харкові до 10000 грн")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if res.Values["filter_float_price:to"] != "10000" {
        t.Errorf("price:to = %q, want 10000 accepted at probability 0.7", res.Values["filter_float_price:to"])
    }
    if got := res.Fields["filter_float_price:to"].Probability; math.Abs(got-0.7) > 1e-9 {
        t.Errorf("Probability = %v, want 0.7", got)
    }
}

// Golden calibration: right numeric answers came at ≥0.90, wrong ones up to
// 0.70 («на 9 поверсі» claimed as the building height).
func TestNewResolverUsesCalibratedThresholds(t *testing.T) {
    r := newTestResolver(t, &fakeDecider{})
    if r.Thresholds != DefaultThresholds || DefaultThresholds.Number != 0.8 {
        t.Fatalf("Thresholds = %+v, want DefaultThresholds with Number 0.8", r.Thresholds)
    }
    for p, want := range map[float64]bool{0.7: false, 0.85: true} {
        f := &fakeDecider{answers: []map[string]*decision.Answer{flatRentKharkiv(), {"filter_float_price:to": choiceP("10000", p, 2)}}}
        res, err := newTestResolver(t, f).Resolve(context.Background(), "квартира в Харкові до 10000 грн")
        if err != nil {
            t.Fatalf("Resolve: %v", err)
        }
        if _, got := res.Values["filter_float_price:to"]; got != want {
            t.Errorf("price at %.2f accepted = %v, want %v", p, got, want)
        }
    }
}

func TestAnswerProbabilityFallsBackToConfidence(t *testing.T) {
    cases := []struct {
        conf float64
        n    int
        want float64
    }{
        {0.4, 2, 0.7},
        {0.42, 42, 0.42*(1-1.0/42) + 1.0/42},
        {0, 4, 0.25},
    }
    for _, c := range cases {
        a := &decision.Answer{Kind: decision.KindChoice, Choice: "x", Confidence: c.conf}
        if got := a.Probability(c.n); math.Abs(got-c.want) > 1e-9 {
            t.Errorf("Probability(conf %v, n %d) = %v, want %v", c.conf, c.n, got, c.want)
        }
    }
}

// The hotel case: the right category at 0.43 is rejected by the default
// cutoff and accepted when the category cutoff is calibrated lower.
func TestCategoryThreshold(t *testing.T) {
    answers := func() []map[string]*decision.Answer {
        return []map[string]*decision.Answer{{QuestionCategory: choiceP("category_3713", 0.43, 42)}}
    }
    const q = "готель на морі в Одеській області"
    if _, err := newTestResolver(t, &fakeDecider{answers: answers()}).Resolve(context.Background(), q); !errors.Is(err, ErrCategoryUnresolved) {
        t.Errorf("default cutoff: err = %v, want ErrCategoryUnresolved", err)
    }
    r := newTestResolver(t, &fakeDecider{answers: answers()})
    r.Thresholds.Category = 0.4
    res, err := r.Resolve(context.Background(), q)
    if err != nil || r.Catalog.LeafOf(res.Values) != "category_3713" {
        t.Errorf("Category 0.4: err %v, values %v, want category_3713", err, res)
    }
}

// Each question type reads its own cutoff; unset types fall back to
// Threshold, then DefaultThreshold.
func TestPerTypeThresholds(t *testing.T) {
    stage2 := func() map[string]*decision.Answer {
        return map[string]*decision.Answer{
            "currency":              choiceP("UAH", 0.7, 4),
            "filter_float_price:to": choiceP("10000", 0.7, 2),
            gateKey("filter_enum_pets"): noul(0.9),
        }
    }
    const q = "квартира в Харкові з котом до 10000 грн"
    resolve := func(set func(r *Resolver)) map[string]string {
        r := newTestResolver(t, &fakeDecider{answers: []map[string]*decision.Answer{flatRentKharkiv(), stage2(), {"filter_enum_pets#yes_cat": noul(0.6)}}})
        r.Thresholds = Thresholds{} // uncalibrated: every type falls back to Threshold
        set(r)
        res, err := r.Resolve(context.Background(), q)
        if err != nil {
            t.Fatalf("Resolve: %v", err)
        }
        return res.Values
    }
    cases := []struct {
        name string
        set  func(r *Resolver)
        want map[string]bool // key -> set
    }{
        {"defaults", func(r *Resolver) {}, map[string]bool{"currency": true, "filter_float_price:to": true, "filter_enum_pets": true}},
        {"Number 0.8", func(r *Resolver) { r.Thresholds.Number = 0.8 }, map[string]bool{"currency": true, "filter_float_price:to": false, "filter_enum_pets": true}},
        {"Choice 0.8", func(r *Resolver) { r.Thresholds.Choice = 0.8 }, map[string]bool{"currency": false, "filter_float_price:to": true, "filter_enum_pets": true}},
        {"Multi 0.7", func(r *Resolver) { r.Thresholds.Multi = 0.7 }, map[string]bool{"currency": true, "filter_float_price:to": true, "filter_enum_pets": false}},
        {"Threshold 0.8, Choice 0.6", func(r *Resolver) { r.Threshold = 0.8; r.Thresholds.Choice = 0.6 }, map[string]bool{"currency": true, "filter_float_price:to": false, "filter_enum_pets": false}},
    }
    for _, c := range cases {
        values := resolve(c.set)
        for key, want := range c.want {
            if _, got := values[key]; got != want {
                t.Errorf("%s: %s set = %v, want %v (values %v)", c.name, key, got, want, values)
            }
        }
    }
}

func TestPresenceThreshold(t *testing.T) {
    const q = "купити будинок в Іванівці"
    answers := func() []map[string]*decision.Answer {
        return []map[string]*decision.Answer{{QuestionCategory: choiceP("category_1602", 0.95, 42), QuestionPlace: noul(0.6)}}
    }
    if _, err := newTestResolver(t, &fakeDecider{answers: answers()}).Resolve(context.Background(), q); !errors.Is(err, ErrLocationUnresolved) {
        t.Errorf("default cutoff: err = %v, want ErrLocationUnresolved", err)
    }
    r := newTestResolver(t, &fakeDecider{answers: answers()})
    r.Thresholds.Presence = 0.7
    if _, err := r.Resolve(context.Background(), q); err != nil {
        t.Errorf("Presence 0.7: err = %v, want none (place not named enough)", err)
    }
}

func TestPlaceThreshold(t *testing.T) {
    r := newTestResolver(t, &fakeDecider{answers: []map[string]*decision.Answer{{
        QuestionCategory: choiceP("category_1760", 0.95, 42),
        QuestionCity:     choiceP("280", 0.6, 9),
    }}})
    r.Thresholds.Place = 0.7
    res, err := r.Resolve(context.Background(), "зняти квартиру в Харкові")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if _, ok := res.Values["city"]; ok {
        t.Errorf("city accepted at 0.6 with Place 0.7: %v", res.Values)
    }
}
