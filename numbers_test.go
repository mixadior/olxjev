package olxjev

import (
    "reflect"
    "testing"
)

func TestExtractNumbersValues(t *testing.T) {
    cases := []struct {
        text string
        want []float64
    }{
        {"хочу зняти однушку квартиру в Харкові до 10к гривень в центрі", []float64{10000}},
        {"до 10 000 грн", []float64{10000}},
        {"до 10тис.", []float64{10000}},
        {"бюджет 10 тисяч", []float64{10000}},
        {"до 12 тыс", []float64{12000}},
        {"дом за 1,5 млн", []float64{1500000}},
        {"дом за 1.5млн", []float64{1500000}},
        {"від 8 до 12 тис", []float64{8000, 12000}},
        {"8-12к", []float64{8000, 12000}},
        {"60м²", []float64{60}},
        {"площа 60 м2 3 поверх", []float64{60, 3}},
        {"кухня 12 кв.м", []float64{12}},
        {"1 кімната", []float64{1}},
        {"2к квартиру", []float64{2}},
        {"2000 грн", []float64{2000}},
        {"квартира 2015 року", []float64{}},
        {"тел 050 123 45 67", []float64{}},
        {"дзвоніть +380501234567", []float64{}},
        {"10к або 10к", []float64{10000}},
        {"без цифр", []float64{}},
    }
    for _, c := range cases {
        got := []float64{}
        for _, m := range ExtractNumbers(c.text) {
            got = append(got, m.Value)
        }
        if !reflect.DeepEqual(got, c.want) {
            t.Errorf("ExtractNumbers(%q) = %v, want %v", c.text, got, c.want)
        }
    }
}

func TestExtractNumbersThousandsSeparators(t *testing.T) {
    cases := []struct {
        text string
        want []float64
    }{
        {"до 10,000 грн", []float64{10000}},
        {"ціна $85,000", []float64{85000}},
        {"до 10.000 грн", []float64{10000}},
        {"будинок за 1,250,000 грн", []float64{1250000}},
        {"дом за 1,5 млн", []float64{1500000}},
        {"площа 10,5 соток", []float64{10.5}},
    }
    for _, c := range cases {
        got := []float64{}
        for _, m := range ExtractNumbers(c.text) {
            got = append(got, m.Value)
        }
        if !reflect.DeepEqual(got, c.want) {
            t.Errorf("ExtractNumbers(%q) = %v, want %v", c.text, got, c.want)
        }
    }
}

func TestExtractNumbersRawAndName(t *testing.T) {
    got := ExtractNumbers("хочу зняти однушку квартиру в Харкові до 10к гривень в центрі")
    if len(got) != 1 {
        t.Fatalf("want 1 mention, got %d", len(got))
    }
    if got[0].Name() != "10000" {
        t.Errorf("Name() = %q, want 10000", got[0].Name())
    }
    if got[0].Raw != "Харкові до 10к гривень в" {
        t.Errorf("Raw = %q", got[0].Raw)
    }
    if n := (NumberMention{Value: 1.5}).Name(); n != "1.5" {
        t.Errorf("Name() = %q, want 1.5", n)
    }
}

func TestExtractNumbersPastedAd(t *testing.T) {
    got := []float64{}
    for _, m := range ExtractNumbers("Продам 3к квартиру 2015 р. 75 м2 5/9 поверх ціна 85 000$ тел. 067 123 45 67") {
        got = append(got, m.Value)
    }
    want := []float64{3, 75, 5, 9, 85000}
    if !reflect.DeepEqual(got, want) {
        t.Errorf("got %v, want %v", got, want)
    }
}
