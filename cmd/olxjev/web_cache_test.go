package main

import (
    "bytes"
    "net/http"
    "net/http/httptest"
    "net/url"
    "strings"
    "sync/atomic"
    "testing"

    "github.com/mixadior/olxjev"
    "github.com/mixadior/olxjev/decision/typesafe"
)

// newCountingWeb serves the UI over jev, counting the Jev calls.
func newCountingWeb(t *testing.T, jev http.HandlerFunc, cacheSize int) (http.Handler, *atomic.Int64, *bytes.Buffer) {
    t.Helper()
    calls := &atomic.Int64{}
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        calls.Add(1)
        jev(w, r)
    }))
    t.Cleanup(srv.Close)
    resolver, err := newResolver(backendOptions{name: typesafe.Name, endpoint: srv.URL}, 0)
    if err != nil {
        t.Fatalf("newResolver: %v", err)
    }
    logs := &bytes.Buffer{}
    h, err := newWeb([]*olxjev.Resolver{resolver}, fakeBreadcrumbs{}, logs, webOptions{cacheSize: cacheSize})
    if err != nil {
        t.Fatalf("newWeb: %v", err)
    }
    return h, calls, logs
}

func TestWebCachesSimilarQueries(t *testing.T) {
    h, calls, logs := newCountingWeb(t, fakeJevHandler(t, "category_1760"), 10)
    get(t, h, "/result?q="+url.QueryEscape("зняти квартиру в Харкові"))
    if calls.Load() != 2 {
        t.Fatalf("first resolve made %d Jev calls, want 2", calls.Load())
    }
    w := get(t, h, "/result?q="+url.QueryEscape("  Зняти   квартиру в Харкові! "))
    page := get(t, h, "/?q="+url.QueryEscape("зняти квартиру в харкові"))
    api := httptest.NewRecorder()
    h.ServeHTTP(api, httptest.NewRequest(http.MethodPost, "/api/resolve", strings.NewReader(`{"q":"Зняти квартиру в Харкові"}`)))
    if calls.Load() != 2 {
        t.Errorf("similar queries made %d Jev calls in total, want 2 (cached)", calls.Load())
    }
    for name, body := range map[string]string{"result": w.Body.String(), "page": page.Body.String(), "api": api.Body.String()} {
        if !strings.Contains(body, "dolgosrochnaya-arenda-kvartir/kharkov") {
            t.Errorf("%s: cached answer lacks the URL:\n%s", name, body)
        }
    }
    if !strings.Contains(w.Body.String(), "з кешу") {
        t.Error("a cached card must say it came from the cache, not show a cost")
    }
    if n := strings.Count(logs.String(), "cache hit"); n != 3 {
        t.Errorf("log has %d cache hits, want 3:\n%s", n, logs.String())
    }
}

// «Category unclear» is the answer to the query, so it is cached too.
func TestWebCachesUnresolvedCategory(t *testing.T) {
    h, calls, _ := newCountingWeb(t, fakeJevHandler(t, olxjev.NoneOption), 10)
    get(t, h, "/result?q="+url.QueryEscape("щось незрозуміле"))
    get(t, h, "/result?q="+url.QueryEscape("щось незрозуміле"))
    if calls.Load() != 1 {
        t.Errorf("made %d Jev calls, want 1 (the unresolved answer cached)", calls.Load())
    }
}

// A Jev failure is not the query's answer: the next request tries again.
func TestWebDoesNotCacheFailures(t *testing.T) {
    h, calls, _ := newCountingWeb(t, func(w http.ResponseWriter, r *http.Request) {
        http.Error(w, "boom", http.StatusBadRequest)
    }, 10)
    get(t, h, "/result?q="+url.QueryEscape("зняти квартиру в Харкові"))
    first := calls.Load()
    get(t, h, "/result?q="+url.QueryEscape("зняти квартиру в Харкові"))
    if first == 0 || calls.Load() == first {
        t.Errorf("Jev calls %d then %d: a failure must not be cached", first, calls.Load())
    }
}

func TestWebCacheSizeZeroDisables(t *testing.T) {
    h, calls, _ := newCountingWeb(t, fakeJevHandler(t, "category_1760"), 0)
    get(t, h, "/result?q="+url.QueryEscape("зняти квартиру в Харкові"))
    get(t, h, "/result?q="+url.QueryEscape("зняти квартиру в Харкові"))
    if calls.Load() != 4 {
        t.Errorf("made %d Jev calls, want 4 with the cache off", calls.Load())
    }
}
