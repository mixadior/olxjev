package decision

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "slices"
    "sort"
    "sync"
    "testing"
)

// recorder answers every question with noul 0.5 and records each call's keys.
type recorder struct {
    mu    sync.Mutex
    calls [][]string
    fail  map[string]error // a call containing this key fails
    wait  bool             // calls block until ctx is done (after recording)
}

func (r *recorder) Name() string  { return "rec" }
func (r *recorder) Model() string { return "rec-1" }

func (r *recorder) Decide(ctx context.Context, _ any, qs map[string]Question) (*Decision, error) {
    keys := make([]string, 0, len(qs))
    for k := range qs {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    r.mu.Lock()
    r.calls = append(r.calls, keys)
    r.mu.Unlock()
    for _, k := range keys {
        if err := r.fail[k]; err != nil {
            return nil, err
        }
    }
    if r.wait {
        <-ctx.Done()
        return nil, ctx.Err()
    }
    d := &Decision{Answers: map[string]*Answer{}, Usage: Usage{Calls: 1, InputTokens: 10, CostUSD: 0.5, Model: "rec-1"}, Raw: []json.RawMessage{json.RawMessage(`"` + keys[0] + `"`)}}
    for _, k := range keys {
        d.Answers[k] = &Answer{Kind: KindNoul, Yes: 0.5}
    }
    return d, nil
}

func questions(n int) map[string]Question {
    qs := map[string]Question{}
    for i := 0; i < n; i++ {
        qs[fmt.Sprintf("q%02d", i)] = Question{Kind: KindNoul, Instructions: "?"}
    }
    return qs
}

func TestBatchedSplitsSortedChunks(t *testing.T) {
    rec := &recorder{}
    d, err := Batched(rec, 16).Decide(context.Background(), "s", questions(21))
    if err != nil {
        t.Fatal(err)
    }
    sort.Slice(rec.calls, func(i, j int) bool { return rec.calls[i][0] < rec.calls[j][0] })
    if len(rec.calls) != 2 || len(rec.calls[0]) != 16 || len(rec.calls[1]) != 5 || rec.calls[0][15] != "q15" || rec.calls[1][0] != "q16" {
        t.Fatalf("calls = %v, want q00..q15 then q16..q20", rec.calls)
    }
    if len(d.Answers) != 21 || d.Usage.Calls != 2 || d.Usage.InputTokens != 20 || d.Usage.CostUSD != 1 || d.Usage.Model != "rec-1" {
        t.Errorf("merged = %d answers, usage %+v", len(d.Answers), d.Usage)
    }
    if len(d.Raw) != 2 || string(d.Raw[0]) != `"q00"` || string(d.Raw[1]) != `"q16"` {
        t.Errorf("Raw = %s, want chunk order", d.Raw)
    }
}

func TestBatchedBoundary(t *testing.T) {
    for _, c := range []struct{ n, calls int }{{16, 1}, {17, 2}, {32, 2}, {33, 3}, {1, 1}} {
        rec := &recorder{}
        if _, err := Batched(rec, 16).Decide(context.Background(), "s", questions(c.n)); err != nil {
            t.Fatal(err)
        }
        if len(rec.calls) != c.calls {
            t.Errorf("%d questions: %d calls, want %d", c.n, len(rec.calls), c.calls)
        }
    }
}

func TestBatchedPassThrough(t *testing.T) {
    rec := &recorder{}
    if b := Batched(rec, 0); b != Decider(rec) {
        t.Error("max 0 must return the Decider itself")
    }
    b := Batched(rec, 16)
    if b.Name() != "rec" || b.Model() != "rec-1" {
        t.Errorf("Name/Model = %s/%s", b.Name(), b.Model())
    }
}

func TestBatchedErrorCancelsAndReturnsNoAnswers(t *testing.T) {
    boom := errors.New("boom")
    rec := &recorder{fail: map[string]error{"q00": boom}, wait: true} // the other chunk waits for cancellation
    d, err := Batched(rec, 16).Decide(context.Background(), "s", questions(21))
    if !errors.Is(err, boom) || d != nil {
        t.Errorf("Decide = %v, %v; want nil, boom", d, err)
    }
}

// With groups, a group never straddles two calls unless it alone exceeds
// the limit; groups are packed in sorted order.
func TestBatchedGroupsStayTogether(t *testing.T) {
    qs := map[string]Question{}
    add := func(group string, n int) {
        for i := 0; i < n; i++ {
            qs[fmt.Sprintf("%s#%02d", group, i)] = Question{Kind: KindNoul}
        }
    }
    add("a", 10)
    add("b", 10)
    add("c", 3)
    add("d", 20)
    group := func(key string) string { return key[:1] }
    rec := &recorder{}
    g, ok := Batched(rec, 16).(Groupable)
    if !ok {
        t.Fatal("Batched must be Groupable")
    }
    d, err := g.WithGroups(group).Decide(context.Background(), "s", qs)
    if err != nil {
        t.Fatal(err)
    }
    sort.Slice(rec.calls, func(i, j int) bool { return rec.calls[i][0] < rec.calls[j][0] })
    got := []string{}
    for _, c := range rec.calls {
        groups := map[string]int{}
        for _, k := range c {
            groups[group(k)]++
        }
        got = append(got, fmt.Sprint(groups))
    }
    want := []string{"map[a:10]", "map[b:10 c:3]", "map[d:16]", "map[d:4]"}
    if fmt.Sprint(got) != fmt.Sprint(want) {
        t.Errorf("calls by group = %v, want %v", got, want)
    }
    if len(d.Answers) != 43 {
        t.Errorf("%d answers, want 43", len(d.Answers))
    }
    if n := g.WithGroups(group).Name(); n != "rec" {
        t.Errorf("Name after WithGroups = %q", n)
    }
}

func TestBatchedCancelledContext(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    rec := &recorder{wait: true}
    d, err := Batched(rec, 16).Decide(ctx, "s", questions(21))
    if !errors.Is(err, context.Canceled) || d != nil {
        t.Errorf("Decide = %v, %v; want nil, context.Canceled", d, err)
    }
    if slices.ContainsFunc(rec.calls, func(c []string) bool { return len(c) > 16 }) {
        t.Error("a call exceeded the limit")
    }
}
