package olxjev

import (
    "context"
    "slices"
    "errors"
    "reflect"
    "strings"
    "testing"

    "github.com/mixadior/olxjev/decision"
)

const exampleQuery = "хочу зняти однушку квартиру в Харкові до 10к гривень в центрі"

func newTestResolver(t *testing.T, f *fakeDecider) *Resolver {
    t.Helper()
    r, err := NewResolver(testComposer(t), f)
    if err != nil {
        t.Fatalf("NewResolver: %v", err)
    }
    return r
}

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

func TestQuestionGroup(t *testing.T) {
    for key, want := range map[string]string{
        "filter_enum_pets#yes_cat":          "filter_enum_pets",
        "filter_enum_pets#" + groupOption:   "filter_enum_pets",
        gateKey("filter_enum_pets"):         "filter_enum_pets",
        "filter_float_price:from":           "filter_float_price",
        "filter_float_price:to":             "filter_float_price",
        "filter_bool_commission":            "filter_bool_commission",
        QuestionCategory:                    QuestionCategory,
    } {
        if got := questionGroup(key); got != want {
            t.Errorf("questionGroup(%q) = %q, want %q", key, got, want)
        }
    }
}

// groupable records the grouping NewResolver hands a batching backend.
type groupable struct {
    *fakeDecider
    group func(string) string
}

func (g *groupable) WithGroups(group func(string) string) decision.Decider {
    g.group = group
    return g
}

// A batching backend gets questionGroup, so a field's options and a number
// family's bounds share a request; the thresholds still follow its Name.
func TestNewResolverGroupsBatchingBackend(t *testing.T) {
    g := &groupable{fakeDecider: &fakeDecider{name: "typesafe"}}
    r, err := NewResolver(testComposer(t), g)
    if err != nil {
        t.Fatal(err)
    }
    if g.group == nil || g.group("filter_enum_pets#no") != "filter_enum_pets" {
        t.Fatal("NewResolver must pass questionGroup to a Groupable backend")
    }
    if r.Decider != decision.Decider(g) || r.Thresholds != CalibratedThresholds["typesafe"] {
        t.Errorf("Decider %T, thresholds %+v", r.Decider, r.Thresholds)
    }
}

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

func flatRentKharkiv() map[string]*decision.Answer {
    return map[string]*decision.Answer{
        QuestionCategory: choice("category_1760", 0.95),
        QuestionCity:     choice("280", 0.9),
    }
}

func TestResolveExampleQuery(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{
        flatRentKharkiv(),
        {
            "district":              choice("67", 0.7),
            "currency":              choice("UAH", 0.8),
            "filter_float_price:to": choice("10000", 0.88),
            gateKey("filter_enum_number_of_rooms_string"): noul(0.95),
        },
        {"filter_enum_number_of_rooms_string#odnokomnatnye": noul(0.93)},
    }}
    res, err := newTestResolver(t, f).Resolve(context.Background(), exampleQuery)
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    want := map[string]string{
        "category_1": "category_1757", "category_1757": "category_1760",
        "region": "8", "city": "280", "district": "67", "currency": "UAH",
        "filter_float_price:to":              "10000",
        "filter_enum_number_of_rooms_string": "odnokomnatnye",
    }
    if !reflect.DeepEqual(res.Values, want) {
        t.Errorf("Values =\n%v\nwant\n%v", res.Values, want)
    }
    if len(f.calls) != 3 {
        t.Fatalf("want 3 Jev calls (the rooms gate passed), got %d", len(f.calls))
    }
    if f.calls[0].state != exampleQuery {
        t.Errorf("stage-1 state = %v, want the raw query", f.calls[0].state)
    }
    state2 := f.calls[1].state.(map[string]any)
    wantResolved := map[string]string{
        "Категорія": "Нерухомість › Квартири › Довгострокова оренда квартир",
        "Місто":     "Харків, Харківська область",
    }
    if !reflect.DeepEqual(state2["resolved"], wantResolved) || state2["query"] != exampleQuery {
        t.Errorf("stage-2 state = %v", state2)
    }
    if _, asked := f.calls[1].questions["filter_float_land_area:from"]; asked {
        t.Error("land area does not apply to flats and must not be asked")
    }
    if res.Fields["region"].Source != SourceDerived {
        t.Errorf("region source = %q, want derived", res.Fields["region"].Source)
    }
    if got := res.Fields["filter_float_price:to"].Label; got != "Харкові до 10к гривень в" {
        t.Errorf("price label = %q", got)
    }
}

func TestResolveCategoryUnresolved(t *testing.T) {
    f := &fakeDecider{} // everything "none"
    res, err := newTestResolver(t, f).Resolve(context.Background(), exampleQuery)
    if !errors.Is(err, ErrCategoryUnresolved) {
        t.Fatalf("err = %v, want ErrCategoryUnresolved", err)
    }
    if len(f.calls) != 1 {
        t.Errorf("want 1 Jev call, got %d", len(f.calls))
    }
    if res == nil || len(res.Values) != 0 {
        t.Errorf("want an empty partial result, got %+v", res)
    }
}

func TestResolveInvalidChoiceIsNone(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{{QuestionCategory: choice("category_9999", 0.99)}}}
    _, err := newTestResolver(t, f).Resolve(context.Background(), exampleQuery)
    if !errors.Is(err, ErrCategoryUnresolved) {
        t.Fatalf("err = %v, want ErrCategoryUnresolved", err)
    }
}

func TestResolveLowConfidenceCityFallsBackToRegion(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{{
        QuestionCategory: choice("category_1760", 0.95),
        QuestionCity:     choice("280", 0.3),
        QuestionRegion:   choice("8", 0.8),
    }}}
    res, err := newTestResolver(t, f).Resolve(context.Background(), exampleQuery)
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if res.Values["region"] != "8" || res.Values["city"] != "" {
        t.Errorf("Values = %v, want region 8 and no city", res.Values)
    }
    if state := f.calls[1].state.(map[string]any)["resolved"].(map[string]string); state["Область"] != "Харківська область" {
        t.Errorf("stage-2 resolved = %v", state)
    }
    if _, asked := f.calls[1].questions["district"]; asked {
        t.Error("district must not be asked without a city")
    }
}

func TestResolveNoLocation(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{{QuestionCategory: choice("category_1760", 0.95)}}}
    res, err := newTestResolver(t, f).Resolve(context.Background(), "зняти квартиру до 8000")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if _, ok := res.Values["region"]; ok {
        t.Errorf("Values = %v, want no region", res.Values)
    }
    if res.Skipped[0] != "region" || res.Skipped[1] != "city" {
        t.Errorf("Skipped = %v", res.Skipped)
    }
}

func TestResolveMultiChoiceSelectsSeveral(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{
        flatRentKharkiv(),
        {gateKey("filter_enum_number_of_rooms_string"): noul(0.95)},
        {
            "filter_enum_number_of_rooms_string#odnokomnatnye": noul(0.9),
            "filter_enum_number_of_rooms_string#dvuhkomnatnye": noul(0.85),
            "filter_enum_number_of_rooms_string#trehkomnatnye": noul(0.2),
        },
    }}
    res, err := newTestResolver(t, f).Resolve(context.Background(), "1-2 кімнатна квартира в Харкові в оренду")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if got := res.Values["filter_enum_number_of_rooms_string"]; got != "dvuhkomnatnye;odnokomnatnye" {
        t.Errorf("rooms = %q, want dvuhkomnatnye;odnokomnatnye", got)
    }
    if got := res.Fields["filter_enum_number_of_rooms_string"].Probability; got != 0.85 {
        t.Errorf("confidence = %v, want the lowest accepted 0.85", got)
    }
    if _, ok := res.Values["filter_enum_pets"]; ok {
        t.Error("pets: no option reached the threshold, must be unset")
    }
}

func TestResolveRangeFromAboveTo(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{
        flatRentKharkiv(),
        {
            "filter_float_price:from": choice("12000", 0.6),
            "filter_float_price:to":   choice("8000", 0.9),
        },
    }}
    res, err := newTestResolver(t, f).Resolve(context.Background(), "квартира в Харкові від 8 до 12 тис")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if res.Values["filter_float_price:to"] != "8000" {
        t.Errorf("price:to = %q, want 8000 (more confident)", res.Values["filter_float_price:to"])
    }
    if _, ok := res.Values["filter_float_price:from"]; ok {
        t.Error("price:from 12000 > to 8000 and less confident: must be dropped")
    }
}

func TestResolveNumberUsedByOneFamily(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{
        flatRentKharkiv(),
        {
            "filter_float_price:to":      choice("60", 0.55),
            "filter_float_total_area:to": choice("60", 0.9),
            "filter_float_total_area:from": choice("60", 0.7),
        },
    }}
    res, err := newTestResolver(t, f).Resolve(context.Background(), "квартира в Харкові до 60 м2")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if res.Values["filter_float_total_area:to"] != "60" || res.Values["filter_float_total_area:from"] != "60" {
        t.Errorf("area = %v, want from and to 60 (same family may share a number)", res.Values)
    }
    if _, ok := res.Values["filter_float_price:to"]; ok {
        t.Error("60 belongs to the area family; price must be dropped")
    }
}

// Live eval: Jev gave 0.98 to both floor:to=5 and total_floors:to=5; picking
// the first in wizard order is a coin toss, so an exact tie drops both.
func TestResolveNumberTieAcrossFamiliesDropsBoth(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{
        flatRentKharkiv(),
        {
            "filter_float_floor:to":        choice("5", 0.98),
            "filter_float_total_floors:to": choice("5", 0.98),
        },
    }}
    res, err := newTestResolver(t, f).Resolve(context.Background(), "квартира в Харкові не вище 5 поверху")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    for _, key := range []string{"filter_float_floor:to", "filter_float_total_floors:to"} {
        if v, ok := res.Values[key]; ok {
            t.Errorf("%s = %q, want unset on an exact tie", key, v)
        }
    }
}

// A bool filter is one noul; «0» ("all ads") is never a value: olxurl.Build
// turns any non-empty bool value into an active filter.
func TestResolveBoolFilterIsNoul(t *testing.T) {
    resolve := func(p float64, set func(r *Resolver)) (*Result, *fakeDecider) {
        f := &fakeDecider{answers: []map[string]*decision.Answer{flatRentKharkiv(), {"filter_bool_commission": noul(p)}}}
        r := newTestResolver(t, f)
        set(r)
        res, err := r.Resolve(context.Background(), "квартира в Харкові без комісії")
        if err != nil {
            t.Fatalf("Resolve: %v", err)
        }
        return res, f
    }

    res, f := resolve(0.9, func(*Resolver) {})
    if q := f.calls[1].questions["filter_bool_commission"]; q.Kind != decision.KindNoul {
        t.Fatalf("commission question = %+v, want a noul", q)
    }
    if res.Values["filter_bool_commission"] != "1" {
        t.Errorf("commission = %q, want 1", res.Values["filter_bool_commission"])
    }
    if fi := res.Fields["filter_bool_commission"]; fi.Label != "Без комісії" || fi.Probability != 0.9 {
        t.Errorf("field = %+v, want label «Без комісії», probability 0.9", fi)
    }

    res, _ = resolve(0.3, func(*Resolver) {})
    if v, ok := res.Values["filter_bool_commission"]; ok || !slices.Contains(res.Skipped, "filter_bool_commission") {
        t.Errorf("at 0.3: commission = %q, skipped %v; want unset and skipped", v, res.Skipped)
    }

    res, _ = resolve(0.9, func(r *Resolver) { r.Thresholds.Multi = 0.95 })
    if _, ok := res.Values["filter_bool_commission"]; ok {
        t.Error("bool filters use the Multi cutoff: 0.9 < 0.95 must be rejected")
    }
}

func TestResolveNoNumbersNoNumberQuestions(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{flatRentKharkiv()}}
    if _, err := newTestResolver(t, f).Resolve(context.Background(), "зняти квартиру в Харкові"); err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    for key := range f.calls[1].questions {
        if strings.HasPrefix(key, "filter_float_") {
            t.Errorf("number question %s asked without numbers in the query", key)
        }
    }
}

func TestResolveStage2ErrorReturnsPartial(t *testing.T) {
    boom := errors.New("HTTP 500")
    f := &fakeDecider{answers: []map[string]*decision.Answer{flatRentKharkiv()}, errs: []error{nil, boom}}
    res, err := newTestResolver(t, f).Resolve(context.Background(), exampleQuery)
    if !errors.Is(err, boom) {
        t.Fatalf("err = %v, want wrapped %v", err, boom)
    }
    if res == nil || res.Values["city"] != "280" || res.Values["category_1757"] != "category_1760" {
        t.Errorf("want partial result with category and city, got %+v", res)
    }
}

func TestResolveStage1ErrorReturnsNil(t *testing.T) {
    f := &fakeDecider{errs: []error{errors.New("HTTP 401")}}
    res, err := newTestResolver(t, f).Resolve(context.Background(), exampleQuery)
    if err == nil || res != nil {
        t.Errorf("Resolve = %+v, %v; want nil result and an error", res, err)
    }
}

func TestResolveEmptyQuery(t *testing.T) {
    f := &fakeDecider{}
    if _, err := newTestResolver(t, f).Resolve(context.Background(), "   "); !errors.Is(err, ErrEmptyQuery) {
        t.Errorf("err = %v, want ErrEmptyQuery", err)
    }
    if len(f.calls) != 0 {
        t.Error("an empty query must not call Jev")
    }
}

func TestResolveTrace(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{flatRentKharkiv()}}
    r := newTestResolver(t, f)
    stages := []string{}
    r.Trace = func(stage string, _ any, _ map[string]decision.Question, resp *decision.Decision) {
        if resp == nil {
            t.Errorf("%s: nil response on success", stage)
        }
        stages = append(stages, stage)
    }
    if _, err := r.Resolve(context.Background(), exampleQuery); err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if !reflect.DeepEqual(stages, []string{"stage1", "stage2"}) {
        t.Errorf("traced stages = %v", stages)
    }
}

func TestResolveZeroConfidenceIsRejected(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{{QuestionCategory: {Kind: decision.KindChoice, Choice: "category_1760"}}}}
    _, err := newTestResolver(t, f).Resolve(context.Background(), exampleQuery)
    if !errors.Is(err, ErrCategoryUnresolved) {
        t.Errorf("err = %v, want ErrCategoryUnresolved for a zero-confidence answer", err)
    }
}

func TestApplyStage2MissingAnswers(t *testing.T) {
    r := newTestResolver(t, &fakeDecider{})
    values := map[string]string{"region": "8", "city": "280", "category_1": "category_1757", "category_1757": "category_1760"}
    elements, err := applicableElements(r.Composer, values)
    if err != nil {
        t.Fatalf("applicableElements: %v", err)
    }
    numbers := ExtractNumbers("до 10к")
    res := &Result{Values: map[string]string{}, Fields: map[string]FieldInfo{}}
    r.applyStage2(res, elements, numbers, stage2Questions(elements, numbers), &decision.Decision{Answers: map[string]*decision.Answer{}})
    if len(res.Values) != 0 || len(res.Skipped) != len(elements) {
        t.Errorf("want nothing resolved and every element skipped, got %v / %v", res.Values, res.Skipped)
    }
}
