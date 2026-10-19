package olxjev

import (
    "context"
    "errors"
    "reflect"
    "testing"

    "github.com/mixadior/olxjev/decision"
)

// «купити оселю» is a flat or a house: below the cutoff, the result offers
// the leading categories so the user can pick (TypeSafe docs: at low
// confidence, ask for clarification instead of guessing).
func TestResolveUnresolvedCategoryOffersCandidates(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{{QuestionCategory: {
        Kind: decision.KindChoice, Choice: "category_1758", Confidence: 0.4,
        Probabilities: map[string]float64{"category_1758": 0.44, "category_1602": 0.38, "category_3711": 0.05, NoneOption: 0.13},
    }}}}
    r := newTestResolver(t, f)
    res, err := r.Resolve(context.Background(), "купити оселю в Києві")
    if !errors.Is(err, ErrCategoryUnresolved) {
        t.Fatalf("err = %v, want ErrCategoryUnresolved", err)
    }
    flat, _ := r.Catalog.Leaf("category_1758")
    house, _ := r.Catalog.Leaf("category_1602")
    want := []FieldInfo{
        {Value: "category_1758", Label: flat.Label, Probability: 0.44, Source: SourceModel},
        {Value: "category_1602", Label: house.Label, Probability: 0.38, Source: SourceModel},
    }
    if !reflect.DeepEqual(res.CategoryCandidates, want) {
        t.Errorf("CategoryCandidates = %+v, want %+v", res.CategoryCandidates, want)
    }
}

func TestResolveUnresolvedCategoryWithoutProbabilities(t *testing.T) {
    f := &fakeDecider{} // "none", no probabilities
    res, err := newTestResolver(t, f).Resolve(context.Background(), "щось незрозуміле")
    if !errors.Is(err, ErrCategoryUnresolved) || len(res.CategoryCandidates) != 0 {
        t.Errorf("err %v, candidates %v; want ErrCategoryUnresolved and none", err, res.CategoryCandidates)
    }
}
