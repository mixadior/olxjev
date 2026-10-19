// Package decision is olxjev's provider-neutral view of a System One
// decision model: typed questions about a state in, probabilities out.
// Adapters (decision/typesafe for TypeSafe's Jev, decision/subq for SubQ's
// Snap) talk to the providers; the JSON forms here are the System One wire
// format both of them accept.
package decision

import (
    "bytes"
    "context"
    "encoding/json"
)

// Kind is a question type.
type Kind string

const (
    KindNoul   Kind = "noul"   // yes/no: the answer is P(yes)
    KindChoice Kind = "choice" // one of named options
)

// Option is one choice option. Desc is a string, a struct (sent as a JSON
// object, e.g. the structured category notes) or nil (sent as null).
type Option struct {
    Name string
    Desc any
}

// Question is one typed question; Options are used by choices only.
type Question struct {
    Kind         Kind
    Instructions string
    Options      []Option
}

// options marshals as a JSON object that keeps the options in order.
type options []Option

func (o options) MarshalJSON() ([]byte, error) {
    var b bytes.Buffer
    b.WriteByte('{')
    for i, opt := range o {
        if i > 0 {
            b.WriteByte(',')
        }
        k, err := json.Marshal(opt.Name)
        if err != nil {
            return nil, err
        }
        v, err := json.Marshal(opt.Desc)
        if err != nil {
            return nil, err
        }
        b.Write(k)
        b.WriteByte(':')
        b.Write(v)
    }
    b.WriteByte('}')
    return b.Bytes(), nil
}

// MarshalJSON writes the wire form {"type", "instructions", "criteria"};
// criteria is omitted when there are no options.
func (q Question) MarshalJSON() ([]byte, error) {
    return json.Marshal(struct {
        Type         string  `json:"type"`
        Instructions string  `json:"instructions"`
        Criteria     options `json:"criteria,omitempty"`
    }{string(q.Kind), q.Instructions, options(q.Options)})
}

// Answer is one typed answer.
type Answer struct {
    Kind          Kind
    Yes           float64 // noul: the probability of yes
    Choice        string
    Probabilities map[string]float64
    Confidence    float64
    Raw           json.RawMessage // the provider's answer as received
}

type wireAnswer struct {
    Type          string             `json:"type"`
    Noul          *float64           `json:"noul,omitempty"`
    Choice        string             `json:"choice,omitempty"`
    Probabilities map[string]float64 `json:"probabilities,omitempty"`
    Confidence    float64            `json:"confidence,omitempty"`
}

func (a *Answer) UnmarshalJSON(b []byte) error {
    var w wireAnswer
    if err := json.Unmarshal(b, &w); err != nil {
        return err
    }
    *a = Answer{Kind: Kind(w.Type), Choice: w.Choice, Probabilities: w.Probabilities, Confidence: w.Confidence, Raw: append(json.RawMessage(nil), b...)}
    if w.Noul != nil {
        a.Yes = *w.Noul
    }
    return nil
}

// MarshalJSON returns Raw when the answer came from a provider, the wire
// form otherwise.
func (a Answer) MarshalJSON() ([]byte, error) {
    if len(a.Raw) > 0 {
        return a.Raw, nil
    }
    w := wireAnswer{Type: string(a.Kind), Choice: a.Choice, Probabilities: a.Probabilities, Confidence: a.Confidence}
    if a.Kind == KindNoul {
        w.Noul = &a.Yes
    }
    return json.Marshal(w)
}

// Probability is the probability of the chosen option of a question with n
// options. Without probabilities it inverts the documented
// confidence = (p − 1/n) / (1 − 1/n).
func (a *Answer) Probability(n int) float64 {
    if p, ok := a.Probabilities[a.Choice]; ok {
        return p
    }
    if n < 2 {
        return a.Confidence
    }
    u := 1 / float64(n)
    return a.Confidence*(1-u) + u
}

// Usage is what decisions cost, as the provider reported it.
type Usage struct {
    Calls        int     `json:"calls"` // HTTP calls, after batching
    InputTokens  int     `json:"input_tokens"`
    OutputTokens int     `json:"output_tokens"`
    CostUSD      float64 `json:"cost_usd"` // estimate: input tokens × the provider's price
    Model        string  `json:"model,omitempty"`
}

// Add counts o into u; a non-empty o.Model replaces u.Model.
func (u *Usage) Add(o Usage) {
    u.Calls += o.Calls
    u.InputTokens += o.InputTokens
    u.OutputTokens += o.OutputTokens
    u.CostUSD += o.CostUSD
    if o.Model != "" {
        u.Model = o.Model
    }
}

// Decision is the answer to one Decide call.
type Decision struct {
    Answers map[string]*Answer
    Usage   Usage
    Raw     []json.RawMessage // one response body per HTTP call
}

// Decider answers typed questions about a state.
type Decider interface {
    Decide(ctx context.Context, state any, questions map[string]Question) (*Decision, error)
    // Name identifies the backend ("typesafe", "subq") and selects its
    // calibrated thresholds.
    Name() string
    // Model is the model ID the backend sends.
    Model() string
}
