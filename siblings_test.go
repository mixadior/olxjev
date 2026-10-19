package olxjev

import (
    "context"
    "reflect"
    "strings"
    "testing"

    "github.com/mixadior/olxjev/decision"
    botform "github.com/mixadior/bfc"
)

func TestCitySiblings(t *testing.T) {
    accepted := CityCandidate{RegionID: "13", CityID: "2569", Name: "Яремча"}
    cities := []CityCandidate{
        accepted,
        {RegionID: "13", CityID: "157", Name: "Яремче"}, // same place, spelled differently: in
        {RegionID: "9", CityID: "901", Name: "Яремче"},  // other region: out
        {RegionID: "13", CityID: "902", Name: "Ярем"},   // name too different: out
        {RegionID: "13", CityID: "903", Name: "Яремчі"}, // near name but Jev gave it nothing: out
    }
    probabilities := map[string]float64{"2569": 0.7, "157": 0.25, "901": 0.3, "902": 0.3, "903": 0.05}
    got := citySiblings(accepted, cities, probabilities)
    if len(got) != 1 || got[0].CityID != "157" {
        t.Errorf("citySiblings = %+v, want only 157", got)
    }
}

// Live eval: Jev sometimes gave village «Запоріжжя» (25423) ≥0.2 next to the
// city (194); a city with districts is unambiguous, same-name villages are
// other places.
func TestCitySiblingsNoneForCityWithDistricts(t *testing.T) {
    accepted := CityCandidate{RegionID: "17", CityID: "194", Name: "Запоріжжя", HasDistricts: true}
    cities := []CityCandidate{accepted, {RegionID: "17", CityID: "25423", Name: "Запоріжжя"}}
    if got := citySiblings(accepted, cities, map[string]float64{"194": 0.7, "25423": 0.3}); len(got) != 0 {
        t.Errorf("citySiblings = %+v, want none for a city with districts", got)
    }
}

// The 2023 OLX data listed «Яремче» (157) and «Яремча» (2569) in one region
// and Jev split 0.75/0.25 between them. City is single-choice: Jev's pick goes
// to Values, the other spelling is offered as an alternative for the user.
// Current OLX data has no such pair, so the gazetteer here is synthetic.
func TestResolveNearDuplicateCityIsAlternative(t *testing.T) {
    f := &fakeDecider{answers: []map[string]*decision.Answer{{
        QuestionCategory: choice("category_3710", 0.99),
        QuestionCity: {
            Kind: decision.KindChoice, Choice: "2569", Confidence: 0.75,
            Probabilities: map[string]float64{"2569": 0.75, "157": 0.25},
        },
    }}}
    r := newTestResolver(t, f)
    gazetteer, err := NewGazetteer(&botform.BotFormDataNode{Children: map[string]*botform.BotFormDataNode{
        "region": {Children: map[string]*botform.BotFormDataNode{
            "13": {Value: "Івано-Франківська область", Children: map[string]*botform.BotFormDataNode{
                "157":  {Value: "Яремче"},
                "2569": {Value: "Яремча"},
            }},
        }},
    }})
    if err != nil {
        t.Fatalf("NewGazetteer: %v", err)
    }
    r.Gazetteer = gazetteer
    res, err := r.Resolve(context.Background(), "зняти будинок подобово в Яремче")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if res.Values["city"] != "2569" || res.Values["region"] != "13" {
        t.Errorf("Values = %v, want single city 2569 in region 13", res.Values)
    }
    want := []FieldInfo{{Value: "157", Label: "Яремче, Івано-Франківська область", Probability: 0.25, Source: SourceModel}}
    if !reflect.DeepEqual(res.CityAlternatives, want) {
        t.Errorf("CityAlternatives = %+v, want %+v", res.CityAlternatives, want)
    }
}

// recordingBreadcrumbs answers olxurl.Build and records the requests it got.
type recordingBreadcrumbs struct {
    urls []string
}

func (r *recordingBreadcrumbs) Q(_ context.Context, requestURL string, _ ...string) (interface{}, string, []error) {
    r.urls = append(r.urls, requestURL)
    return nil, `{"data":{"breadcrumbs":[{"href":"/uk/nedvizhimost/posutochno-pochasovo/"}]}}`, nil
}

func TestSearchURLsPrimaryThenAlternatives(t *testing.T) {
    res := &Result{
        Values: map[string]string{"category_1": "category_9", "category_9": "category_331", "region": "13", "city": "2569"},
        Fields: map[string]FieldInfo{"city": {Value: "2569", Label: "Яремча, Івано-Франківська область"}},
        CityAlternatives: []FieldInfo{{Value: "157", Label: "Яремче, Івано-Франківська область"}},
    }
    rq := &recordingBreadcrumbs{}
    urls, _, err := SearchURLs(context.Background(), res, rq)
    if err != nil {
        t.Fatalf("SearchURLs: %v", err)
    }
    if len(urls) != 2 || urls[0].City != "2569" || urls[1].City != "157" || urls[1].Label != "Яремче, Івано-Франківська область" {
        t.Fatalf("urls = %+v, want 2569 first, then alternative 157", urls)
    }
    if len(rq.urls) != 2 || !strings.HasSuffix(rq.urls[0], "&city_id=2569") || !strings.HasSuffix(rq.urls[1], "&city_id=157") {
        t.Errorf("breadcrumbs asked for %v, want one request per city", rq.urls)
    }
    if res.Values["city"] != "2569" {
        t.Errorf("SearchURLs must not modify values, city = %q", res.Values["city"])
    }
}

func TestSearchURLsWithoutAlternatives(t *testing.T) {
    res := &Result{Values: map[string]string{"category_1": "category_9", "category_9": "category_331", "region": "13"}}
    urls, _, err := SearchURLs(context.Background(), res, &recordingBreadcrumbs{})
    if err != nil {
        t.Fatalf("SearchURLs: %v", err)
    }
    if len(urls) != 1 || urls[0].City != "" || urls[0].URL == "" {
        t.Errorf("urls = %+v, want one region-level URL", urls)
    }
}
