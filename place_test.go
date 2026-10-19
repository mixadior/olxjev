package olxjev

import (
    "context"
    "errors"
    "sort"
    "testing"

    "github.com/mixadior/olxjev/decision"
)

// «в Іванівці»: OLX has dozens of Іванівка; without the region Jev cannot
// pick one, and searching all of Ukraine would ignore the place the user
// named. The user is asked to add the region instead.
func TestResolvePlaceNamedButUnresolvedAsksForRegion(t *testing.T) {
    const q = "купити будинок в Іванівці"
    r := newTestResolver(t, &fakeDecider{})
    shortlist := r.Gazetteer.Shortlist(q, DefaultShortlist)
    if len(shortlist) < 2 {
        t.Fatalf("shortlist = %v, want several Іванівка", shortlist)
    }
    probabilities := map[string]float64{}
    for i, c := range shortlist {
        probabilities[c.CityID] = 0.05 * float64(i+1) // ascending, so sorting is visible
    }
    f := &fakeDecider{answers: []map[string]*decision.Answer{{
        QuestionCategory: choice("category_1602", 0.95),
        QuestionCity:     {Kind: decision.KindChoice, Choice: NoneOption, Confidence: 0.6, Probabilities: probabilities},
        QuestionPlace:    noul(0.9),
    }}}
    r.Decider = f

    res, err := r.Resolve(context.Background(), q)
    if !errors.Is(err, ErrLocationUnresolved) {
        t.Fatalf("err = %v, want ErrLocationUnresolved", err)
    }
    if len(f.calls) != 1 {
        t.Errorf("want 1 Jev call (no stage 2 without a place), got %d", len(f.calls))
    }
    if res == nil || res.Values["category_1"] == "" || res.Values["region"] != "" {
        t.Fatalf("want the category and no region, got %+v", res)
    }
    if len(res.CityCandidates) != len(shortlist) {
        t.Fatalf("CityCandidates = %d, want the %d shortlisted", len(res.CityCandidates), len(shortlist))
    }
    if !sort.SliceIsSorted(res.CityCandidates, func(i, j int) bool {
        return res.CityCandidates[i].Probability > res.CityCandidates[j].Probability
    }) {
        t.Errorf("CityCandidates must be most probable first: %+v", res.CityCandidates)
    }
    if c := res.CityCandidates[0]; c.Label == "" || c.Value == "" {
        t.Errorf("candidate = %+v, want id and «name, region» label", c)
    }
}

// Ordinary words fuzzy-match villages («тур» → Тур): a shortlist alone does
// not mean the user named a place.
func TestResolveNoPlaceNamedSearchesEverywhere(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{{
        QuestionCategory: choice("category_3715", 0.95),
        QuestionPlace:    noul(0.1),
    }}}
    res, err := newTestResolver(t, f).Resolve(context.Background(), "тур від туроператора на море")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if len(res.CityCandidates) != 0 {
        t.Errorf("CityCandidates = %v, want none", res.CityCandidates)
    }
}

func TestStage1AsksPlaceOnlyWithShortlist(t *testing.T) {
    catalog, err := NewCatalog(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewCatalog: %v", err)
    }
    cities := []CityCandidate{{RegionID: "8", CityID: "280", Label: "Харків, Харківська область"}}
    if q, ok := stage1Questions(catalog, cities)[QuestionPlace]; !ok || q.Kind != decision.KindNoul {
        t.Errorf("place question = %+v, want a noul", q)
    }
    if _, ok := stage1Questions(catalog, nil)[QuestionPlace]; ok {
        t.Error("place question must be omitted for an empty shortlist")
    }
}
