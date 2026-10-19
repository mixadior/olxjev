package olxjev

import (
    "strings"
    "testing"
)

func TestShortlistFindsCity(t *testing.T) {
    g, err := NewGazetteer(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewGazetteer: %v", err)
    }
    cases := []struct {
        text     string
        cityID   string // must be in the shortlist
        regionID string
        top      bool // must be the first candidate
    }{
        {"хочу зняти однушку квартиру в Харкові до 10к гривень в центрі", "280", "8", true},
        {"снять квартиру в Харькове", "280", "8", true},
        {"квартира у Києві на Печерську", "268", "25", true},
        {"снять квартиру в Киеве", "268", "25", true},
        {"купити будинок під Львовом", "176", "5", true},
        {"квартира в Одессе у моря", "62", "9", true},
        {"квартира в Днепре", "121", "21", true},
        {"квартира в Николаеве", "106", "19", true},
        {"квартира в Запорожье", "194", "17", true},
        {"дом в Ирпене", "295", "25", false},
        {"будинок у Білій Церкві", "60", "25", false},
        {"квартира в Івано-Франківську", "132", "13", true},
        {"будинок у Кам'янському", "1", "21", false},
        {"будинок у Камʼянському", "1", "21", false},
        {"кімнату в Вінниці до 5000", "313", "24", true},
    }
    for _, c := range cases {
        got := g.Shortlist(c.text, 8)
        idx := -1
        for i, cand := range got {
            if cand.CityID == c.cityID {
                idx = i
                if cand.RegionID != c.regionID {
                    t.Errorf("%q: city %s region = %s, want %s", c.text, c.cityID, cand.RegionID, c.regionID)
                }
            }
        }
        if idx < 0 {
            t.Errorf("%q: city %s not in shortlist %v", c.text, c.cityID, got)
        } else if c.top && idx != 0 {
            t.Errorf("%q: city %s at position %d, want first; shortlist %v", c.text, c.cityID, idx, got)
        }
    }
}

// Multi-word names must win over single-word villages matching one of their
// words, and a short alternating last word («Ріг» / «Розі» / «Роге») must not
// block the match.
func TestShortlistMultiWordCities(t *testing.T) {
    g, err := NewGazetteer(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewGazetteer: %v", err)
    }
    cases := []struct {
        text   string
        cityID string
    }{
        {"квартира у Кривому Розі", "340"},
        {"снять квартиру в Кривом Роге", "340"},
        {"купити будинок у Кривий Ріг", "340"},
        {"квартира в Новій Каховці", "374"},
        {"будинок в Кам'янці-Подільському", "107"},
    }
    for _, c := range cases {
        got := g.Shortlist(c.text, 8)
        if len(got) == 0 || got[0].CityID != c.cityID {
            t.Errorf("%q: want city %s first, got %v", c.text, c.cityID, got)
        }
    }
}

func TestShortlistLabelsCarryRegion(t *testing.T) {
    g, err := NewGazetteer(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewGazetteer: %v", err)
    }
    // 100+ villages are called Іванівка: the region in the label is what lets Jev tell them apart
    got := g.Shortlist("будинок в Іванівці", 8)
    if len(got) != 8 {
        t.Fatalf("want a full shortlist of 8, got %d", len(got))
    }
    for _, c := range got {
        if !strings.Contains(c.Label, " область") && !strings.Contains(c.Label, "Крим") {
            t.Errorf("label %q has no region part", c.Label)
        }
    }
}

func TestShortlistNoCity(t *testing.T) {
    g, err := NewGazetteer(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewGazetteer: %v", err)
    }
    if got := g.Shortlist("хочу зняти квартиру", 8); len(got) != 0 {
        t.Errorf("want empty shortlist, got %v", got)
    }
}

func TestShortlistTwoCities(t *testing.T) {
    g, err := NewGazetteer(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewGazetteer: %v", err)
    }
    got := g.Shortlist("переїжджаю з Києва до Львова, шукаю квартиру у Львові", 8)
    ids := map[string]bool{}
    for _, c := range got {
        ids[c.CityID] = true
    }
    if !ids["268"] || !ids["176"] {
        t.Errorf("want both Kyiv and Lviv, got %v", got)
    }
}

func TestShortlistLatinIsEmpty(t *testing.T) {
    g, err := NewGazetteer(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewGazetteer: %v", err)
    }
    if got := g.Shortlist("kvartyra kharkiv 10k", 8); len(got) != 0 {
        t.Errorf("want empty shortlist for Latin input, got %v", got)
    }
}
