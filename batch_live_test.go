package olxjev

import (
    "context"
    "fmt"
    "math"
    "os"
    "slices"
    "strings"
    "testing"
    "time"

    "github.com/mattn/go-jev"

    "github.com/mixadior/olxjev/decision"
    "github.com/mixadior/olxjev/decision/subq"
)

// TestLiveBatchInvariance checks that SubQ's batching does not change what
// Resolve accepts. SubQ takes 16 questions per request and its answers
// shift with the questions sharing a request (2026-10-18: up to 0.17
// between sorted chunks of 16 and of 5), so stages are batched by field
// (questionGroup). Each stage-2 and stage-3 question set is answered three
// times: grouped in requests of 16, again (run-to-run noise), and grouped in
// requests of 5 (other neighbours). It logs every answer whose accept/reject
// outcome flips at the default cutoffs, with both probabilities, and fails
// when other neighbours flip more outcomes than repeating the request does.
// Run: OLXJEV_LIVE=1 SUBQ_API_KEY=... go test -run TestLiveBatchInvariance -v ./olxjev/
func TestLiveBatchInvariance(t *testing.T) {
    key := os.Getenv("SUBQ_API_KEY")
    if os.Getenv("OLXJEV_LIVE") != "1" || key == "" {
        t.Skip("set OLXJEV_LIVE=1 and SUBQ_API_KEY (spends API credits)")
    }
    r, err := NewResolver(testComposer(t), nil)
    if err != nil {
        t.Fatal(err)
    }
    opts := []jev.ClientOption{jev.WithTimeout(60 * time.Second), jev.WithMaxRetries(3)}
    by16 := subq.New(key, opts...).(decision.Groupable).WithGroups(questionGroup)
    by5 := decision.Batched(subq.New(key, opts...), 5).(decision.Groupable).WithGroups(questionGroup)
    cases := []struct{ q, leaf string }{
        {"хочу зняти однушку квартиру в Харкові до 10к гривень в центрі з котом", "category_1760"},
        {"купити трикімнатну квартиру в Дніпрі від 60 до 80 тис доларів з ремонтом і балконом", "category_1758"},
        {"зняти двушку на Оболоні до 20 тисяч без комісії, можна з дітьми", "category_1760"},
        {"купити будинок з газом і свердловиною до 50 тис доларів", "category_1602"},
    }
    noiseFlips, neighbourFlips := 0, 0
    worst, noise := 0.0, 0.0
    for _, c := range cases {
        state, plans, err := r.Stage2Plan(c.q, c.leaf, nil)
        if err != nil {
            t.Fatal(err)
        }
        stage2, stage3 := map[string]decision.Question{}, map[string]decision.Question{}
        for _, p := range plans {
            for k, q := range p.Questions {
                stage2[k] = q
            }
            for k, q := range p.Options {
                stage3[k] = q
            }
        }
        for _, stage := range []struct {
            name string
            qs   map[string]decision.Question
        }{{"stage2", stage2}, {"stage3", stage3}} {
            ctx := context.Background()
            a, err := by16.Decide(ctx, state, stage.qs)
            if err != nil {
                t.Fatal(err)
            }
            b, err := by16.Decide(ctx, state, stage.qs)
            if err != nil {
                t.Fatal(err)
            }
            n, err := by5.Decide(ctx, state, stage.qs)
            if err != nil {
                t.Fatal(err)
            }
            nf, nd := outcomeFlips(stage.qs, a, b, r.Thresholds)
            gf, gd := outcomeFlips(stage.qs, a, n, r.Thresholds)
            noiseFlips += len(nf)
            neighbourFlips += len(gf)
            noise, worst = math.Max(noise, nd), math.Max(worst, gd)
            t.Logf("%s %q: %d questions, requests 16→%d 5→%d; max Δ repeat %.3f, other neighbours %.3f; flips repeat %v; flips other neighbours %v",
                stage.name, c.q, len(stage.qs), a.Usage.Calls, n.Usage.Calls, nd, gd, nf, gf)
        }
    }
    t.Logf("overall: outcome flips repeat %d, other neighbours %d; max Δ repeat %.3f, other neighbours %.3f",
        noiseFlips, neighbourFlips, noise, worst)
    if neighbourFlips > noiseFlips {
        t.Errorf("batching flips %d outcomes, repeating the request %d: do not calibrate, report back", neighbourFlips, noiseFlips)
    }
}

// outcomeFlips lists the answers whose accept/reject outcome differs
// between a and b at the cutoffs Resolve would apply ("key 0.27→0.35 @0.30"),
// and returns the largest probability change over all answers.
func outcomeFlips(qs map[string]decision.Question, a, b *decision.Decision, th Thresholds) ([]string, float64) {
    flips, worst := []string{}, 0.0
    for _, k := range slices.Sorted(func(yield func(string) bool) {
        for k := range qs {
            if !yield(k) {
                return
            }
        }
    }) {
        x, y := a.Answers[k], b.Answers[k]
        if x == nil || y == nil {
            flips = append(flips, k+" missing")
            continue
        }
        q := qs[k]
        vx, px, cut := outcome(k, q, x, th)
        vy, py, _ := outcome(k, q, y, th)
        worst = math.Max(worst, math.Abs(px-py))
        if vx != vy {
            flips = append(flips, fmt.Sprintf("%s %s %.2f→%s %.2f @%.2f", k, orNone(vx), px, orNone(vy), py, cut))
        }
    }
    return flips, worst
}

// outcome is what Resolve would accept from one answer: the option (or
// "yes") when its probability reaches the question type's cutoff.
func outcome(key string, q decision.Question, a *decision.Answer, th Thresholds) (string, float64, float64) {
    if q.Kind == decision.KindNoul {
        cut := th.Multi
        if strings.HasPrefix(key, gateKey("")) {
            cut = th.Gate
        }
        if a.Yes >= cut {
            return "yes", a.Yes, cut
        }
        return "", a.Yes, cut
    }
    cut := th.Choice
    if strings.HasPrefix(key, "filter_float_") {
        cut = th.Number
    }
    p := a.Probability(len(q.Options))
    if a.Choice != NoneOption && p >= cut {
        return a.Choice, p, cut
    }
    return "", p, cut
}

func orNone(v string) string {
    if v == "" {
        return "-"
    }
    return v
}
