package olxjev

import (
    "context"

    "github.com/mixadior/olxjev/decision"
)

// fakePrice prices fake input tokens like Jev, so costs read as before.
const fakePrice = 0.042

// fakeDecider answers every question with "none" (choice) or 0.01 (noul)
// unless a canned answer for that call index and question key is given.
type fakeDecider struct {
    name    string                        // "" means "fake"
    answers []map[string]*decision.Answer // per call
    errs    []error                       // per call
    tokens  [][2]int                      // per call: input, output tokens reported
    calls   []fakeCall
}

type fakeCall struct {
    state     any
    questions map[string]decision.Question
}

func (f *fakeDecider) Name() string {
    if f.name == "" {
        return "fake"
    }
    return f.name
}

func (f *fakeDecider) Model() string { return "fake-1" }

func (f *fakeDecider) Decide(_ context.Context, state any, qs map[string]decision.Question) (*decision.Decision, error) {
    i := len(f.calls)
    f.calls = append(f.calls, fakeCall{state: state, questions: qs})
    if i < len(f.errs) && f.errs[i] != nil {
        return nil, f.errs[i]
    }
    d := &decision.Decision{Answers: map[string]*decision.Answer{}, Usage: decision.Usage{Calls: 1, Model: "fake-1"}}
    if i < len(f.tokens) {
        d.Usage.InputTokens, d.Usage.OutputTokens = f.tokens[i][0], f.tokens[i][1]
        d.Usage.CostUSD = float64(f.tokens[i][0]) * fakePrice / 1e6
    }
    for key, q := range qs {
        if q.Kind == decision.KindNoul {
            d.Answers[key] = noul(0.01)
        } else {
            d.Answers[key] = choice(NoneOption, 0.9)
        }
    }
    if i < len(f.answers) {
        for key, a := range f.answers[i] {
            d.Answers[key] = a
        }
    }
    return d, nil
}

func choice(option string, confidence float64) *decision.Answer {
    return &decision.Answer{Kind: decision.KindChoice, Choice: option, Confidence: confidence}
}

func noul(p float64) *decision.Answer {
    return &decision.Answer{Kind: decision.KindNoul, Yes: p}
}
