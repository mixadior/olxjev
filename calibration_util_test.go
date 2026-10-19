package olxjev

import (
    "fmt"
    "slices"
    "sort"
    "strings"

    "github.com/mixadior/olxjev/decision"
)

// calSample is one judgment the golden corpus can grade: for a choice, Jev's
// pick (when not none) and its probability; for a noul, its value and
// whether the true answer is yes. Missed marks an expected value Jev never
// offered (answered none, or the question was not asked) — no cutoff fixes it.
type calSample struct {
    Type    string // a Thresholds field name
    P       float64
    Correct bool
    Missed  bool
    Pick    string // what Jev answered: the option, "key=option", or the noul's key
    Query   string // set by TestGolden
}

// calibrationCutoffs are the cutoffs calibrationReport tabulates.
var calibrationCutoffs = []float64{0.30, 0.35, 0.40, 0.45, 0.50, 0.55, 0.60, 0.65, 0.70, 0.75, 0.80, 0.85, 0.90}

// collectSamples grades Jev's raw answers for one golden case against its
// expectations, before any of Resolve's rules (thresholds, number settling)
// apply. Region is graded only when no city is expected: otherwise its
// answer is an unused speculative branch.
func collectSamples(expect map[string]string, questions map[string]decision.Question, answers map[string]*decision.Answer) []calSample {
    samples := []calSample{}
    pick := func(typ, key, want string) {
        q, asked := questions[key]
        a := answers[key]
        if !asked || a == nil || a.Choice == NoneOption {
            if want != "" {
                samples = append(samples, calSample{Type: typ, Missed: true, Pick: key + "=" + NoneOption})
            }
            return
        }
        options := q.Options
        label := a.Choice
        if typ == "Number" || typ == "Choice" {
            label = key + "=" + a.Choice
        }
        samples = append(samples, calSample{Type: typ, P: a.Probability(len(options)), Correct: slices.Contains(alternatives(want), a.Choice), Pick: label})
    }

    for key, want := range expect {
        switch {
        case key == "error":
        case key == QuestionCategory:
            pick("Category", QuestionCategory, want)
        case key == "city":
            pick("Place", QuestionCity, want)
        case key == "region":
            if _, hasCity := expect["city"]; !hasCity {
                pick("Place", QuestionRegion, want)
            }
        case strings.HasPrefix(key, "filter_float_"):
            pick("Number", key, want)
        default:
            if q, single := questions[key]; single && q.Kind == decision.KindNoul { // bool filter
                if a := answers[key]; a != nil {
                    samples = append(samples, calSample{Type: "Multi", P: a.Yes, Correct: want == "1", Pick: key})
                }
                continue
            } else if single {
                pick("Choice", key, want)
                continue
            }
            if a := answers[gateKey(key)]; a != nil {
                samples = append(samples, calSample{Type: "Gate", P: a.Yes, Correct: want != "", Pick: gateKey(key)})
            }
            groupKey := multiKey(key, groupOption)
            if g, ok := optionGroups[key]; ok && want == strings.Join(g.Options, ";") && answers[groupKey] != nil {
                // the whole group expected («з питомцем»): grade the group noul
                samples = append(samples, calSample{Type: "Multi", P: answers[groupKey].Yes, Correct: true, Pick: groupKey})
                continue
            }
            options := []string{}
            for qk := range questions {
                if strings.HasPrefix(qk, key+"#") && qk != multiKey(key, groupOption) {
                    options = append(options, strings.TrimPrefix(qk, key+"#"))
                }
            }
            if len(options) == 0 {
                if why := " (not asked)"; want != "" {
                    if answers[gateKey(key)] != nil {
                        why = " (gate said no)"
                    }
                    samples = append(samples, calSample{Type: "Multi", Missed: true, Pick: key + why})
                }
                continue
            }
            wanted := strings.Split(want, ";")
            for _, option := range options {
                if a := answers[multiKey(key, option)]; a != nil {
                    samples = append(samples, calSample{Type: "Multi", P: a.Yes, Correct: slices.Contains(wanted, option), Pick: multiKey(key, option)})
                }
            }
        }
    }

    if a := answers[QuestionPlace]; a != nil {
        named := expect["city"] != "" || expect["region"] != "" || expect["error"] == "location"
        samples = append(samples, calSample{Type: "Presence", P: a.Yes, Correct: named, Pick: QuestionPlace})
    }
    sort.SliceStable(samples, func(i, j int) bool {
        if samples[i].Type != samples[j].Type {
            return samples[i].Type < samples[j].Type
        }
        if samples[i].P != samples[j].P {
            return samples[i].P < samples[j].P
        }
        return samples[i].Pick < samples[j].Pick
    })
    return samples
}

// calibrationReport tabulates, per question type and cutoff, how many right
// answers the cutoff keeps and how many wrong ones it lets through
// ("0.50=38:1"), after a "right/wrong/missed" count. For nouls, right means
// the true answer is yes.
func calibrationReport(samples []calSample) string {
    byType := map[string][]calSample{}
    for _, s := range samples {
        byType[s.Type] = append(byType[s.Type], s)
    }
    types := make([]string, 0, len(byType))
    for typ := range byType {
        types = append(types, typ)
    }
    sort.Strings(types)

    var b strings.Builder
    b.WriteString("calibration — per type: right/wrong/missed, then cutoff=kept right:let wrong through\n")
    for _, typ := range types {
        right, wrong, missed := 0, 0, 0
        rightSum, rightMin, wrongMax := 0.0, 1.0, 0.0
        for _, s := range byType[typ] {
            switch {
            case s.Missed:
                missed++
            case s.Correct:
                right++
                rightSum += s.P
                rightMin = min(rightMin, s.P)
            default:
                wrong++
                wrongMax = max(wrongMax, s.P)
            }
        }
        rightMean := 0.0
        if right > 0 {
            rightMean = rightSum / float64(right)
        }
        fmt.Fprintf(&b, "  %-8s  %d/%d/%d  right mean %.2f min %.2f, wrong max %.2f\n   ", typ, right, wrong, missed, rightMean, rightMin, wrongMax)
        for _, c := range calibrationCutoffs {
            kept, through := 0, 0
            for _, s := range byType[typ] {
                if s.Missed || s.P < c-1e-9 {
                    continue
                }
                if s.Correct {
                    kept++
                } else {
                    through++
                }
            }
            fmt.Fprintf(&b, " %.2f=%d:%d", c, kept, through)
        }
        b.WriteString("\n")
    }

    // the answers a cutoff decides: wrong ones a low cutoff would let
    // through, right ones the default rejects
    for _, s := range samples {
        switch {
        case s.Missed:
        case !s.Correct && s.P >= calibrationCutoffs[0]:
            fmt.Fprintf(&b, "  %-6s %-8s %.2f %s «%s»\n", "wrong", s.Type, s.P, s.Pick, s.Query)
        case s.Correct && s.P < DefaultThreshold:
            fmt.Fprintf(&b, "  %-6s %-8s %.2f %s «%s»\n", "low", s.Type, s.P, s.Pick, s.Query)
        }
    }
    for _, s := range samples {
        if s.Missed {
            fmt.Fprintf(&b, "  %-6s %-8s %s «%s»\n", "missed", s.Type, s.Pick, s.Query)
        }
    }
    return b.String()
}
