package olxjev

import (
    "reflect"
    "testing"
)

func TestCatalogLeaves(t *testing.T) {
    c, err := NewCatalog(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewCatalog: %v", err)
    }
    // 2026-10 data: commercial property split into sub-types, daily rent
    // re-parented from «Житло подобово»
    if len(c.Leaves) != 41 {
        t.Fatalf("want 41 leaves, got %d", len(c.Leaves))
    }
    leaf, ok := c.Leaf("category_1760")
    if !ok {
        t.Fatal("category_1760 missing")
    }
    if leaf.Label != "Нерухомість › Квартири › Довгострокова оренда квартир" {
        t.Errorf("Label = %q", leaf.Label)
    }
    want := map[string]string{"category_1": "category_1757", "category_1757": "category_1760"}
    if !reflect.DeepEqual(leaf.Values(), want) {
        t.Errorf("Values = %v, want %v", leaf.Values(), want)
    }
    abroad, _ := c.Leaf("category_1522")
    if !reflect.DeepEqual(abroad.Values(), map[string]string{"category_1": "category_1522"}) {
        t.Errorf("depth-1 leaf Values = %v", abroad.Values())
    }
}

func TestCatalogLeafOfRoundTrip(t *testing.T) {
    c, err := NewCatalog(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewCatalog: %v", err)
    }
    for _, leaf := range c.Leaves {
        if got := c.LeafOf(leaf.Values()); got != leaf.Key {
            t.Errorf("LeafOf(%v) = %q, want %q", leaf.Values(), got, leaf.Key)
        }
    }
    if got := c.LeafOf(map[string]string{}); got != "" {
        t.Errorf("LeafOf(empty) = %q, want empty", got)
    }
}

func TestCatalogRegions(t *testing.T) {
    c, err := NewCatalog(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewCatalog: %v", err)
    }
    if len(c.Regions) != 25 {
        t.Fatalf("want 25 regions, got %d", len(c.Regions))
    }
    if got := c.RegionLabel("8"); got != "Харківська область" {
        t.Errorf("RegionLabel(8) = %q", got)
    }
}
