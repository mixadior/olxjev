// Package olxjev turns a free-text real-estate request («хочу зняти однушку
// в Харкові до 10к») into the bfc values of the OLX.ua wizard, letting a
// decision model make the wizard's choices (TypeSafe's Jev or SubQ's Snap,
// see olxjev/decision). See docs/superpowers/specs/2026-10-14-olxjev-design.md.
package olxjev

import (
    "context"
    "errors"
    "fmt"
    "maps"
    "sort"
    "strconv"
    "strings"

    "github.com/mixadior/olxjev/decision"
    botform "github.com/mixadior/bfc"
    "github.com/rs/zerolog"
)

const (
    // DefaultThreshold is the minimum Jev probability for an answer to be
    // accepted: the chosen option's probability for a choice, the noul value
    // for a noul. Probability, not Jev's confidence: confidence is normalized
    // by the option count, so one confidence cutoff demands 75% from a
    // two-option question and 51% from the 42 categories.
    DefaultThreshold = 0.5
    // DefaultShortlist is how many city candidates stage 1 offers Jev.
    DefaultShortlist = 8
    // MinCategoryCandidate and MaxCategoryCandidates bound the categories an
    // unresolved category offers the user.
    MinCategoryCandidate  = 0.15
    MaxCategoryCandidates = 3
)

// Field sources.
const (
    SourceModel   = "model"   // answered by the decision model
    SourceDerived = "derived" // region implied by the accepted city
)

var (
    // ErrCategoryUnresolved means stage 1 could not settle the category; no
    // valid OLX URL can be built without it.
    ErrCategoryUnresolved = errors.New("olxjev: category unresolved")
    // ErrLocationUnresolved means the query names a place that matches no
    // single city and no region was given («в Іванівці»); the result's
    // CityCandidates are the shortlisted places, and the user should add the
    // region (область) to the query.
    ErrLocationUnresolved = errors.New("olxjev: place is ambiguous or unknown, add the region (область) to the query")
    // ErrEmptyQuery is returned for a blank query.
    ErrEmptyQuery = errors.New("olxjev: empty query")
)

// TraceFunc observes every decision exchange, once per stage with the
// stage's questions and merged answers; d is nil when the call failed.
type TraceFunc func(stage string, state any, questions map[string]decision.Question, d *decision.Decision)

// FieldInfo describes one resolved field.
type FieldInfo struct {
    Value      string  `json:"value"`
    Label      string  `json:"label"`
    Probability float64 `json:"probability"` // the model's probability for Value (min over options for multi-choice)
    Source     string  `json:"source"`
}

// Result is the outcome of Resolve.
type Result struct {
    // Values has the bfc State.Values shape and feeds olxurl.Build.
    Values map[string]string `json:"values"`
    // Fields describes each resolved element ("category" for the leaf).
    Fields map[string]FieldInfo `json:"fields"`
    // Skipped lists applicable elements left unset, in wizard order.
    Skipped []string `json:"skipped"`
    // CityAlternatives are other spellings of the accepted city that OLX lists
    // separately in the same region («Яремче» / «Яремча»); city is
    // single-choice, so the user picks between the URLs (see SearchURLs).
    // Probability is Jev's probability for the alternative.
    CityAlternatives []FieldInfo `json:"city_alternatives,omitempty"`
    // CategoryCandidates are set with ErrCategoryUnresolved: the categories
    // Jev gave at least MinCategoryCandidate, most probable first (up to
    // MaxCategoryCandidates), for the user to pick from.
    CategoryCandidates []FieldInfo `json:"category_candidates,omitempty"`
    // CityCandidates are set with ErrLocationUnresolved: the shortlisted
    // places, most probable first.
    CityCandidates []FieldInfo `json:"city_candidates,omitempty"`
    // Usage is what this Resolve spent on decisions, with an estimated cost.
    Usage decision.Usage `json:"usage"`
}

// Resolver resolves free-text queries. Configure the exported fields before
// the first Resolve; a Resolver is safe for concurrent use afterwards.
type Resolver struct {
    Composer  *botform.BotFormComposer
    Catalog   *Catalog
    Gazetteer *Gazetteer
    Decider   decision.Decider
    Threshold  float64    // cutoff for every question type left 0 in Thresholds; 0 means DefaultThreshold
    Thresholds Thresholds // per question type, calibrated on the golden corpus
    Shortlist  int        // 0 means DefaultShortlist
    Log        *zerolog.Logger
    Trace      TraceFunc
}

// DefaultThresholds are calibrated on the golden corpus (TestGolden's
// calibration table; 2026-10-15, 58 cases, two runs). Right numeric answers
// came at ≥0.90 and wrong ones up to 0.70 («на 9 поверсі» claimed as the
// building height), and a wrong number narrows the search, so Number is 0.8.
// Multi (with bool filters as nouls): wrong yes-answers stayed below 0.30,
// right ones went down to 0.50–0.55, so Multi is 0.4, between the two.
// Place, Presence and Choice separated cleanly around 0.5. Categories: right
// and wrong picks both fell in 0.40–0.50 (run-to-run spread ±0.07), so 0.5
// stays. Gate (2026-10-17, two runs): gates of wanted fields came at
// 0.85–0.99, weak hints at 0.2–0.5 and untouched fields at 0.06–0.10; a
// wrong yes costs only stage-3 tokens, so Gate is a loose 0.3. On the
// 100-case corpus 42 of 43 wanted gates were ≥0.85; «свердловиною» scored
// 0.32–0.35 against OLX's option label «Скважина», which shares no word with it.
var DefaultThresholds = Thresholds{Category: 0.5, Place: 0.5, Presence: 0.5, Choice: 0.5, Number: 0.8, Multi: 0.4, Gate: 0.3}

// CalibratedThresholds are the thresholds per decision backend (Decider
// Name), each calibrated on the golden corpus; NewResolver falls back to
// DefaultThresholds for a backend missing here.
//
// subq (subq-snap-preview), golden ×2 over 100 cases, 2026-10-18, batched
// by field (16 questions per request): numbers came right at ≥0.97 and
// wrong at ≤0.71 («на 9 поверсі» as the building height again), so Number
// keeps 0.8. Categories: 92 right, 8 wrong in both runs, the wrong ones
// confident daily-rent confusions (rooms vs flats, houses vs hotels:
// 0.82–0.93) no cutoff separates; 0.55 drops the two weakest (0.52, 0.54)
// for one right answer and keeps the 90% floor (0.90 would have kept 84).
// Multi: right yeses down to 0.61, wrong ones up to 0.96; 0.50–0.70 all
// cost 4 errors, 0.5 keeps all 48 right. Place and Choice saw no wrong
// answers (right ≥0.55, ≥0.86), Presence separated 0.96 / 0.02, and
// wanted gates came at ≥0.84 (wrong at 0.47–0.57, only stage-3 tokens).
var CalibratedThresholds = map[string]Thresholds{
    "typesafe": DefaultThresholds,
    "subq":     {Category: 0.55, Place: 0.5, Presence: 0.5, Choice: 0.5, Number: 0.8, Multi: 0.5, Gate: 0.3},
}

// Thresholds are the minimum probabilities per question type; 0 means
// Resolver.Threshold. Types differ in what a wrong answer costs: a wrong
// category or city makes the URL useless, a wrong filter narrows it.
type Thresholds struct {
    Category float64 // the category leaf
    Place    float64 // city and region
    Presence float64 // whether the query names a place at all (noul)
    Choice   float64 // single-choice filters: district, currency
    Number   float64 // numeric filters: price, area, floors…
    Multi    float64 // nouls: one per multi-choice option, one per bool filter
    // Gate is the probability a multi-choice element's gate needs for stage
    // 3 to ask its options; not an acceptance cutoff.
    Gate float64
}

// NewResolver builds the catalog and gazetteer from the composer's data and
// takes the thresholds calibrated for decider's backend.
func NewResolver(composer *botform.BotFormComposer, decider decision.Decider) (*Resolver, error) {
    catalog, err := NewCatalog(composer.Data)
    if err != nil {
        return nil, err
    }
    gazetteer, err := NewGazetteer(composer.Data)
    if err != nil {
        return nil, err
    }
    thresholds := DefaultThresholds
    if decider != nil {
        if t, ok := CalibratedThresholds[decider.Name()]; ok {
            thresholds = t
        }
    }
    if g, ok := decider.(decision.Groupable); ok {
        decider = g.WithGroups(questionGroup)
    }
    return &Resolver{Composer: composer, Catalog: catalog, Gazetteer: gazetteer, Decider: decider, Thresholds: thresholds}, nil
}

// Resolve runs the decision stages for text: 1 = category and location, 2 = the
// category's filters with one gate per multi-choice filter, 3 (only when a
// gate passed) = those filters' options. On a stage-2 or stage-3 failure it
// returns the partial result (category and location) together with the
// error; on ErrCategoryUnresolved the result carries whatever stage 1
// produced.
func (r *Resolver) Resolve(ctx context.Context, text string) (*Result, error) {
    text = strings.TrimSpace(text)
    if text == "" {
        return nil, ErrEmptyQuery
    }
    numbers := ExtractNumbers(text)
    cities := r.Gazetteer.Shortlist(text, r.shortlist())

    q1 := stage1Questions(r.Catalog, cities)
    resp1, err := r.evaluate(ctx, "stage1", text, q1)
    if err != nil {
        return nil, fmt.Errorf("stage1: %w", err)
    }

    res := &Result{Values: map[string]string{}, Fields: map[string]FieldInfo{}}
    res.Usage.Add(resp1.Usage)

    leafKey, conf, ok := r.acceptChoice(resp1, QuestionCategory, q1, r.cutoff(r.Thresholds.Category))
    if !ok {
        res.Skipped = append(res.Skipped, QuestionCategory)
        res.CategoryCandidates = r.CategoryCandidates(resp1.Answers[QuestionCategory])
        return res, ErrCategoryUnresolved
    }
    leaf, _ := r.Catalog.Leaf(leafKey)
    maps.Copy(res.Values, leaf.Values())
    res.Fields[QuestionCategory] = FieldInfo{Value: leaf.Key, Label: leaf.Label, Probability: conf, Source: SourceModel}

    if cityID, conf, ok := r.acceptChoice(resp1, QuestionCity, q1, r.cutoff(r.Thresholds.Place)); ok {
        city := candidateByID(cities, cityID)
        res.Values["region"] = city.RegionID
        res.Values["city"] = city.CityID
        res.Fields["city"] = FieldInfo{Value: city.CityID, Label: city.Label, Probability: conf, Source: SourceModel}
        probabilities := resp1.Answers[QuestionCity].Probabilities
        for _, alt := range citySiblings(city, cities, probabilities) {
            res.CityAlternatives = append(res.CityAlternatives, FieldInfo{Value: alt.CityID, Label: alt.Label, Probability: probabilities[alt.CityID], Source: SourceModel})
        }
        res.Fields["region"] = FieldInfo{Value: city.RegionID, Label: r.Catalog.RegionLabel(city.RegionID), Probability: conf, Source: SourceDerived}
    } else if regionID, conf, ok := r.acceptChoice(resp1, QuestionRegion, q1, r.cutoff(r.Thresholds.Place)); ok {
        res.Values["region"] = regionID
        res.Fields["region"] = FieldInfo{Value: regionID, Label: r.Catalog.RegionLabel(regionID), Probability: conf, Source: SourceModel}
        res.Skipped = append(res.Skipped, "city")
    } else {
        res.Skipped = append(res.Skipped, "region", "city")
        if a := resp1.Answers[QuestionPlace]; a != nil && a.Yes >= r.cutoff(r.Thresholds.Presence) {
            res.CityCandidates = cityCandidates(cities, resp1.Answers[QuestionCity])
            return res, ErrLocationUnresolved
        }
    }

    elements, err := applicableElements(r.Composer, res.Values)
    if err != nil {
        return res, fmt.Errorf("stage2: %w", err)
    }
    q2 := stage2Questions(elements, numbers)
    if len(q2) == 0 {
        return res, nil
    }
    state := stage2State(text, leaf.Label, res)
    resp2, err := r.evaluate(ctx, "stage2", state, q2)
    if err != nil {
        return res, fmt.Errorf("stage2: %w", err)
    }
    res.Usage.Add(resp2.Usage)

    // stage 3: the options of the multi-choice elements whose gate passed
    asked, answers := maps.Clone(q2), maps.Clone(resp2.Answers)
    q3 := map[string]decision.Question{}
    for _, el := range elements {
        if a := answers[gateKey(el.Key)]; el.Kind == kindMulti && a != nil && a.Yes >= r.cutoff(r.Thresholds.Gate) {
            maps.Copy(q3, multiOptionQuestions(el))
        }
    }
    if len(q3) > 0 {
        resp3, err := r.evaluate(ctx, "stage3", state, q3)
        if err != nil {
            return res, fmt.Errorf("stage3: %w", err)
        }
        res.Usage.Add(resp3.Usage)
        maps.Copy(asked, q3)
        maps.Copy(answers, resp3.Answers)
    }
    r.applyStage2(res, elements, numbers, asked, &decision.Decision{Answers: answers})
    return res, nil
}

func (r *Resolver) evaluate(ctx context.Context, stage string, state any, questions map[string]decision.Question) (*decision.Decision, error) {
    d, err := r.Decider.Decide(ctx, state, questions)
    if err != nil {
        d = nil
    }
    if r.Trace != nil {
        r.Trace(stage, state, questions, d)
    }
    if trace, ok := ctx.Value(traceKey{}).(TraceFunc); ok {
        trace(stage, state, questions, d)
    }
    if err != nil {
        return nil, err
    }
    if d == nil || d.Answers == nil {
        return nil, errors.New("the decision model returned no answers")
    }
    return d, nil
}

// acceptChoice validates a "choice" answer: it must name an option that was
// sent, must not be NoneOption and its probability must reach cutoff. It
// returns the choice and its probability.
func (r *Resolver) acceptChoice(resp *decision.Decision, key string, questions map[string]decision.Question, cutoff float64) (string, float64, bool) {
    q, asked := questions[key]
    if !asked {
        return "", 0, false
    }
    a := resp.Answers[key]
    if a == nil {
        return "", 0, false
    }
    options := q.Options
    if !hasOption(options, a.Choice) {
        r.log().Warn().Str("question", key).Str("choice", a.Choice).Msg("olxjev: the model chose an option that was not offered")
        return "", 0, false
    }
    p := a.Probability(len(options))
    if a.Choice == NoneOption || p < cutoff {
        return "", p, false
    }
    return a.Choice, p, true
}

type numberClaim struct {
    key  string
    name string
    conf float64
}

func (r *Resolver) applyStage2(res *Result, elements []pendingElement, numbers []NumberMention, questions map[string]decision.Question, resp *decision.Decision) {
    claims := []numberClaim{}
    for _, el := range elements {
        switch el.Kind {
        case kindChoice:
            value, conf, ok := r.acceptChoice(resp, el.Key, questions, r.cutoff(r.Thresholds.Choice))
            if !ok {
                res.Skipped = append(res.Skipped, el.Key)
                continue
            }
            res.Values[el.Key] = value
            res.Fields[el.Key] = FieldInfo{Value: value, Label: el.Options[value], Probability: conf, Source: SourceModel}
        case kindBool:
            a := resp.Answers[el.Key]
            if a == nil || a.Yes < r.cutoff(r.Thresholds.Multi) {
                res.Skipped = append(res.Skipped, el.Key)
                continue
            }
            res.Values[el.Key] = "1"
            res.Fields[el.Key] = FieldInfo{Value: "1", Label: el.Options["1"], Probability: a.Yes, Source: SourceModel}
        case kindMulti:
            accepted, labels, minConf := []string{}, []string{}, 1.0
            if _, asked := questions[multiKey(el.Key, sortedKeys(el.Options)[0])]; !asked {
                res.Skipped = append(res.Skipped, el.Key) // its gate said the query doesn't mention it
                continue
            }
            for _, option := range sortedKeys(el.Options) {
                a := resp.Answers[multiKey(el.Key, option)]
                if a == nil || a.Yes < r.cutoff(r.Thresholds.Multi) {
                    continue
                }
                accepted = append(accepted, option)
                labels = append(labels, el.Options[option])
                minConf = min(minConf, a.Yes)
            }
            if g, ok := elementGroup(el); ok && len(accepted) == 0 {
                // no specific option: the group («any pet») selects all of its options
                if a := resp.Answers[multiKey(el.Key, groupOption)]; a != nil && a.Yes >= r.cutoff(r.Thresholds.Multi) {
                    accepted, labels, minConf = g.Options, []string{g.Label}, a.Yes
                }
            }
            if len(accepted) == 0 {
                res.Skipped = append(res.Skipped, el.Key)
                continue
            }
            value := strings.Join(accepted, el.Separator)
            res.Values[el.Key] = value
            res.Fields[el.Key] = FieldInfo{Value: value, Label: strings.Join(labels, ", "), Probability: minConf, Source: SourceModel}
        case kindNumber:
            name, conf, ok := r.acceptChoice(resp, el.Key, questions, r.cutoff(r.Thresholds.Number))
            if !ok {
                res.Skipped = append(res.Skipped, el.Key)
                continue
            }
            claims = append(claims, numberClaim{key: el.Key, name: name, conf: conf})
        }
    }

    kept, dropped := settleNumberClaims(claims)
    kept, moved := barePriceIsMax(kept, numbers)
    dropped = append(dropped, moved...)
    raw := map[string]string{}
    for _, m := range numbers {
        raw[m.Name()] = m.Raw
    }
    for _, c := range kept {
        res.Values[c.key] = c.name
        res.Fields[c.key] = FieldInfo{Value: c.name, Label: raw[c.name], Probability: c.conf, Source: SourceModel}
    }
    for _, c := range dropped {
        res.Skipped = append(res.Skipped, c.key)
    }
}

// settleNumberClaims enforces two rules, keeping claims in input order:
//  1. one number belongs to at most one field family (price, area, floor, …);
//     the family holding the most confident claim on it wins; when two
//     families tie for the top confidence the number is ambiguous and every
//     claim on it is dropped;
//  2. within a family, from must not exceed to; the less confident side is dropped.
func settleNumberClaims(claims []numberClaim) (kept, dropped []numberClaim) {
    winner := map[string]numberClaim{} // number -> most confident claim
    for _, c := range claims {
        if w, ok := winner[c.name]; !ok || c.conf > w.conf {
            winner[c.name] = c
        }
    }
    ambiguous := map[string]bool{} // numbers whose top confidence is shared by two families
    for _, c := range claims {
        w := winner[c.name]
        if c.conf == w.conf && family(c.key) != family(w.key) {
            ambiguous[c.name] = true
        }
    }
    stage := []numberClaim{}
    for _, c := range claims {
        if !ambiguous[c.name] && family(c.key) == family(winner[c.name].key) {
            stage = append(stage, c)
        } else {
            dropped = append(dropped, c)
        }
    }

    bounds := map[string]map[string]numberClaim{} // family -> "from"/"to" -> claim
    for _, c := range stage {
        f := family(c.key)
        if bounds[f] == nil {
            bounds[f] = map[string]numberClaim{}
        }
        bounds[f][bound(c.key)] = c
    }
    for _, c := range stage {
        b := bounds[family(c.key)]
        from, hasFrom := b["from"]
        to, hasTo := b["to"]
        if hasFrom && hasTo && parseNumber(from.name) > parseNumber(to.name) {
            loser := from
            if from.conf > to.conf {
                loser = to
            }
            if c.key == loser.key {
                dropped = append(dropped, c)
                continue
            }
        }
        kept = append(kept, c)
    }
    return kept, dropped
}

const priceFamily = "filter_float_price"

// barePriceIsMax reads a price said without «від» as a budget — the user
// wants it or cheaper. A lower-bound price claim on a number that was not
// said as a minimum moves to the upper bound (or is dropped when the upper
// bound already holds that number); a number said as a minimum keeps only
// its lower-bound claim. Other families keep Jev's bounds: «40 м2» is no
// maximum. The returned dropped claims name the bounds left unset.
func barePriceIsMax(claims []numberClaim, numbers []NumberMention) (kept, dropped []numberClaim) {
    minimum := map[string]bool{}
    for _, m := range numbers {
        minimum[m.Name()] = m.lowerBound
    }
    from, to := -1, -1
    for i, c := range claims {
        switch c.key {
        case priceFamily + ":from":
            from = i
        case priceFamily + ":to":
            to = i
        }
    }
    drop := -1
    switch {
    case from < 0:
    case minimum[claims[from].name]:
        if to >= 0 && claims[to].name == claims[from].name {
            drop = to
        }
    case to < 0:
        claims[from].key = priceFamily + ":to"
        dropped = append(dropped, numberClaim{key: priceFamily + ":from"})
    case claims[to].name == claims[from].name:
        drop = from
    }
    for i, c := range claims {
        if i == drop {
            dropped = append(dropped, c)
            continue
        }
        kept = append(kept, c)
    }
    return kept, dropped
}

// questionGroup keeps related questions in one request for a backend that
// splits them (SubQ: 16 per request; its answers shift with the questions
// they share a request with): a field's options with its option group and
// gate, and the bounds of one number family.
func questionGroup(key string) string {
    if i := strings.Index(key, "#"); i >= 0 {
        return key[:i]
    }
    return family(strings.TrimPrefix(key, gateKey("")))
}

// family strips the :from / :to suffix: filter_float_price:to -> filter_float_price.
func family(key string) string {
    if i := strings.LastIndex(key, ":"); i >= 0 {
        return key[:i]
    }
    return key
}

func bound(key string) string {
    if i := strings.LastIndex(key, ":"); i >= 0 {
        return key[i+1:]
    }
    return ""
}

func parseNumber(s string) float64 {
    v, _ := strconv.ParseFloat(s, 64)
    return v
}

func hasOption(options []decision.Option, name string) bool {
    for _, o := range options {
        if o.Name == name {
            return true
        }
    }
    return false
}

func candidateByID(cities []CityCandidate, id string) CityCandidate {
    for _, c := range cities {
        if c.CityID == id {
            return c
        }
    }
    return CityCandidate{}
}

// CategoryCandidates lists the categories Jev gave at least
// MinCategoryCandidate, most probable first, at most MaxCategoryCandidates.
func (r *Resolver) CategoryCandidates(answer *decision.Answer) []FieldInfo {
    if answer == nil {
        return nil
    }
    out := []FieldInfo{}
    for key, p := range answer.Probabilities {
        if leaf, ok := r.Catalog.Leaf(key); ok && p >= MinCategoryCandidate {
            out = append(out, FieldInfo{Value: key, Label: leaf.Label, Probability: p, Source: SourceModel})
        }
    }
    sort.Slice(out, func(i, j int) bool {
        if out[i].Probability != out[j].Probability {
            return out[i].Probability > out[j].Probability
        }
        return out[i].Value < out[j].Value
    })
    if len(out) > MaxCategoryCandidates {
        out = out[:MaxCategoryCandidates]
    }
    if len(out) == 0 {
        return nil
    }
    return out
}

// cityCandidates lists the shortlist with Jev's probabilities from the city
// answer, most probable first; ties keep the shortlist's order.
func cityCandidates(cities []CityCandidate, answer *decision.Answer) []FieldInfo {
    var probabilities map[string]float64
    if answer != nil {
        probabilities = answer.Probabilities
    }
    out := make([]FieldInfo, 0, len(cities))
    for _, c := range cities {
        out = append(out, FieldInfo{Value: c.CityID, Label: c.Label, Probability: probabilities[c.CityID], Source: SourceModel})
    }
    sort.SliceStable(out, func(i, j int) bool { return out[i].Probability > out[j].Probability })
    return out
}

// cutoff is the threshold for a question type: its own, else Threshold,
// else DefaultThreshold.
func (r *Resolver) cutoff(typed float64) float64 {
    if typed > 0 {
        return typed
    }
    if r.Threshold > 0 {
        return r.Threshold
    }
    return DefaultThreshold
}

func (r *Resolver) shortlist() int {
    if r.Shortlist > 0 {
        return r.Shortlist
    }
    return DefaultShortlist
}

var nopLogger = zerolog.Nop()

func (r *Resolver) log() *zerolog.Logger {
    if r.Log == nil {
        return &nopLogger
    }
    return r.Log
}
