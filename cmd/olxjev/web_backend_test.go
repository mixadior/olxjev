package main

import (
    "bytes"
    "io"
    "net/http"
    "net/http/httptest"
    "net/url"
    "strings"
    "sync/atomic"
    "testing"

    "github.com/rs/zerolog"

    "github.com/mixadior/olxjev"
    "github.com/mixadior/olxjev/decision/subq"
    "github.com/mixadior/olxjev/decision/typesafe"
)

// newBackendsWeb serves the UI over a resolver per named backend, each on
// its own fake server, counting each backend's calls.
func newBackendsWeb(t *testing.T, opts webOptions, names ...string) (http.Handler, map[string]*atomic.Int64, *bytes.Buffer) {
    t.Helper()
    calls := map[string]*atomic.Int64{}
    var resolvers []*olxjev.Resolver
    for _, name := range names {
        n := &atomic.Int64{}
        calls[name] = n
        answer := fakeJevHandler(t, "category_1760")
        srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            n.Add(1)
            answer(w, r)
        }))
        t.Cleanup(srv.Close)
        resolver, err := newResolver(backendOptions{name: name, endpoint: srv.URL}, 0)
        if err != nil {
            t.Fatalf("newResolver(%s): %v", name, err)
        }
        resolvers = append(resolvers, resolver)
    }
    logs := &bytes.Buffer{}
    h, err := newWeb(resolvers, fakeBreadcrumbs{}, zerolog.New(logs), opts)
    if err != nil {
        t.Fatalf("newWeb: %v", err)
    }
    return h, calls, logs
}

func postResolve(h http.Handler, body string) *httptest.ResponseRecorder {
    w := httptest.NewRecorder()
    h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/resolve", strings.NewReader(body)))
    return w
}

const kharkiv = "зняти квартиру в Харкові"

// ?backend= (and the API's "backend") picks the resolver on every endpoint.
func TestWebBackendParamPicksResolver(t *testing.T) {
    h, calls, _ := newBackendsWeb(t, webOptions{}, typesafe.Name, subq.Name)
    steps := []struct {
        name, backend string
        do            func() *httptest.ResponseRecorder
    }{
        {"result", subq.Name, func() *httptest.ResponseRecorder { return get(t, h, "/result?backend=subq&q="+url.QueryEscape(kharkiv)) }},
        {"page", typesafe.Name, func() *httptest.ResponseRecorder { return get(t, h, "/?backend=typesafe&q="+url.QueryEscape(kharkiv)) }},
        {"api", subq.Name, func() *httptest.ResponseRecorder { return postResolve(h, `{"q":"`+kharkiv+`","backend":"subq"}`) }},
    }
    for _, s := range steps {
        before := map[string]int64{typesafe.Name: calls[typesafe.Name].Load(), subq.Name: calls[subq.Name].Load()}
        if w := s.do(); w.Code != http.StatusOK {
            t.Fatalf("%s: status %d: %s", s.name, w.Code, w.Body.String())
        }
        for name, n := range calls {
            moved := n.Load() > before[name]
            if moved != (name == s.backend) {
                t.Errorf("%s with backend=%s: %s calls %d → %d", s.name, s.backend, name, before[name], n.Load())
            }
        }
    }
}

// Without a backend the default answers: the first resolver, or the one
// webOptions.backend names.
func TestWebBackendDefault(t *testing.T) {
    for _, tc := range []struct{ opt, want string }{{"", typesafe.Name}, {subq.Name, subq.Name}} {
        h, calls, _ := newBackendsWeb(t, webOptions{backend: tc.opt}, typesafe.Name, subq.Name)
        get(t, h, "/result?q="+url.QueryEscape(kharkiv))
        postResolve(h, `{"q":"зняти квартиру у Львові"}`)
        for name, n := range calls {
            if (n.Load() > 0) != (name == tc.want) {
                t.Errorf("default %q: %s made %d calls", tc.opt, name, n.Load())
            }
        }
    }
}

// An unknown backend, or one without a key, is a 400 that says why — never
// a silent fallback to another model.
func TestWebBackendRejects(t *testing.T) {
    h, calls, _ := newBackendsWeb(t, webOptions{}, typesafe.Name)
    for backend, want := range map[string]string{
        "subq": "backend subq is not available: SUBQ_API_KEY is not set",
        "nope": `unknown backend "nope" (typesafe or subq)`,
    } {
        for name, w := range map[string]*httptest.ResponseRecorder{
            "page":   get(t, h, "/?backend="+backend+"&q="+url.QueryEscape(kharkiv)),
            "result": get(t, h, "/result?backend="+backend+"&q="+url.QueryEscape(kharkiv)),
            "api":    postResolve(h, `{"q":"`+kharkiv+`","backend":"`+backend+`"}`),
        } {
            body := strings.NewReplacer("&#34;", `"`, `\"`, `"`).Replace(w.Body.String()) // HTML and JSON escapes
            if w.Code != http.StatusBadRequest || !strings.Contains(body, want) {
                t.Errorf("%s backend=%s = %d, want 400 with %q:\n%s", name, backend, w.Code, want, body)
            }
        }
    }
    if n := calls[typesafe.Name].Load(); n != 0 {
        t.Errorf("rejected requests made %d calls", n)
    }
}

// The two models answer differently, so each backend has its own cache entry.
func TestWebCacheKeyedByBackend(t *testing.T) {
    h, calls, _ := newBackendsWeb(t, webOptions{cacheSize: 10}, typesafe.Name, subq.Name)
    get(t, h, "/result?backend=typesafe&q="+url.QueryEscape(kharkiv))
    w := get(t, h, "/result?backend=subq&q="+url.QueryEscape(kharkiv))
    if calls[subq.Name].Load() == 0 || strings.Contains(w.Body.String(), "з кешу") {
        t.Fatalf("subq was served typesafe's cached answer")
    }
    subqCalls := calls[subq.Name].Load()
    w = get(t, h, "/result?backend=subq&q="+url.QueryEscape("  Зняти квартиру в Харкові "))
    if calls[subq.Name].Load() != subqCalls || !strings.Contains(w.Body.String(), "з кешу") {
        t.Errorf("a repeated subq query was not served from the cache")
    }
}

// The page offers a switch when two or more backends are available; its
// radios belong to the GET form, so it works without the script.
func TestWebBackendSwitch(t *testing.T) {
    h, _, _ := newBackendsWeb(t, webOptions{}, typesafe.Name, subq.Name)
    body := get(t, h, "/?backend=subq&q="+url.QueryEscape("купити оселю")).Body.String()
    for _, want := range []string{
        `<form id="ask"`,
        `form="ask" name="backend" value="typesafe">`,
        `form="ask" name="backend" value="subq" checked>`,
        `data-backend="subq"`, // the card, for the script's history
        `backend=subq`,        // in the category suggestions' links
    } {
        if !strings.Contains(body, want) {
            t.Errorf("page lacks %q", want)
        }
    }

    one := get(t, newTestWeb(t, "category_1760", nil), "/").Body.String()
    if strings.Contains(one, `name="backend"`) {
        t.Error("with one backend the page must not show a switch")
    }
}

// The resolve event names the backend that answered.
func TestWebLogNamesBackend(t *testing.T) {
    h, _, logs := newBackendsWeb(t, webOptions{}, typesafe.Name, subq.Name)
    get(t, h, "/result?backend=subq&q="+url.QueryEscape(kharkiv))
    events := resolveEvents(t, logs)
    if len(events) != 1 || events[0]["backend"] != subq.Name || events[0]["query"] != kharkiv {
        t.Errorf("resolve events = %v, want one by subq for %q", events, kharkiv)
    }
}

// The debug panel and its badge follow the chosen backend.
func TestWebDebugFollowsBackend(t *testing.T) {
    h, _, _ := newBackendsWeb(t, webOptions{debug: true}, typesafe.Name, subq.Name)
    body := get(t, h, "/?backend=subq&q="+url.QueryEscape(kharkiv)).Body.String()
    for _, want := range []string{"debug · subq", subq.DefaultModel} {
        if !strings.Contains(body, want) {
            t.Errorf("debug page lacks %q", want)
        }
    }
}

// -serve offers every backend whose key is set, besides the default.
func TestRunServeListsBackends(t *testing.T) {
    srv := fakeJevServer(t)
    defer srv.Close()
    for _, tc := range []struct {
        args []string
        env  map[string]string
        want string
    }{
        {[]string{"-endpoint", srv.URL}, nil, `"backends":"typesafe (default)"`},
        {[]string{"-endpoint", srv.URL}, map[string]string{"SUBQ_API_KEY": "k"}, `"backends":"typesafe (default), subq"`},
        {[]string{"-backend", "subq", "-endpoint", srv.URL}, map[string]string{"TYPESAFE_API_KEY": "k"}, `"backends":"typesafe, subq (default)"`},
    } {
        var stderr bytes.Buffer
        run(append(tc.args, "-serve", "127.0.0.1:-1"), nil, io.Discard, &stderr, func(k string) string { return tc.env[k] })
        if !strings.Contains(stderr.String(), tc.want) {
            t.Errorf("%v %v: stderr lacks %q:\n%s", tc.args, tc.env, tc.want, stderr.String())
        }
    }
}
