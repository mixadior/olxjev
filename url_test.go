package olxjev

import (
    "context"
    "net/url"
    "reflect"
    "testing"
)

// fakeBreadcrumbs answers olxurl.Build's breadcrumbs request without network.
type fakeBreadcrumbs struct{}

func (fakeBreadcrumbs) Get(_ context.Context, _ string) ([]byte, error) {
    return []byte(`{"data":{"breadcrumbs":[{"href":"/uk/nedvizhimost/kvartiry/prodazha-kvartir/odessa/"}]}}`), nil
}

func TestSearchURLAddsCurrency(t *testing.T) {
    values := map[string]string{
        "category_1": "category_1757", "category_1757": "category_1758",
        "region": "9", "city": "62",
        "currency":              "USD",
        "filter_float_price:to": "50000",
    }
    got, notInURL, err := SearchURL(context.Background(), values, fakeBreadcrumbs{})
    if err != nil {
        t.Fatalf("SearchURL: %v", err)
    }
    u, err := url.Parse(got)
    if err != nil {
        t.Fatalf("parse %q: %v", got, err)
    }
    if c := u.Query().Get("currency"); c != "USD" {
        t.Errorf("currency = %q, want USD in %s", c, got)
    }
    if p := u.Query().Get("search[filter_float_price:to]"); p != "50000" {
        t.Errorf("price:to = %q, want 50000 in %s", p, got)
    }
    if len(notInURL) != 0 {
        t.Errorf("notInURL = %v, want none", notInURL)
    }
}

func TestSearchURLReportsFieldsNotInURL(t *testing.T) {
    values := map[string]string{
        "category_1": "category_1757", "category_1757": "category_1760",
        "owner_type": "private",
    }
    got, notInURL, err := SearchURL(context.Background(), values, fakeBreadcrumbs{})
    if err != nil {
        t.Fatalf("SearchURL: %v", err)
    }
    if !reflect.DeepEqual(notInURL, []string{"owner_type"}) {
        t.Errorf("notInURL = %v, want [owner_type]", notInURL)
    }
    u, _ := url.Parse(got)
    if _, ok := u.Query()["currency"]; ok {
        t.Errorf("currency must not be set when unresolved: %s", got)
    }
}
