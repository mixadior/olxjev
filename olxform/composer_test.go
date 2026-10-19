package olxform

import (
    "reflect"
    "slices"
    "sync"
    "testing"

    botform "github.com/mixadior/bfc"
)

var (
    composerOnce sync.Once
    composer     *botform.BotFormComposer
    composerErr  error
)

func testComposer(t *testing.T) *botform.BotFormComposer {
    t.Helper()
    composerOnce.Do(func() { composer, composerErr = NewComposer() })
    if composerErr != nil {
        t.Fatalf("NewComposer: %v", composerErr)
    }
    return composer
}

// These sequences pin the wizard built from the embedded YAML (regenerated
// 2026-10-15 with `olxtoolkit -stage fetch|filters|data`; filters follow
// OLX's display order). A change here changes what /olxwizard asks.
func TestWalkMatchesBotWizard(t *testing.T) {
    cases := []struct {
        name   string
        values map[string]string
        want   []string
    }{
        {
            name: "flat long-term rent in Kharkiv (city with districts)",
            values: map[string]string{
                "region": "8", "city": "280",
                "category_1": "category_1757", "category_1757": "category_1760",
            },
            want: []string{
                "region", "city", "district", "currency", "category_1", "category_1757",
                "filter_float_price:from", "filter_float_price:to",
                "filter_bool_commission",
                "filter_float_floor:from", "filter_float_floor:to",
                "filter_float_total_floors:from", "filter_float_total_floors:to",
                "filter_float_total_area:from", "filter_float_total_area:to",
                "filter_enum_pets", "filter_enum_blackout_autonomy",
                "filter_enum_number_of_rooms_string", "filter_enum_comfort",
                "filter_enum_communications", "filter_enum_infrastructure_500_m",
                "filter_enum_inclusiveness", "owner_type", "search_name",
            },
        },
        {
            name: "house long-term rent, region only",
            values: map[string]string{
                "region": "8",
                "category_1": "category_206", "category_206": "category_330",
            },
            want: []string{
                "region", "city", "currency", "category_1", "category_206",
                "filter_float_price:from", "filter_float_price:to",
                "filter_bool_commission",
                "filter_float_total_floors:from", "filter_float_total_floors:to",
                "filter_float_total_area:from", "filter_float_total_area:to",
                "filter_float_number_of_rooms:from", "filter_float_number_of_rooms:to",
                "filter_float_land_area:from", "filter_float_land_area:to",
                "filter_enum_pets", "filter_enum_blackout_autonomy",
                "filter_enum_comfort_4", "filter_enum_communications",
                "filter_enum_infrastructure_500_m", "filter_enum_inclusiveness",
                "owner_type", "search_name",
            },
        },
    }
    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            got, err := Walk(testComposer(t), c.values)
            if err != nil {
                t.Fatalf("Walk: %v", err)
            }
            if !reflect.DeepEqual(got, c.want) {
                t.Errorf("Walk =\n%v\nwant\n%v", got, c.want)
            }
        })
    }
}

// OLX moved daily rentals to top-level «Житло подобово» (3709); the toolkit
// re-parents it under category_1 so /olxwizard still offers them.
func TestDailyRentReachableUnderRealEstate(t *testing.T) {
    bfc := testComposer(t)
    daily, ok := bfc.Data.Children["category_1"].Children["category_3709"]
    if !ok || daily.Value != "Житло подобово" || len(daily.Children) != 6 {
        t.Fatalf("category_1 › category_3709 = %+v, want «Житло подобово» with 6 leaves", daily)
    }
    got, err := Walk(bfc, map[string]string{
        "region": "8", "category_1": "category_3709", "category_3709": "category_3711",
    })
    if err != nil {
        t.Fatalf("Walk: %v", err)
    }
    want := []string{"region", "city", "currency", "category_1", "category_3709"}
    if len(got) < len(want) || !reflect.DeepEqual(got[:len(want)], want) || !slices.Contains(got, "filter_float_price:to") {
        t.Errorf("Walk = %v, want prefix %v and a price question", got, want)
    }
}

func TestNewComposerOverrides(t *testing.T) {
    bfc := testComposer(t)
    want := map[string]string{"region": "Область", "city": "Місто", "district": "Район міста"}
    for key, title := range want {
        el, ok := bfc.GetElement(key)
        if !ok {
            t.Fatalf("element %q missing", key)
        }
        if got := el.Renderer.GetTitle(); got != title {
            t.Errorf("%s title = %q, want %q", key, got, title)
        }
    }
    if el, _ := bfc.GetElement("currency"); !el.Required {
        t.Error("currency must be required")
    }
    if len(bfc.Data.Children["region"].Children) != 25 {
        t.Errorf("want 25 regions, got %d", len(bfc.Data.Children["region"].Children))
    }
}
