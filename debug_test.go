package olxjev

import (
    "context"
    "encoding/json"
    "testing"

    "github.com/mixadior/olxjev/decision"
)

// Stage2Plan describes exactly what Resolve sends Jev in stage 2.
func TestStage2PlanMatchesResolve(t *testing.T) {
    fake := &fakeDecider{answers: []map[string]*decision.Answer{flatRentKharkiv()}}
    r := newTestResolver(t, fake)
    query := "  зняти квартиру в Харкові до 10к з котом "
    res, err := r.Resolve(context.Background(), query)
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if len(fake.calls) != 2 {
        t.Fatalf("got %d Jev calls, want 2", len(fake.calls))
    }
    sent := fake.calls[1]

    state, filters, err := r.Stage2Plan(query, "category_1760", res)
    if err != nil {
        t.Fatalf("Stage2Plan: %v", err)
    }
    if got, want := mustJSON(t, state), mustJSON(t, sent.state); got != want {
        t.Errorf("plan state = %s\nResolve sent %s", got, want)
    }
    union := map[string]decision.Question{}
    seen := map[string]bool{}
    for _, f := range filters {
        if seen[f.Key] {
            t.Errorf("filter %s listed twice", f.Key)
        }
        seen[f.Key] = true
        if f.Title == "" || f.Kind == "" {
            t.Errorf("filter %s lacks a title or kind: %+v", f.Key, f)
        }
        for k, q := range f.Questions {
            union[k] = q
        }
    }
    if got, want := mustJSON(t, union), mustJSON(t, sent.questions); got != want {
        t.Errorf("plan questions differ from the ones Resolve sent:\nplan %s\nsent %s", got, want)
    }
    pets := filterByKey(filters, "filter_enum_pets")
    if pets == nil || pets.Kind != "multi" || len(pets.Questions) != 1 || len(pets.Options) < 2 {
        t.Errorf("pets filter = %+v, want a multi with its gate in stage 2 and a noul per option", pets)
    }
}

// Stage 3 sends a passed element's Options exactly.
func TestStage2PlanOptionsMatchStage3(t *testing.T) {
    fake := &fakeDecider{answers: []map[string]*decision.Answer{flatRentKharkiv(), {gateKey("filter_enum_pets"): noul(0.9)}}}
    r := newTestResolver(t, fake)
    query := "зняти квартиру в Харкові з котом"
    res, err := r.Resolve(context.Background(), query)
    if err != nil || len(fake.calls) != 3 {
        t.Fatalf("Resolve: %v, %d calls, want 3", err, len(fake.calls))
    }
    _, filters, err := r.Stage2Plan(query, "category_1760", res)
    if err != nil {
        t.Fatalf("Stage2Plan: %v", err)
    }
    if got, want := mustJSON(t, filterByKey(filters, "filter_enum_pets").Options), mustJSON(t, fake.calls[2].questions); got != want {
        t.Errorf("plan options = %s\nstage 3 sent %s", got, want)
    }
}

// A number field is listed even when the query has no numbers; it is just
// not asked.
func TestStage2PlanListsUnaskedNumbers(t *testing.T) {
    r := newTestResolver(t, &fakeDecider{})
    _, filters, err := r.Stage2Plan("купити квартиру", "category_1758", nil)
    if err != nil {
        t.Fatalf("Stage2Plan: %v", err)
    }
    price := filterByKey(filters, "filter_float_price:to")
    if price == nil || price.Kind != "number" || len(price.Questions) != 0 {
        t.Errorf("price filter = %+v, want a number field with no questions", price)
    }
}

func TestStage2PlanUnknownCategory(t *testing.T) {
    if _, _, err := newTestResolver(t, &fakeDecider{}).Stage2Plan("x", "category_0", nil); err == nil {
        t.Error("want an error for an unknown category")
    }
}

// WithTrace reports this Resolve's exchanges, answers included.
func TestWithTraceReportsExchanges(t *testing.T) {
    r := newTestResolver(t, &fakeDecider{answers: []map[string]*decision.Answer{{QuestionCategory: choice("category_1760", 0.95)}}})
    var stages []string
    ctx := WithTrace(context.Background(), func(stage string, _ any, questions map[string]decision.Question, resp *decision.Decision) {
        if len(questions) == 0 || resp == nil {
            t.Errorf("%s: questions %d, resp %v", stage, len(questions), resp)
        }
        stages = append(stages, stage)
    })
    if _, err := r.Resolve(ctx, "зняти квартиру"); err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if len(stages) != 2 || stages[0] != "stage1" || stages[1] != "stage2" {
        t.Errorf("traced %v, want [stage1 stage2]", stages)
    }
}

func TestCategoryCandidatesExported(t *testing.T) {
    r := newTestResolver(t, &fakeDecider{})
    got := r.CategoryCandidates(&decision.Answer{Probabilities: map[string]float64{"category_1760": 0.6, "category_3711": 0.3, "category_1758": 0.05}})
    if len(got) != 2 || got[0].Value != "category_1760" || got[1].Value != "category_3711" {
        t.Errorf("CategoryCandidates = %+v", got)
    }
}

func filterByKey(filters []FilterPlan, key string) *FilterPlan {
    for i := range filters {
        if filters[i].Key == key {
            return &filters[i]
        }
    }
    return nil
}

func mustJSON(t *testing.T, v any) string {
    t.Helper()
    b, err := json.Marshal(v)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    return string(b)
}
