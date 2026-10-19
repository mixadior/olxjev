package olxjev

import (
    "context"
    "os"
    "strings"
    "testing"

    "github.com/mixadior/olxjev/request"
    "github.com/mixadior/olxjev/olxurl"
)

// TestLiveLeafURLsKeepCategory builds a real OLX URL for every category leaf
// and fails when OLX's breadcrumbs no longer know the category — the 2023 data
// silently produced https://www.olx.ua/uk/kharkov/ for daily rentals.
// Run after refreshing the YAML: OLXJEV_LIVE_URLS=1 go test -run TestLiveLeafURLs -v ./olxjev/
func TestLiveLeafURLsKeepCategory(t *testing.T) {
    if os.Getenv("OLXJEV_LIVE_URLS") != "1" {
        t.Skip("set OLXJEV_LIVE_URLS=1 to build every leaf's URL against OLX")
    }
    catalog, err := NewCatalog(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewCatalog: %v", err)
    }
    rq := request.NewLocalRequest()
    for _, leaf := range catalog.Leaves {
        values := leaf.Values()
        values["region"], values["city"] = "8", "280" // Харків
        u, err := olxurl.Build(context.Background(), values, rq)
        if err != nil {
            t.Errorf("%s (%s): %v", leaf.Key, leaf.Label, err)
            continue
        }
        if strings.TrimSuffix(u, "/") == "https://www.olx.ua/uk/kharkov" {
            t.Errorf("%s (%s): OLX dropped the category, URL %s", leaf.Key, leaf.Label, u)
        }
    }
}
