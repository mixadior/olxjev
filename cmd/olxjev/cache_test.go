package main

import "testing"

func TestLRUEvictsLeastRecentlyUsed(t *testing.T) {
    c := newLRU[string, int](2)
    c.Add("a", 1)
    c.Add("b", 2)
    if v, ok := c.Get("a"); !ok || v != 1 { // a is now the most recent
        t.Fatalf("Get(a) = %v, %v", v, ok)
    }
    c.Add("c", 3) // evicts b
    if _, ok := c.Get("b"); ok {
        t.Error("b must be evicted as least recently used")
    }
    for k, want := range map[string]int{"a": 1, "c": 3} {
        if v, ok := c.Get(k); !ok || v != want {
            t.Errorf("Get(%s) = %v, %v; want %d", k, v, ok, want)
        }
    }
    if c.Len() != 2 {
        t.Errorf("Len = %d, want 2", c.Len())
    }
}

func TestLRUAddReplaces(t *testing.T) {
    c := newLRU[string, int](2)
    c.Add("a", 1)
    c.Add("a", 2)
    if v, _ := c.Get("a"); v != 2 || c.Len() != 1 {
        t.Errorf("Get(a) = %d, Len %d; want 2, 1", v, c.Len())
    }
}

func TestLRUZeroCapacityIsDisabled(t *testing.T) {
    c := newLRU[string, int](0)
    c.Add("a", 1)
    if _, ok := c.Get("a"); ok || c.Len() != 0 {
        t.Error("a zero-capacity cache must store nothing")
    }
}

func TestNormalizeQuery(t *testing.T) {
    want := "хочу зняти квартиру в кам'янці"
    for _, q := range []string{
        "хочу зняти квартиру в кам'янці",
        "  Хочу Зняти   квартиру\tв Кам’янці!! ",
        "хочу зняти квартиру в Камʼянці.",
    } {
        if got := normalizeQuery(q); got != want {
            t.Errorf("normalizeQuery(%q) = %q, want %q", q, got, want)
        }
    }
    if normalizeQuery("квартира у Львові") == normalizeQuery("квартира у Києві") {
        t.Error("different places must not share a key")
    }
}
