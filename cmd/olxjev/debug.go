package main

import (
    "encoding/json"
    "fmt"
    "maps"
    "sort"
    "strings"

    "github.com/mixadior/olxjev"
    "github.com/mixadior/olxjev/decision"
)

// exchange is one decision stage of a resolve, kept for the -web-debug panel.
type exchange struct {
    stage     string
    state     any
    questions map[string]decision.Question
    d         *decision.Decision // nil when the call failed
}

// debugView is the -web-debug panel: the stage-1 questions, then the filters
// of the accepted category and of the categories Jev leaned towards.
type debugView struct {
    Stage1     []filterView
    Categories []categoryView
}

type categoryView struct {
    Key         string
    Label       string
    Probability float64
    Accepted    bool
    Note        string // why the filters have no answers, or what failed
    Filters     []filterView
}

// filterView is one wizard element (or stage-1 question) and what Jev was
// asked about it.
type filterView struct {
    Key       string
    Title     string
    Kind      string
    Outcome   string // what the resolve made of Jev's answer; "" when Jev was not asked
    Set       bool
    Questions []questionView
    Request   string // a Jev request with this filter's questions alone, as JSON
    // Request3 is the stage-3 request with a multi-choice filter's options,
    // sent when its gate passes.
    Request3 string
    Answer   string // Jev's answers to them, as JSON; "" when not answered
}

type questionView struct {
    Key          string
    Type         string
    Instructions string
    Options      []optionView
    Answer       string // «none · 90%», «так · 3%»
}

type optionView struct {
    Name        string
    Desc        string
    Probability string // Jev's, when it answered with probabilities
    Chosen      bool
}

var stage1Questions = []struct{ key, title string }{
    {olxjev.QuestionCategory, "Категорія"},
    {olxjev.QuestionCity, "Місто"},
    {olxjev.QuestionRegion, "Область"},
    {olxjev.QuestionPlace, "Чи названо місце"},
}

// debugView builds the panel from the resolve's exchanges with r, the
// resolver that answered. Filters of the low-confidence categories are the
// questions the model would get (Stage2Plan).
func (s *web) debugView(r *olxjev.Resolver, q string, out output) *debugView {
    dv := &debugView{}
    stage1, stage2 := findExchange(out.exchanges, "stage1"), findExchange(out.exchanges, "stage2")
    if stage3 := findExchange(out.exchanges, "stage3"); stage2 != nil && stage3 != nil && stage2.d != nil && stage3.d != nil {
        // the filters read the answers of both: gates from stage 2, options from stage 3
        answers := maps.Clone(stage2.d.Answers)
        maps.Copy(answers, stage3.d.Answers)
        stage2 = &exchange{stage: "stage2", state: stage2.state, d: &decision.Decision{Answers: answers}}
    }
    res := out.Result

    if stage1 != nil {
        for _, sq := range stage1Questions {
            question, ok := stage1.questions[sq.key]
            if !ok {
                continue
            }
            dv.Stage1 = append(dv.Stage1, s.filterView(r, sq.key, sq.title, string(question.Kind), map[string]decision.Question{sq.key: question}, nil, stage1.state, stage1, res))
        }
    }

    accepted := ""
    if res != nil {
        if f, ok := res.Fields[olxjev.QuestionCategory]; ok {
            accepted = f.Value
            cv := categoryView{Key: f.Value, Label: f.Label, Probability: f.Probability, Accepted: true}
            if stage2 == nil {
                cv.Note = "Етап 2 не виконувався (" + strings.TrimPrefix(out.Error, "olxjev: ") + "): це питання, які отримала б модель."
                if out.Error == "" {
                    cv.Note = "Етап 2 не виконувався: питати не було про що."
                }
            }
            s.fillFilters(r, &cv, q, res, stage2)
            dv.Categories = append(dv.Categories, cv)
        }
    }

    candidates := []olxjev.FieldInfo{}
    if stage1 != nil && stage1.d != nil {
        candidates = r.CategoryCandidates(stage1.d.Answers[olxjev.QuestionCategory])
    } else if res != nil {
        candidates = res.CategoryCandidates
    }
    for _, c := range candidates {
        if c.Value == accepted {
            continue
        }
        cv := categoryView{Key: c.Value, Label: c.Label, Probability: c.Probability,
            Note: "Модель ще не питали: це питання, які вона отримає, якщо обрати цю категорію."}
        if res == nil || res.Fields["city"].Value == "" {
            cv.Note += " Місто не визначали, тож фільтрів міста (району) тут немає."
        }
        s.fillFilters(r, &cv, q, res, nil)
        dv.Categories = append(dv.Categories, cv)
    }
    return dv
}

// fillFilters lists the category's stage-2 filters; ex is the stage-2
// exchange when Jev answered them.
func (s *web) fillFilters(r *olxjev.Resolver, cv *categoryView, q string, res *olxjev.Result, ex *exchange) {
    state, plans, err := r.Stage2Plan(q, cv.Key, res)
    if err != nil {
        cv.Note = err.Error()
        return
    }
    for _, p := range plans {
        cv.Filters = append(cv.Filters, s.filterView(r, p.Key, elementTitle(r.Composer, p.Key), p.Kind, p.Questions, p.Options, state, ex, res))
    }
}

// filterView shows a filter's stage-2 questions, then options: a
// multi-choice filter's stage-3 questions, asked when its gate passes.
func (s *web) filterView(r *olxjev.Resolver, key, title, kind string, questions, options map[string]decision.Question, state any, ex *exchange, res *olxjev.Result) filterView {
    fv := filterView{Key: key, Title: title, Kind: kind}
    var answers map[string]*decision.Answer
    if ex != nil && ex.d != nil {
        answers = ex.d.Answers
    }
    raw := map[string]json.RawMessage{}
    optionsAnswered := false
    for _, set := range []map[string]decision.Question{questions, options} {
        keys := make([]string, 0, len(set))
        for k := range set {
            keys = append(keys, k)
        }
        sort.Strings(keys)
        for _, k := range keys {
            a := answers[k]
            fv.Questions = append(fv.Questions, questionOf(k, set[k], a))
            if a != nil {
                raw[k] = answerJSON(a)
                optionsAnswered = optionsAnswered || options[k].Kind != ""
            }
        }
    }
    request := func(qs map[string]decision.Question) string {
        return prettyJSON(struct {
            State     any                     `json:"state"`
            Model     string                  `json:"model"`
            Questions map[string]decision.Question `json:"questions"`
        }{state, r.Decider.Model(), qs})
    }
    if len(questions) > 0 {
        fv.Request = request(questions)
    }
    if len(options) > 0 {
        fv.Request3 = request(options)
    }
    if len(raw) > 0 {
        fv.Answer = prettyJSON(raw)
    }

    switch {
    case answers == nil:
    case key == olxjev.QuestionPlace:
        // whether a place is named: a gate, not a field
        fv.Outcome = fv.Questions[0].Answer
    case len(options) > 0 && !optionsAnswered && fv.Questions[0].Answer != "":
        fv.Outcome = "не згадано · " + fv.Questions[0].Answer // the gate said no, stage 3 skipped it
    case res != nil && res.Fields[key].Value != "":
        f := res.Fields[key]
        fv.Set = true
        label := f.Label
        if kind == "number" {
            label = groupThousands(f.Value) // the label is the query text around the number
        }
        fv.Outcome = fmt.Sprintf("%s · %d%%", label, int(f.Probability*100+0.5))
        if f.Source == olxjev.SourceDerived {
            fv.Outcome = "з міста: " + f.Label
        }
    case len(fv.Questions) == 1 && fv.Questions[0].Answer != "":
        fv.Outcome = "не встановлено · " + fv.Questions[0].Answer
    case len(fv.Questions) == 0:
        fv.Outcome = "не питали"
    default:
        fv.Outcome = "не встановлено"
    }
    return fv
}

func questionOf(key string, q decision.Question, a *decision.Answer) questionView {
    qv := questionView{Key: key, Type: string(q.Kind), Instructions: q.Instructions}
    for _, o := range q.Options {
        ov := optionView{Name: o.Name, Desc: textOf(o.Desc)}
        if a != nil {
            if p, ok := a.Probabilities[o.Name]; ok {
                ov.Probability = fmt.Sprintf("%d%%", int(p*100+0.5))
            }
            ov.Chosen = a.Choice == o.Name
        }
        qv.Options = append(qv.Options, ov)
    }
    if a != nil {
        switch a.Kind {
        case decision.KindNoul:
            qv.Answer = fmt.Sprintf("так · %d%%", int(a.Yes*100+0.5))
        case decision.KindChoice:
            if p, ok := a.Probabilities[a.Choice]; ok {
                qv.Answer = fmt.Sprintf("%s · %d%%", a.Choice, int(p*100+0.5))
            } else {
                qv.Answer = fmt.Sprintf("%s · confidence %d%%", a.Choice, int(a.Confidence*100+0.5))
            }
        default:
            qv.Answer = string(a.Kind)
        }
    }
    return qv
}

// textOf renders an instruction or option description: strings as they
// are, structured ones (for / not_for / examples) as JSON.
func textOf(v any) string {
    if s, ok := v.(string); ok {
        return s
    }
    b, err := json.Marshal(v)
    if err != nil {
        return fmt.Sprint(v)
    }
    return string(b)
}

func answerJSON(a *decision.Answer) json.RawMessage {
    b, _ := json.Marshal(a) // the provider's answer when there is one
    return b
}

func findExchange(exchanges []exchange, stage string) *exchange {
    for i := range exchanges {
        if exchanges[i].stage == stage {
            return &exchanges[i]
        }
    }
    return nil
}

// prettyJSON indents v without escaping <, > and &, as a person reads it.
func prettyJSON(v any) string {
    var b strings.Builder
    enc := json.NewEncoder(&b)
    enc.SetEscapeHTML(false)
    enc.SetIndent("", "  ")
    if err := enc.Encode(v); err != nil {
        return err.Error()
    }
    return strings.TrimSpace(b.String())
}
