package olxjev

import (
    "strings"
    "testing"
)

// «в Іванівці» is the locative of «Іванівка» (-ка → -ці; also -га → -зі,
// -ха → -сі): every Іванівка must match as well as villages named Іванівці.
func TestShortlistLocativeOfKa(t *testing.T) {
    g, err := NewGazetteer(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewGazetteer: %v", err)
    }
    got := g.Shortlist("купити будинок в Іванівці", DefaultShortlist)
    for _, c := range got {
        if c.Name == "Іванівка" {
            return
        }
    }
    t.Errorf("shortlist for «в Іванівці» has no Іванівка: %v", got)
}

// A region named in the text ranks its places first: dozens of Іванівка
// match equally, and the region is how the user tells them apart.
func TestShortlistPrefersNamedRegion(t *testing.T) {
    g, err := NewGazetteer(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewGazetteer: %v", err)
    }
    for _, text := range []string{
        "купити будинок в Іванівці, Іванівка, Одеська область",
        "купить дом в Ивановке Одесской области",
        "будинок в Іванівці Одеської області",
    } {
        got := g.Shortlist(text, DefaultShortlist)
        if len(got) == 0 || got[0].RegionID != "9" || !strings.HasPrefix(got[0].Name, "Іванів") {
            t.Errorf("%q: first = %+v, want an Іванів… in Одеська область (9)", text, got)
        }
    }
    // the named region must not push its places above the city the text names
    if got := g.Shortlist("квартира в Харкові, Харківська область", DefaultShortlist); len(got) == 0 || got[0].CityID != "280" {
        t.Errorf("Харків with its region: first = %+v, want Харків", got)
    }
}
