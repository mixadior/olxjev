package olxjev

import (
    "context"
    "errors"
    "strings"
    "testing"

    "github.com/mixadior/olxjev/decision"
)

// Stage 2 asks one gate per multi-choice element instead of a noul per
// option; nothing about pets or rooms in the query, so no stage 3.
func TestResolveGateRejectedSkipsOptions(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{flatRentKharkiv()}}
    res, err := newTestResolver(t, f).Resolve(context.Background(), "зняти квартиру в Харкові")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if len(f.calls) != 2 {
        t.Fatalf("got %d Jev calls, want 2 (every gate said no)", len(f.calls))
    }
    stage2 := f.calls[1].questions
    if q, ok := stage2[gateKey("filter_enum_pets")]; !ok || q.Kind != decision.KindNoul {
        t.Errorf("stage 2 lacks the pets gate noul: %+v", q)
    }
    for k := range stage2 {
        if strings.Contains(k, "#") {
            t.Errorf("stage 2 asks option %s: options wait for their gate", k)
        }
    }
    if _, asked := stage2["currency"]; !asked {
        t.Error("single-choice fields stay in stage 2")
    }
    if _, ok := res.Values["filter_enum_pets"]; ok {
        t.Errorf("pets = %q, want unset", res.Values["filter_enum_pets"])
    }
}

// A passed gate sends that element's option nouls, and only those, in a
// third call with the stage-2 state.
func TestResolveGatePassedAsksOptions(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{
        flatRentKharkiv(),
        {gateKey("filter_enum_pets"): noul(0.9), "currency": choice("UAH", 0.9)},
        {"filter_enum_pets#yes_cat": noul(0.95)},
    }}
    res, err := newTestResolver(t, f).Resolve(context.Background(), "зняти квартиру в Харкові з котом")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if len(f.calls) != 3 {
        t.Fatalf("got %d Jev calls, want 3", len(f.calls))
    }
    if got, want := mustJSON(t, f.calls[2].state), mustJSON(t, f.calls[1].state); got != want {
        t.Errorf("stage-3 state = %s, want the stage-2 state %s", got, want)
    }
    for k := range f.calls[2].questions {
        if !strings.HasPrefix(k, "filter_enum_pets#") {
            t.Errorf("stage 3 asks %s: only the passed element's options belong there", k)
        }
    }
    if _, ok := f.calls[2].questions["filter_enum_pets#"+groupOption]; !ok {
        t.Error("stage 3 lacks the any-pet group noul")
    }
    if res.Values["filter_enum_pets"] != "yes_cat" || res.Values["currency"] != "UAH" {
        t.Errorf("Values = %v, want pets yes_cat and currency from stage 2", res.Values)
    }
    if res.Usage.Calls != 3 {
        t.Errorf("Usage.Calls = %d, want 3", res.Usage.Calls)
    }
}

func TestResolveGateCutoff(t *testing.T) {
    for _, tc := range []struct {
        gate  float64
        calls int
    }{{0.29, 2}, {0.3, 3}} {
        f := &fakeDecider{answers: []map[string]*decision.Answer{flatRentKharkiv(), {gateKey("filter_enum_pets"): noul(tc.gate)}}}
        if _, err := newTestResolver(t, f).Resolve(context.Background(), "зняти квартиру в Харкові з котом"); err != nil {
            t.Fatalf("Resolve: %v", err)
        }
        if len(f.calls) != tc.calls {
            t.Errorf("gate %.2f: %d calls, want %d (DefaultThresholds.Gate %.2f)", tc.gate, len(f.calls), tc.calls, DefaultThresholds.Gate)
        }
    }
}

// A failed stage 3 is reported like a failed stage 2.
func TestResolveStage3Error(t *testing.T) {
    f := &fakeDecider{
        answers: []map[string]*decision.Answer{flatRentKharkiv(), {gateKey("filter_enum_pets"): noul(0.9)}},
        errs:    []error{nil, nil, errors.New("boom")},
    }
    res, err := newTestResolver(t, f).Resolve(context.Background(), "зняти квартиру в Харкові з котом")
    if err == nil || !strings.Contains(err.Error(), "stage3") {
        t.Fatalf("err = %v, want a stage3 error", err)
    }
    if res == nil || res.Fields[QuestionCategory].Value != "category_1760" {
        t.Errorf("res = %+v, want the partial result with the category", res)
    }
}

// The golden calibration grades each gate: right when the corpus expects the
// field set, wrong when it must stay unset.
func TestCollectSamplesGradesGate(t *testing.T) {
    key := gateKey("filter_enum_pets")
    questions := map[string]decision.Question{key: {Kind: decision.KindNoul}}
    got := collectSamples(map[string]string{"filter_enum_pets": "yes_cat"}, questions, map[string]*decision.Answer{key: noul(0.2)})
    want := []calSample{
        {Type: "Gate", P: 0.2, Correct: true, Pick: key},
        {Type: "Multi", Missed: true, Pick: "filter_enum_pets (gate said no)"},
    }
    if mustJSON(t, got) != mustJSON(t, want) {
        t.Errorf("expected set: samples = %+v, want %+v", got, want)
    }
    got = collectSamples(map[string]string{"filter_enum_pets": ""}, questions, map[string]*decision.Answer{key: noul(0.1)})
    if len(got) != 1 || got[0] != (calSample{Type: "Gate", P: 0.1, Correct: false, Pick: key}) {
        t.Errorf("must stay unset: samples = %+v, want one wrong Gate sample", got)
    }
}

// The gate lists the element's options: with the title alone, Jev passed
// nearly every gate and wanted fields scored 0.35–0.44 (golden 2026-10-17).
func TestGateQuestionListsOptions(t *testing.T) {
    el := pendingElement{Key: "filter_enum_heating", Title: "Опалення", Kind: kindMulti,
        Options: map[string]string{"central": "Централізоване", "individual_gas": "Індивідуальне газове"}}
    q := gateQuestion(el)
    s := q.Instructions
    if q.Kind != decision.KindNoul || !strings.HasPrefix(s, "Опалення") || !strings.Contains(s, "Централізоване; Індивідуальне газове") {
        t.Errorf("gate = %+v, want a noul naming the title and every option", q)
    }
}
