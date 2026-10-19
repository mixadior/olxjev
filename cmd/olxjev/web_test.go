package main

import (
    "bytes"
    "context"
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "net/url"
    "strings"
    "sync"
    "testing"

    "github.com/mixadior/olxjev"
    "github.com/mixadior/olxjev/decision/typesafe"
    "github.com/mixadior/olxjev/request"
)

// fakeBreadcrumbs stands in for OLX's breadcrumbs API behind olxurl.Build.
type fakeBreadcrumbs struct{}

func (fakeBreadcrumbs) Q(_ context.Context, _ string, _ ...string) (interface{}, string, []error) {
    return nil, `{"data":{"breadcrumbs":[{"href":"/uk/nedvizhimost/kvartiry/dolgosrochnaya-arenda-kvartir/kharkov/"}]}}`, nil
}

func newTestWeb(t *testing.T, category string, rq request.Request) http.Handler {
    t.Helper()
    srv := fakeJevServerCategory(t, category)
    t.Cleanup(srv.Close)
    resolver, err := newResolver(backendOptions{name: typesafe.Name, endpoint: srv.URL}, 0)
    if err != nil {
        t.Fatalf("newResolver: %v", err)
    }
    h, err := newWeb([]*olxjev.Resolver{resolver}, rq, io.Discard, webOptions{})
    if err != nil {
        t.Fatalf("newWeb: %v", err)
    }
    return h
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
    t.Helper()
    w := httptest.NewRecorder()
    h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
    return w
}

func TestWebIndexServesPage(t *testing.T) {
    w := get(t, newTestWeb(t, "category_1760", nil), "/")
    if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
        t.Fatalf("GET / = %d %q", w.Code, w.Header().Get("Content-Type"))
    }
    body := w.Body.String()
    for _, want := range []string{`<form`, `name="q"`, `/assets/app.css`, `/assets/app.js`, `хочу зняти однушку`} {
        if !strings.Contains(body, want) {
            t.Errorf("page lacks %q", want)
        }
    }
}

func TestWebAssetsEmbedded(t *testing.T) {
    h := newTestWeb(t, "category_1760", nil)
    for path, ctype := range map[string]string{"/assets/app.css": "text/css", "/assets/app.js": "javascript"} {
        w := get(t, h, path)
        if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), ctype) {
            t.Errorf("GET %s = %d %q, want 200 %s", path, w.Code, w.Header().Get("Content-Type"), ctype)
        }
    }
    if w := get(t, h, "/nope"); w.Code != http.StatusNotFound {
        t.Errorf("GET /nope = %d, want 404", w.Code)
    }
}

// A query in the address renders the result server-side: links are shareable
// and the page works without JavaScript.
func TestWebIndexWithQueryRendersResult(t *testing.T) {
    w := get(t, newTestWeb(t, "category_1760", fakeBreadcrumbs{}), "/?q="+url.QueryEscape("зняти квартиру в Харкові"))
    if w.Code != http.StatusOK {
        t.Fatalf("GET /?q= = %d", w.Code)
    }
    body := w.Body.String()
    for _, want := range []string{
        "https://www.olx.ua/uk/nedvizhimost/kvartiry/dolgosrochnaya-arenda-kvartir/kharkov/",
        "Довгострокова оренда квартир",
        "Харків, Харківська область",
        ">зняти квартиру в Харкові</textarea>",
    } {
        if !strings.Contains(body, want) {
            t.Errorf("page lacks %q", want)
        }
    }
}

func TestWebResultFragment(t *testing.T) {
    w := get(t, newTestWeb(t, "category_1760", fakeBreadcrumbs{}), "/result?q="+url.QueryEscape("зняти квартиру в Харкові"))
    if w.Code != http.StatusOK {
        t.Fatalf("GET /result = %d", w.Code)
    }
    body := w.Body.String()
    if strings.Contains(body, "<html") {
        t.Error("fragment must not be a full page")
    }
    if !strings.Contains(body, "https://www.olx.ua/uk/nedvizhimost/kvartiry/dolgosrochnaya-arenda-kvartir/kharkov/") {
        t.Errorf("fragment lacks the URL:\n%s", body)
    }
}

func TestWebResultFragmentEmptyQuery(t *testing.T) {
    if w := get(t, newTestWeb(t, "category_1760", nil), "/result?q=+"); w.Code != http.StatusBadRequest {
        t.Errorf("GET /result?q= = %d, want 400", w.Code)
    }
}

func TestWebUnresolvedCategoryExplains(t *testing.T) {
    w := get(t, newTestWeb(t, olxjev.NoneOption, fakeBreadcrumbs{}), "/result?q="+url.QueryEscape("щось незрозуміле"))
    body := w.Body.String()
    if w.Code != http.StatusOK || !strings.Contains(body, "Не вдалося визначити категорію") {
        t.Errorf("GET /result = %d, want a hint about the category:\n%s", w.Code, body)
    }
    if strings.Contains(body, "https://www.olx.ua") {
        t.Error("no URL may be shown without a category")
    }
}

// A place no city matches: no URL, a request to add the region, and the
// shortlisted places as one-click refinements of the query.
func TestWebLocationUnresolvedSuggestsPlaces(t *testing.T) {
    const q = "зняти квартиру в Іванівці"
    w := get(t, newTestWeb(t, "category_1760", fakeBreadcrumbs{}), "/result?q="+url.QueryEscape(q))
    body := w.Body.String()
    if w.Code != http.StatusOK || !strings.Contains(body, "Додайте область") {
        t.Fatalf("GET /result = %d, want a request to add the region:\n%s", w.Code, body)
    }
    if strings.Contains(body, "https://www.olx.ua") {
        t.Error("no URL may be shown for an unresolved place")
    }
    if n := strings.Count(body, `class="suggest" data-q="`+q+`, Іванів`); n < 2 || n > maxSuggestions {
        t.Errorf("want 2..%d suggestions refining the query with «Іванів…, <область>», got %d:\n%s", maxSuggestions, n, body)
    }
}

// An unresolved category offers the leading ones; picking one refines the
// query with that category's name.
func TestWebCategoryCandidatesSuggested(t *testing.T) {
    const q = "купити оселю в Києві"
    w := get(t, newTestWeb(t, "category_1760", fakeBreadcrumbs{}), "/result?q="+url.QueryEscape(q))
    body := w.Body.String()
    if w.Code != http.StatusOK || !strings.Contains(body, "Уточніть, що саме шукаєте") {
        t.Fatalf("GET /result = %d, want a request to pick the category:\n%s", w.Code, body)
    }
    for _, want := range []string{
        `class="suggest" data-q="` + q + `, продаж квартир"`,
        `class="suggest" data-q="` + q + `, продаж будинків"`,
        `>Квартири › Продаж квартир</a>`,
    } {
        if !strings.Contains(body, want) {
            t.Errorf("fragment lacks %s:\n%s", want, body)
        }
    }
    if strings.Contains(body, "https://www.olx.ua") {
        t.Error("no URL may be shown without a category")
    }
}

// Every resolve logs its query and estimated cost; the card shows the cost.
func TestWebLogsCostPerResolve(t *testing.T) {
    srv := fakeJevServer(t)
    defer srv.Close()
    resolver, err := newResolver(backendOptions{name: typesafe.Name, endpoint: srv.URL}, 0)
    if err != nil {
        t.Fatalf("newResolver: %v", err)
    }
    var logs bytes.Buffer
    h, err := newWeb([]*olxjev.Resolver{resolver}, fakeBreadcrumbs{}, &logs, webOptions{})
    if err != nil {
        t.Fatalf("newWeb: %v", err)
    }
    w := get(t, h, "/result?q="+url.QueryEscape("зняти квартиру в Харкові"))
    if !strings.Contains(w.Body.String(), "≈$0.000126") {
        t.Errorf("card lacks the cost:\n%s", w.Body.String())
    }
    w = httptest.NewRecorder()
    h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/resolve", strings.NewReader(`{"q":"зняти квартиру у Львові"}`)))
    log := logs.String()
    for _, want := range []string{
        "resolve [typesafe] «зняти квартиру в Харкові»: ≈$0.000126 (fake, 2 calls, 3000 input tokens)",
        "resolve [typesafe] «зняти квартиру у Львові»: ",
    } {
        if !strings.Contains(log, want) {
            t.Errorf("log lacks %q:\n%s", want, log)
        }
    }
}

func TestWebEscapesQuery(t *testing.T) {
    w := get(t, newTestWeb(t, olxjev.NoneOption, nil), "/?q="+url.QueryEscape("<script>alert(1)</script>"))
    if strings.Contains(w.Body.String(), "<script>alert(1)") {
        t.Error("query must be HTML-escaped")
    }
}

func TestWebResolveJSON(t *testing.T) {
    h := newTestWeb(t, "category_1760", fakeBreadcrumbs{})
    w := httptest.NewRecorder()
    h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/resolve", strings.NewReader(`{"q":"зняти квартиру в Харкові"}`)))
    if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
        t.Fatalf("POST /api/resolve = %d %q: %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
    }
    var out output
    if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if out.Result == nil || out.Result.Values["city"] != "280" || len(out.URLs) != 1 {
        t.Errorf("out = %+v", out)
    }
}

func TestWebResolveJSONRejects(t *testing.T) {
    h := newTestWeb(t, "category_1760", nil)
    for _, body := range []string{`{"q":"  "}`, `not json`} {
        w := httptest.NewRecorder()
        h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/resolve", strings.NewReader(body)))
        if w.Code != http.StatusBadRequest {
            t.Errorf("POST %s = %d, want 400", body, w.Code)
        }
    }
    if w := get(t, h, "/api/resolve"); w.Code != http.StatusMethodNotAllowed {
        t.Errorf("GET /api/resolve = %d, want 405", w.Code)
    }
}

// The server shares one resolver and composer between requests; run with -race.
func TestWebConcurrentRequests(t *testing.T) {
    h := newTestWeb(t, "category_1760", fakeBreadcrumbs{})
    var wg sync.WaitGroup
    for i := 0; i < 4; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            w := httptest.NewRecorder()
            h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/result?q="+url.QueryEscape("зняти квартиру в Харкові"), nil))
            if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "kharkov") {
                t.Errorf("concurrent GET /result = %d", w.Code)
            }
        }()
    }
    wg.Wait()
}

func TestRunServeBadAddress(t *testing.T) {
    srv := fakeJevServer(t)
    defer srv.Close()
    var stdout, stderr bytes.Buffer
    code := run([]string{"-endpoint", srv.URL, "-serve", "127.0.0.1:-1"}, nil, &stdout, &stderr, func(string) string { return "" })
    if code != 1 || stderr.Len() == 0 {
        t.Errorf("exit %d, stderr %q; want 1 and the listen error", code, stderr.String())
    }
}

// The card's cost names the model.
func TestWebCostShowsModel(t *testing.T) {
    w := get(t, newTestWeb(t, "category_1760", fakeBreadcrumbs{}), "/result?q="+url.QueryEscape("зняти квартиру в Харкові"))
    if body := w.Body.String(); !strings.Contains(body, "≈$0.000126 · fake") {
        t.Errorf("card cost lacks the model:\n%s", body)
    }
}
