package olxjev

import (
    "reflect"
    "strings"
    "testing"

    "github.com/mixadior/olxjev/decision"
)

func TestCollectSamples(t *testing.T) {
    expect := map[string]string{
        "category":                           "category_1760",
        "city":                               "280",
        "filter_float_price:to":              "10000",
        "filter_float_price:from":            "",
        "currency":                           "UAH",
        "filter_bool_commission":             "1",
        "filter_enum_number_of_rooms_string": "odnokomnatnye",
    }
    opts := func(names ...string) []decision.Option {
        o := []decision.Option{}
        for _, n := range names {
            o = append(o, decision.Option{Name: n})
        }
        return append(o, decision.Option{Name: NoneOption})
    }
    questions := map[string]decision.Question{
        QuestionCategory:          {Kind: decision.KindChoice, Options: opts("category_1760", "category_1758")},
        QuestionCity:              {Kind: decision.KindChoice, Options: opts("280", "281")},
        QuestionRegion:            {Kind: decision.KindChoice, Options: opts("8")},
        QuestionPlace:             {Kind: decision.KindNoul},
        "filter_float_price:to":   {Kind: decision.KindChoice, Options: opts("10000")},
        "filter_float_price:from": {Kind: decision.KindChoice, Options: opts("10000")},
        "currency":                {Kind: decision.KindChoice, Options: opts("UAH", "USD")},
        "filter_bool_commission":  {Kind: decision.KindNoul},
        "filter_enum_number_of_rooms_string#odnokomnatnye": {Kind: decision.KindNoul},
        "filter_enum_number_of_rooms_string#dvuhkomnatnye": {Kind: decision.KindNoul},
        "filter_enum_number_of_rooms_string#" + groupOption: {Kind: decision.KindNoul}, // a group: not graded as an option
    }
    answers := map[string]*decision.Answer{
        QuestionCategory:          choiceP("category_1760", 0.9, 3),
        QuestionCity:              choiceP("281", 0.6, 3), // wrong city
        QuestionRegion:            choiceP("8", 0.8, 2),   // unused: city is expected
        QuestionPlace:             noul(0.95),
        "filter_float_price:to":   choiceP("10000", 0.7, 2),
        "filter_float_price:from": choiceP("10000", 0.55, 2), // claimed though unset is expected
        "currency":                choiceP(NoneOption, 0.6, 3), // Jev said none: missed
        "filter_bool_commission":  noul(0.8),
        "filter_enum_number_of_rooms_string#odnokomnatnye": noul(0.9),
        "filter_enum_number_of_rooms_string#dvuhkomnatnye": noul(0.3),
        "filter_enum_number_of_rooms_string#" + groupOption: noul(0.99),
    }
    got := collectSamples(expect, questions, answers)
    want := []calSample{
        {Type: "Category", P: 0.9, Correct: true, Pick: "category_1760"},
        {Type: "Choice", Missed: true, Pick: "currency=none"},
        {Type: "Multi", P: 0.3, Correct: false, Pick: "filter_enum_number_of_rooms_string#dvuhkomnatnye"},
        {Type: "Multi", P: 0.8, Correct: true, Pick: "filter_bool_commission"},
        {Type: "Multi", P: 0.9, Correct: true, Pick: "filter_enum_number_of_rooms_string#odnokomnatnye"},
        {Type: "Number", P: 0.55, Correct: false, Pick: "filter_float_price:from=10000"},
        {Type: "Number", P: 0.7, Correct: true, Pick: "filter_float_price:to=10000"},
        {Type: "Place", P: 0.6, Correct: false, Pick: "281"},
        {Type: "Presence", P: 0.95, Correct: true, Pick: "place"},
    }
    if !reflect.DeepEqual(got, want) {
        t.Errorf("collectSamples =\n%+v\nwant\n%+v", got, want)
    }
}

func TestCalibrationReport(t *testing.T) {
    samples := []calSample{
        {Type: "Category", P: 0.95, Correct: true},
        {Type: "Category", P: 0.43, Correct: true},
        {Type: "Category", P: 0.40, Correct: false},
        {Type: "Category", Missed: true},
    }
    samples[1].Query, samples[1].Pick = "готель на морі", "category_3713"
    samples[2].Query, samples[2].Pick = "купити оселю в Києві", "category_1602"
    report := calibrationReport(samples)
    for _, want := range []string{
        "Category  2/1/1  right mean 0.69 min 0.43, wrong max 0.40", // right / wrong / missed, margins
        "0.30=2:1", "0.45=1:0", "0.90=1:0", // kept right : let wrong through
        "wrong  Category 0.40 category_1602 «купити оселю в Києві»",
        "low    Category 0.43 category_3713 «готель на морі»",
    } {
        if !strings.Contains(report, want) {
            t.Errorf("report lacks %q:\n%s", want, report)
        }
    }
}
