package main

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "net/url"
    "regexp"
    "strings"
    "testing"
    "time"

    "github.com/rs/zerolog"

    "github.com/mixadior/olxjev"
    "github.com/mixadior/olxjev/decision/typesafe"
)

func TestPageTokens(t *testing.T) {
    now := time.Unix(1_800_000_000, 0)
    p := &pageTokens{secret: []byte("0123456789abcdef"), now: func() time.Time { return now }}
    tok := p.issue()
    if !p.valid(tok) {
        t.Fatalf("a fresh token %q is not valid", tok)
    }
    exp, sig, _ := strings.Cut(tok, ".")
    other := &pageTokens{secret: []byte("fedcba9876543210"), now: p.now}
    for name, bad := range map[string]string{
        "empty":           "",
        "no signature":    exp,
        "garbage":         "x.y",
        "tampered expiry": "9" + exp + "." + sig,
        "other secret":    other.issue(),
    } {
        if p.valid(bad) {
            t.Errorf("%s token %q is valid", name, bad)
        }
    }
    now = now.Add(tokenTTL + time.Second)
    if p.valid(tok) {
        t.Error("the token is valid after its TTL")
    }
}

func TestTokenSecret(t *testing.T) {
    if _, err := tokenSecret("short", zerolog.Nop()); err == nil {
        t.Error("a short OLXJEV_SECRET is accepted")
    }
    if key, err := tokenSecret("0123456789abcdef", zerolog.Nop()); err != nil || string(key) != "0123456789abcdef" {
        t.Errorf("tokenSecret = %q, %v; want the env value", key, err)
    }
    a, _ := tokenSecret("", zerolog.Nop())
    b, _ := tokenSecret("", zerolog.Nop())
    if len(a) != 32 || string(a) == string(b) {
        t.Errorf("unset OLXJEV_SECRET: keys %x, %x; want two random 32-byte keys", a, b)
    }
}

// newTokenWeb is the web UI as -serve runs it: /result and /api/resolve
// answer the page's script only.
func newTokenWeb(t *testing.T) http.Handler {
    t.Helper()
    srv := fakeJevServerCategory(t, "category_1760")
    t.Cleanup(srv.Close)
    resolver, err := newResolver(backendOptions{name: typesafe.Name, endpoint: srv.URL}, 0)
    if err != nil {
        t.Fatalf("newResolver: %v", err)
    }
    h, err := newWeb([]*olxjev.Resolver{resolver}, fakeBreadcrumbs{}, zerolog.Nop(), webOptions{secret: []byte("0123456789abcdef")})
    if err != nil {
        t.Fatalf("newWeb: %v", err)
    }
    return h
}

var tokenMeta = regexp.MustCompile(`<meta name="olxjev-token" content="([^"]+)">`)

// pageToken loads the page and returns the token it carries.
func pageToken(t *testing.T, h http.Handler) string {
    t.Helper()
    m := tokenMeta.FindStringSubmatch(get(t, h, "/").Body.String())
    if m == nil {
        t.Fatal("the page carries no token")
    }
    return m[1]
}

func callResult(h http.Handler, headers map[string]string) *httptest.ResponseRecorder {
    r := httptest.NewRequest(http.MethodGet, "/result?q="+url.QueryEscape("зняти квартиру в Харкові"), nil)
    for k, v := range headers {
        r.Header.Set(k, v)
    }
    w := httptest.NewRecorder()
    h.ServeHTTP(w, r)
    return w
}

func callAPI(h http.Handler, headers map[string]string) *httptest.ResponseRecorder {
    r := httptest.NewRequest(http.MethodPost, "/api/resolve", strings.NewReader(`{"q":"зняти квартиру в Харкові"}`))
    for k, v := range headers {
        r.Header.Set(k, v)
    }
    w := httptest.NewRecorder()
    h.ServeHTTP(w, r)
    return w
}

// The page's token opens /result and /api/resolve; without it, with a forged
// one or from another site they are a 403.
func TestWebEndpointsAnswerOnlyThePage(t *testing.T) {
    h := newTokenWeb(t)
    tok := pageToken(t, h)
    for name, tc := range map[string]struct {
        headers map[string]string
        want    int
    }{
        "page token":      {map[string]string{tokenHeader: tok, "Sec-Fetch-Site": "same-origin"}, http.StatusOK},
        "token, no Sec-*": {map[string]string{tokenHeader: tok}, http.StatusOK},
        "no token":        {nil, http.StatusForbidden},
        "forged token":    {map[string]string{tokenHeader: "9999999999.forged"}, http.StatusForbidden},
        "cross-site":      {map[string]string{tokenHeader: tok, "Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
    } {
        if w := callResult(h, tc.headers); w.Code != tc.want {
            t.Errorf("/result, %s: %d, want %d: %s", name, w.Code, tc.want, w.Body.String())
        }
        if w := callAPI(h, tc.headers); w.Code != tc.want {
            t.Errorf("/api/resolve, %s: %d, want %d: %s", name, w.Code, tc.want, w.Body.String())
        }
    }

    w := callAPI(h, nil)
    var out output
    if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !strings.Contains(out.Error, "page token") {
        t.Errorf("the API's 403 = %q (%v), want JSON naming the page token", w.Body.String(), err)
    }
}

// The page itself stays open: ?q= links are shareable and work without JS.
func TestWebPageNeedsNoToken(t *testing.T) {
    w := get(t, newTokenWeb(t), "/?q="+url.QueryEscape("зняти квартиру в Харкові"))
    if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "olx.ua") {
        t.Errorf("GET /?q= = %d, want 200 with the result", w.Code)
    }
}

// The script sends the page's token and reloads the page on a 403.
func TestWebScriptSendsToken(t *testing.T) {
    js := get(t, newTokenWeb(t), "/assets/app.js").Body.String()
    for _, want := range []string{`meta[name="olxjev-token"]`, `"X-OlxJev-Token"`, "r.status === 403"} {
        if !strings.Contains(js, want) {
            t.Errorf("app.js lacks %s", want)
        }
    }
}
