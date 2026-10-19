package main

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "net/http"
    "net/http/httptest"
    "net/url"
    "strings"
    "testing"

    "github.com/mixadior/olxjev"
)

// failingBreadcrumbs is OLX's breadcrumbs API failing.
type failingBreadcrumbs struct{}

func (failingBreadcrumbs) Q(_ context.Context, _ string, _ ...string) (interface{}, string, []error) {
    return nil, "", []error{errors.New("olx is down")}
}

// The API's status says whether the query was answered: a decision or OLX
// failure is a 502, a «clarify» outcome is the answer (200).
func TestWebResolveJSONStatus(t *testing.T) {
    decisionDown, _, _ := newCountingWeb(t, func(w http.ResponseWriter, r *http.Request) {
        http.Error(w, "boom", http.StatusBadRequest)
    }, 10)
    olxDown := newTestWeb(t, "category_1760", failingBreadcrumbs{})
    clarify := newTestWeb(t, olxjev.NoneOption, fakeBreadcrumbs{})
    for _, tc := range []struct {
        name string
        h    http.Handler
        want int
    }{
        {"decision failure", decisionDown, http.StatusBadGateway},
        {"OLX failure", olxDown, http.StatusBadGateway},
        {"category unclear", clarify, http.StatusOK},
    } {
        w := postResolve(tc.h, `{"q":"`+kharkiv+`"}`)
        var out output
        if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
            t.Fatalf("%s: unmarshal %q: %v", tc.name, w.Body.String(), err)
        }
        if w.Code != tc.want || out.Error == "" {
            t.Errorf("%s: %d %q, want %d with the error", tc.name, w.Code, out.Error, tc.want)
        }
    }
}

// A query over maxQueryRunes is a 400 on every endpoint, before any model
// call: it would spend tokens and fill the cache.
func TestWebRejectsLongQuery(t *testing.T) {
    h, calls, _ := newCountingWeb(t, fakeJevHandler(t, "category_1760"), 10)
    long := strings.Repeat("а", maxQueryRunes+1)
    for name, w := range map[string]*httptest.ResponseRecorder{
        "page":   get(t, h, "/?q="+url.QueryEscape(long)),
        "result": get(t, h, "/result?q="+url.QueryEscape(long)),
        "api":    postResolve(h, `{"q":"`+long+`"}`),
    } {
        if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "longer than 500 characters") {
            t.Errorf("%s: %d, want 400 saying the query is too long:\n%.300s", name, w.Code, w.Body.String())
        }
    }
    if calls.Load() != 0 {
        t.Errorf("made %d model calls for over-long queries, want 0", calls.Load())
    }
    if w := get(t, h, "/result?q="+url.QueryEscape(strings.Repeat("а", maxQueryRunes))); w.Code != http.StatusOK {
        t.Errorf("a query of exactly %d characters = %d, want 200", maxQueryRunes, w.Code)
    }
}

// -web-debug caches each answer's model exchanges (~30 KB, not ~2 KB), so
// unless -cache-size is given it keeps debugCacheSize entries and says so.
func TestRunWebDebugLimitsCache(t *testing.T) {
    srv := fakeJevServer(t)
    defer srv.Close()
    for _, tc := range []struct {
        args    []string
        limited bool
    }{
        {[]string{"-web-debug"}, true},
        {[]string{"-web-debug", "-cache-size", "50"}, false},
        {nil, false},
    } {
        var stderr bytes.Buffer
        run(append(tc.args, "-endpoint", srv.URL, "-serve", "127.0.0.1:-1"), nil, io.Discard, &stderr, env(nil))
        if got := strings.Contains(stderr.String(), "cache limited to 2000 entries"); got != tc.limited {
            t.Errorf("%v: cache limited = %v, want %v:\n%s", tc.args, got, tc.limited, stderr.String())
        }
    }
}
