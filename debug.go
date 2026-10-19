package olxjev

import (
    "context"
    "fmt"
    "strings"

    "github.com/mixadior/olxjev/decision"
)

type traceKey struct{}

// WithTrace returns a context whose Resolve calls also report every decision
// exchange to fn, in addition to Resolver.Trace: a per-request trace for
// a shared Resolver.
func WithTrace(ctx context.Context, fn TraceFunc) context.Context {
    return context.WithValue(ctx, traceKey{}, fn)
}

// FilterPlan is what stage 2 asks the decision model about one wizard element.
type FilterPlan struct {
    Key   string `json:"key"`
    Title string `json:"title"` // the wizard's title, as in the YAML
    Kind  string `json:"kind"`  // "choice", "multi", "number" or "bool"
    // Questions are what stage 2 sends, keyed as Resolve sends them: the
    // element key, or gateKey for a multi-choice element. Empty when the
    // element is not asked: a number field and a query without numbers.
    Questions map[string]decision.Question `json:"questions"`
    // Options are a multi-choice element's stage-3 questions, sent when its
    // gate passes: element#option per option (and option group).
    Options map[string]decision.Question `json:"options,omitempty"`
}

var kindNames = map[elementKind]string{kindChoice: "choice", kindMulti: "multi", kindNumber: "number", kindBool: "bool"}

// Stage2Plan returns the stage-2 state (stage 3 reuses it) and the
// questions, per element in wizard order, that Resolve sends for text once
// the category is leafKey, without calling Jev. The location comes from res (its city or region,
// as Resolve accepted them); res may be nil.
func (r *Resolver) Stage2Plan(text, leafKey string, res *Result) (any, []FilterPlan, error) {
    leaf, ok := r.Catalog.Leaf(leafKey)
    if !ok {
        return nil, nil, fmt.Errorf("olxjev: unknown category %q", leafKey)
    }
    text = strings.TrimSpace(text)
    values := leaf.Values()
    if res != nil {
        for _, k := range []string{"region", "city"} {
            if v, ok := res.Values[k]; ok {
                values[k] = v
            }
        }
    }
    elements, err := applicableElements(r.Composer, values)
    if err != nil {
        return nil, nil, err
    }
    numbers := ExtractNumbers(text)
    plans := make([]FilterPlan, 0, len(elements))
    for _, el := range elements {
        plan := FilterPlan{
            Key:       el.Key,
            Title:     el.Title,
            Kind:      kindNames[el.Kind],
            Questions: stage2Questions([]pendingElement{el}, numbers),
        }
        if el.Kind == kindMulti {
            plan.Options = multiOptionQuestions(el)
        }
        plans = append(plans, plan)
    }
    return stage2State(text, leaf.Label, res), plans, nil
}

// stage2State is the state stage 2 sends: the query and what stage 1
// settled — the category and the city, or the region without a city.
func stage2State(text, category string, res *Result) map[string]any {
    resolved := map[string]string{"Категорія": category}
    if res != nil {
        if f, ok := res.Fields["city"]; ok {
            resolved["Місто"] = f.Label
        } else if f, ok := res.Fields["region"]; ok {
            resolved["Область"] = f.Label
        }
    }
    return map[string]any{"query": text, "resolved": resolved}
}
