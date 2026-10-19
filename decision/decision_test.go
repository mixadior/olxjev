package decision

import (
    "encoding/json"
    "math"
    "testing"
)

type note struct {
    For      string   `json:"for"`
    Examples []string `json:"examples"`
}

// A choice keeps its options in order, a nil description is null and a
// struct description is an object; a noul has no criteria.
func TestQuestionWireJSON(t *testing.T) {
    choice := Question{Kind: KindChoice, Instructions: "Яка категорія?", Options: []Option{
        {Name: "b", Desc: "друга"},
        {Name: "a", Desc: note{For: "готель", Examples: []string{"на морі"}}},
        {Name: "none", Desc: nil},
    }}
    got, err := json.Marshal(choice)
    if err != nil {
        t.Fatal(err)
    }
    want := `{"type":"choice","instructions":"Яка категорія?","criteria":{"b":"друга","a":{"for":"готель","examples":["на морі"]},"none":null}}`
    if string(got) != want {
        t.Errorf("choice:\n got %s\nwant %s", got, want)
    }
    got, _ = json.Marshal(Question{Kind: KindNoul, Instructions: "Так?"})
    if want := `{"type":"noul","instructions":"Так?"}`; string(got) != want {
        t.Errorf("noul: got %s, want %s", got, want)
    }
}

func TestAnswerUnmarshalKeepsRaw(t *testing.T) {
    raw := `{"type":"choice","choice":"a","probabilities":{"a":0.8,"b":0.2},"confidence":0.6}`
    var a Answer
    if err := json.Unmarshal([]byte(raw), &a); err != nil {
        t.Fatal(err)
    }
    if a.Kind != KindChoice || a.Choice != "a" || a.Probabilities["a"] != 0.8 || a.Confidence != 0.6 || string(a.Raw) != raw {
        t.Errorf("choice answer = %+v", a)
    }
    var n Answer
    if err := json.Unmarshal([]byte(`{"type":"noul","noul":0.92}`), &n); err != nil {
        t.Fatal(err)
    }
    if n.Kind != KindNoul || n.Yes != 0.92 {
        t.Errorf("noul answer = %+v", n)
    }
}

// MarshalJSON returns the provider's answer when there is one, the wire
// form otherwise (fakes in tests).
func TestAnswerMarshal(t *testing.T) {
    raw := json.RawMessage(`{"type":"noul","noul":0.5,"extra":1}`)
    got, _ := json.Marshal(&Answer{Kind: KindNoul, Yes: 0.1, Raw: raw})
    if string(got) != string(raw) {
        t.Errorf("with Raw: got %s", got)
    }
    got, _ = json.Marshal(&Answer{Kind: KindNoul, Yes: 0.25})
    if want := `{"type":"noul","noul":0.25}`; string(got) != want {
        t.Errorf("noul: got %s, want %s", got, want)
    }
    got, _ = json.Marshal(&Answer{Kind: KindChoice, Choice: "x", Confidence: 0.9})
    if want := `{"type":"choice","choice":"x","confidence":0.9}`; string(got) != want {
        t.Errorf("choice: got %s, want %s", got, want)
    }
}

// Probability is the chosen option's probability; without probabilities it
// inverts confidence = (p − 1/n)/(1 − 1/n).
func TestAnswerProbability(t *testing.T) {
    a := &Answer{Kind: KindChoice, Choice: "x", Probabilities: map[string]float64{"x": 0.43}, Confidence: 0.99}
    if p := a.Probability(42); p != 0.43 {
        t.Errorf("with probabilities: %v, want 0.43", p)
    }
    for _, c := range []struct {
        conf float64
        n    int
        want float64
    }{{0.5, 2, 0.75}, {0, 4, 0.25}, {1, 42, 1}, {0.7, 1, 0.7}, {0.7, 0, 0.7}} {
        a := &Answer{Kind: KindChoice, Choice: "x", Confidence: c.conf}
        if p := a.Probability(c.n); math.Abs(p-c.want) > 1e-9 {
            t.Errorf("confidence %v, n %d: %v, want %v", c.conf, c.n, p, c.want)
        }
    }
}

func TestUsageAdd(t *testing.T) {
    u := Usage{Calls: 1, InputTokens: 100, OutputTokens: 1, CostUSD: 0.001, Model: "m1"}
    u.Add(Usage{Calls: 2, InputTokens: 50, CostUSD: 0.0005, Model: "m2"})
    u.Add(Usage{Calls: 1})
    if u.Calls != 4 || u.InputTokens != 150 || u.OutputTokens != 1 || math.Abs(u.CostUSD-0.0015) > 1e-12 || u.Model != "m2" {
        t.Errorf("Usage = %+v", u)
    }
    got, _ := json.Marshal(Usage{Calls: 1})
    if want := `{"calls":1,"input_tokens":0,"output_tokens":0,"cost_usd":0}`; string(got) != want {
        t.Errorf("JSON %s, want %s", got, want)
    }
}
