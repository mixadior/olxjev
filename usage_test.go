package olxjev

import (
    "context"
    "errors"
    "math"
    "testing"

    "github.com/mixadior/olxjev/decision"
)

// Result.Usage sums the decision calls of one Resolve, as the Decider
// priced them, and names the model.
func TestResolveUsageSumsCalls(t *testing.T) {
    f := &fakeDecider{
        answers: []map[string]*decision.Answer{flatRentKharkiv(), {}},
        tokens:  [][2]int{{3000, 7}, {2000, 13}},
    }
    res, err := newTestResolver(t, f).Resolve(context.Background(), "зняти квартиру в Харкові")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    u := res.Usage
    if u.Calls != 2 || u.InputTokens != 5000 || u.OutputTokens != 20 || u.Model != "fake-1" {
        t.Errorf("Usage = %+v, want 2 calls, 5000 input, 20 output tokens, model fake-1", u)
    }
    if want := 5000 * fakePrice / 1e6; math.Abs(u.CostUSD-want) > 1e-12 {
        t.Errorf("CostUSD = %v, want %v", u.CostUSD, want)
    }
}

func TestResolveUsageStage1Only(t *testing.T) {
    f := &fakeDecider{tokens: [][2]int{{4200, 3}}} // category "none": no stage 2
    res, err := newTestResolver(t, f).Resolve(context.Background(), "щось незрозуміле")
    if !errors.Is(err, ErrCategoryUnresolved) {
        t.Fatalf("err = %v, want ErrCategoryUnresolved", err)
    }
    if res.Usage.Calls != 1 || res.Usage.InputTokens != 4200 {
        t.Errorf("Usage = %+v, want the stage-1 call only", res.Usage)
    }
}
