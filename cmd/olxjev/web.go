package main

import (
    "context"
    "embed"
    "encoding/json"
    "errors"
    "fmt"
    "html/template"
    "io"
    "log"
    "net/http"
    "strings"
    "time"
    "unicode"
    "unicode/utf8"

    botform "github.com/mixadior/bfc"

    "github.com/mixadior/olxjev"
    "github.com/mixadior/olxjev/decision"
    "github.com/mixadior/olxjev/decision/typesafe"
    "github.com/mixadior/olxjev/request"
    "github.com/mixadior/olxjev/olxform"
)

//go:embed templates/*
var templatesFs embed.FS

//go:embed assets/*
var assetsFs embed.FS

// resolveTimeout bounds one web request: the decision calls with retries plus the
// OLX breadcrumbs call.
const resolveTimeout = 60 * time.Second

// maxQueryRunes caps a query: longer ones would spend tokens on every stage
// and fill the cache (golden's longest is 88).
const maxQueryRunes = 500

var examples = []string{
    "хочу зняти однушку квартиру в Харкові до 10к гривень в центрі",
    "купити трикімнатну квартиру в Дніпрі від 60 до 80 тис доларів",
    "снять двушку в Киеве на Оболони до 20 тыс",
    "зняти будинок подобово в Яремче",
    "оренда офісу в Харкові площею до 30 м2",
}

// web serves the OlxJev UI: GET / (a ?q= query is resolved server-side),
// GET /result (the result card alone, for the page's script),
// POST /api/resolve (the -json output) and the embedded assets. Each takes
// a backend (?backend=, the API's "backend"); without one the default answers.
type web struct {
    log       *log.Logger                 // one line per resolve: backend, query, decision usage, estimated cost
    resolvers map[string]*olxjev.Resolver // by Decider.Name()
    names     []string                    // the backends in the switch's order
    backend   string                      // the default
    rq        request.Request
    tpl       *template.Template
    cache     *lru[string, output] // by backend and normalizeQuery; answers only, never failures
    debug     bool                 // -web-debug: show the model's questions and answers under each result
}

// webOptions configure the web UI.
type webOptions struct {
    cacheSize int    // how many answered queries the LRU keeps; 0 disables it
    debug     bool   // show the debug panel: every filter, the model's questions and answers
    backend   string // the default backend; "" = the first resolver's
    via       string // -via: where the typesafe backend is served, for the startup line
}

// newWeb builds the handler over one resolver per backend, in the order the
// page's switch lists them.
func newWeb(resolvers []*olxjev.Resolver, rq request.Request, logw io.Writer, opts webOptions) (http.Handler, error) {
    if len(resolvers) == 0 {
        return nil, errors.New("no decision backend")
    }
    tpl, err := template.New("").Funcs(template.FuncMap{
        "pct": func(f float64) int { return int(f*100 + 0.5) },
    }).ParseFS(templatesFs, "templates/*.html")
    if err != nil {
        return nil, err
    }
    s := &web{resolvers: map[string]*olxjev.Resolver{}, backend: opts.backend, rq: rq, tpl: tpl,
        log: log.New(logw, "", log.LstdFlags), cache: newLRU[string, output](opts.cacheSize), debug: opts.debug}
    for _, r := range resolvers {
        name := r.Decider.Name()
        s.resolvers[name] = r
        s.names = append(s.names, name)
    }
    if s.backend == "" {
        s.backend = s.names[0]
    }
    if _, ok := s.resolvers[s.backend]; !ok {
        return nil, fmt.Errorf("default backend %q has no resolver", s.backend)
    }
    mux := http.NewServeMux()
    mux.HandleFunc("GET /{$}", s.index)
    mux.HandleFunc("GET /result", s.result)
    mux.HandleFunc("POST /api/resolve", s.resolveJSON)
    mux.Handle("GET /assets/", http.FileServerFS(assetsFs))
    return mux, nil
}

// serveWeb runs the web UI until the listener fails.
func serveWeb(addr string, resolvers []*olxjev.Resolver, rq request.Request, opts webOptions, stdout, stderr io.Writer) int {
    h, err := newWeb(resolvers, rq, stdout, opts)
    if err != nil {
        fmt.Fprintln(stderr, "olxjev:", err)
        return 1
    }
    srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
    var names []string
    for _, r := range resolvers {
        name := r.Decider.Name()
        label := name
        if name == typesafe.Name && opts.via == "opencode" {
            label += " via opencode"
        }
        if name == opts.backend {
            label += " (default)"
        }
        names = append(names, label)
    }
    fmt.Fprintf(stderr, "olxjev: web UI on http://%s/ (backends: %s)\n", addr, strings.Join(names, ", "))
    if err := srv.ListenAndServe(); err != nil {
        fmt.Fprintln(stderr, "olxjev:", err)
    }
    return 1
}

type pageView struct {
    Debug    bool
    Backend  string          // the decision backend answering, for the debug badge
    Backends []backendChoice // the switch; empty with a single backend
    Query    string
    Examples []string
    Result   *resultView
}

// backendChoice is one radio of the page's backend switch.
type backendChoice struct {
    Name    string
    Model   string
    Checked bool
}

type resultView struct {
    Query        string
    URL          string
    Alternatives []olxjev.CityURL
    Chips        []chip
    NotInURL     []string
    Hint         string // the request needs rewording
    Suggestions  []suggestion
    Error        string // something failed
    Elapsed      string
    Cost         string // estimated decision cost of this resolve, with the model
    Backend      string // the backend that answered, for the script and the suggestions' links
    Debug        *debugView
}

// suggestion refines the query with one shortlisted place.
type suggestion struct {
    Label string
    Query string
}

// maxSuggestions caps the places offered for an unresolved location.
const maxSuggestions = 6

type chip struct {
    Title       string
    Label       string
    Probability float64
}

// resolverFor is the resolver of the named backend; "" is the default. A
// backend without a resolver is an error, never a fallback to another model.
func (s *web) resolverFor(name string) (*olxjev.Resolver, error) {
    if name == "" {
        name = s.backend
    }
    if r, ok := s.resolvers[name]; ok {
        return r, nil
    }
    if b, ok := backends[name]; ok {
        return nil, fmt.Errorf("backend %s is not available: %s is not set", name, b.keyEnv)
    }
    return nil, fmt.Errorf("unknown backend %q (typesafe or subq)", name)
}

// choices is the page's switch with name checked; none for a single backend.
func (s *web) choices(name string) []backendChoice {
    if len(s.names) < 2 {
        return nil
    }
    out := make([]backendChoice, 0, len(s.names))
    for _, n := range s.names {
        out = append(out, backendChoice{Name: n, Model: s.resolvers[n].Decider.Model(), Checked: n == name})
    }
    return out
}

func (s *web) index(w http.ResponseWriter, r *http.Request) {
    q := r.URL.Query().Get("q")
    view := pageView{Debug: s.debug, Query: q, Examples: examples}
    status := http.StatusOK
    resolver, err := s.resolverFor(r.URL.Query().Get("backend"))
    if err == nil {
        err = checkQuery(q)
    }
    switch {
    case err != nil:
        // the switch falls back to the default; the card says why
        if resolver == nil {
            resolver = s.resolvers[s.backend]
        }
        view.Result = &resultView{Query: q, Error: err.Error()}
        status = http.StatusBadRequest
    case strings.TrimSpace(q) != "":
        rv := s.resolve(r.Context(), resolver, q)
        view.Result = &rv
    }
    view.Backend = resolver.Decider.Name()
    view.Backends = s.choices(view.Backend)
    s.render(w, status, "index", view)
}

func (s *web) result(w http.ResponseWriter, r *http.Request) {
    q := r.URL.Query().Get("q")
    if strings.TrimSpace(q) == "" {
        s.render(w, http.StatusBadRequest, "result", resultView{Hint: "Опишіть, що шукаєте."})
        return
    }
    resolver, err := s.resolverFor(r.URL.Query().Get("backend"))
    if err == nil {
        err = checkQuery(q)
    }
    if err != nil {
        s.render(w, http.StatusBadRequest, "result", resultView{Query: q, Error: err.Error()})
        return
    }
    s.render(w, http.StatusOK, "result", s.resolve(r.Context(), resolver, q))
}

func (s *web) resolveJSON(w http.ResponseWriter, r *http.Request) {
    var in struct {
        Q       string `json:"q"`
        Backend string `json:"backend"`
    }
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil || strings.TrimSpace(in.Q) == "" {
        w.WriteHeader(http.StatusBadRequest)
        json.NewEncoder(w).Encode(output{Query: in.Q, Error: "body must be {\"q\": \"<non-empty query>\", \"backend\": \"<optional backend>\"}"})
        return
    }
    resolver, err := s.resolverFor(in.Backend)
    if err == nil {
        err = checkQuery(in.Q)
    }
    if err != nil {
        w.WriteHeader(http.StatusBadRequest)
        json.NewEncoder(w).Encode(output{Query: in.Q, Error: err.Error()})
        return
    }
    out, _ := s.resolveCached(r.Context(), resolver, in.Q)
    if !out.answered() {
        w.WriteHeader(http.StatusBadGateway) // the decision model or OLX failed
    }
    json.NewEncoder(w).Encode(out)
}

// checkQuery rejects a query over maxQueryRunes.
func checkQuery(q string) error {
    if utf8.RuneCountInString(q) > maxQueryRunes {
        return fmt.Errorf("the query is longer than %d characters", maxQueryRunes)
    }
    return nil
}

// answered reports whether out is the query's answer: resolved, or a
// «clarify» outcome (category unclear, add the region). A decision or OLX
// failure is not.
func (o output) answered() bool {
    clarify := errors.Is(o.err, olxjev.ErrCategoryUnresolved) || errors.Is(o.err, olxjev.ErrLocationUnresolved)
    return o.Result != nil && o.urlErr == nil && (o.err == nil || clarify)
}

// resolveCached answers q from the cache or resolves it, caching answers
// (output.answered) — never a decision or OLX failure, which the next
// request retries. The backends answer differently, so each has its own
// entries.
func (s *web) resolveCached(ctx context.Context, resolver *olxjev.Resolver, q string) (output, bool) {
    start := time.Now()
    backend := resolver.Decider.Name()
    key := backend + "\x00" + normalizeQuery(q)
    if out, ok := s.cache.Get(key); ok {
        out.Query = q
        s.logResolve(backend, q, out, time.Since(start), true)
        return out, true
    }
    ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
    defer cancel()
    var exchanges []exchange
    if s.debug {
        ctx = olxjev.WithTrace(ctx, func(stage string, state any, questions map[string]decision.Question, d *decision.Decision) {
            exchanges = append(exchanges, exchange{stage: stage, state: state, questions: questions, d: d})
        })
    }
    out := resolveOne(ctx, resolver, q, s.rq)
    out.exchanges = exchanges
    s.logResolve(backend, q, out, time.Since(start), false)
    if out.answered() {
        s.cache.Add(key, out)
    }
    return out, false
}

func (s *web) resolve(ctx context.Context, resolver *olxjev.Resolver, q string) resultView {
    start := time.Now()
    out, cached := s.resolveCached(ctx, resolver, q)
    view := resultView{Query: q, Elapsed: fmt.Sprintf("%.1f с", time.Since(start).Seconds()), Backend: resolver.Decider.Name()}
    switch {
    case cached:
        view.Cost = "з кешу"
    case out.Result != nil && out.Result.Usage.Calls > 0:
        view.Cost = fmt.Sprintf("≈$%.6f", out.Result.Usage.CostUSD)
        if m := out.Result.Usage.Model; m != "" {
            view.Cost += " · " + m
        }
    }
    switch {
    case errors.Is(out.err, olxjev.ErrCategoryUnresolved) && out.Result != nil && len(out.Result.CategoryCandidates) > 0:
        view.Hint = "Не вдалося точно визначити категорію. Уточніть, що саме шукаєте, або оберіть:"
        for _, c := range out.Result.CategoryCandidates {
            label := strings.TrimPrefix(c.Label, "Нерухомість › ")
            view.Suggestions = append(view.Suggestions, suggestion{Label: label, Query: strings.TrimSpace(q) + ", " + lowerFirst(lastSegment(label))})
        }
    case errors.Is(out.err, olxjev.ErrCategoryUnresolved):
        view.Hint = "Не вдалося визначити категорію. Уточніть, що саме шукаєте: «зняти квартиру», «купити будинок», «оренда офісу»…"
    case errors.Is(out.err, olxjev.ErrLocationUnresolved):
        view.Hint = "Не вдалося визначити, де шукати: таких населених пунктів кілька або назву не знайдено. Додайте область до запиту, наприклад «… Одеська область», або оберіть:"
        for _, c := range out.Result.CityCandidates {
            if len(view.Suggestions) == maxSuggestions {
                break
            }
            view.Suggestions = append(view.Suggestions, suggestion{Label: c.Label, Query: strings.TrimSpace(q) + ", " + c.Label})
        }
    case out.Error != "":
        view.Error = out.Error
    }
    if len(out.URLs) > 0 {
        view.URL = out.URLs[0].URL
        view.Alternatives = out.URLs[1:]
    }
    if out.Result != nil && out.Result.Fields[olxjev.QuestionCategory].Value != "" {
        view.Chips = chips(resolver.Composer, out.Result)
    }
    for _, key := range out.NotInURL {
        view.NotInURL = append(view.NotInURL, elementTitle(resolver.Composer, key))
    }
    if s.debug {
        view.Debug = s.debugView(resolver, q, out)
    }
    return view
}

// logResolve writes one line per resolve: the backend, the query, decision
// usage with the estimated cost, the time taken and any error.
func (s *web) logResolve(backend, q string, out output, took time.Duration, cached bool) {
    line := fmt.Sprintf("resolve [%s] «%s»: ", backend, q)
    switch {
    case cached:
        line += fmt.Sprintf("cache hit (%d cached)", s.cache.Len())
    case out.Result != nil:
        line += costLine(out.Result.Usage)
    default:
        line += "no model answer"
    }
    line += fmt.Sprintf(" in %.1fs", took.Seconds())
    if out.Error != "" {
        line += "; error: " + out.Error
    }
    s.log.Print(line)
}

func (s *web) render(w http.ResponseWriter, status int, name string, data any) {
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    w.WriteHeader(status)
    if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
        fmt.Fprintf(w, "<p class=\"error\">%s</p>", template.HTMLEscapeString(err.Error()))
    }
}

// chips lists the resolved fields in wizard order: category, place, then
// the elements as the order tree reaches them.
func chips(composer *botform.BotFormComposer, res *olxjev.Result) []chip {
    keys := []string{olxjev.QuestionCategory, "city"}
    if _, ok := res.Fields["city"]; !ok {
        keys[1] = "region"
    }
    if seq, err := olxform.Walk(composer, res.Values); err == nil {
        for _, k := range seq {
            if k != "region" && k != "city" {
                keys = append(keys, k)
            }
        }
    }
    out := []chip{}
    for _, k := range keys {
        f, ok := res.Fields[k]
        if !ok {
            continue
        }
        label := f.Label
        if strings.HasPrefix(k, "filter_float_") {
            label = groupThousands(f.Value)
        }
        out = append(out, chip{Title: elementTitle(composer, k), Label: label, Probability: f.Probability})
    }
    for _, alt := range res.CityAlternatives {
        out = append(out, chip{Title: "або місто", Label: alt.Label, Probability: alt.Probability})
    }
    return out
}

var fixedTitles = map[string]string{
    olxjev.QuestionCategory: "Категорія",
    "city":                  "Місто",
    "region":                "Область",
}

// elementTitle is the wizard's title for key, tidied («Ціна , до» → «Ціна, до»).
func elementTitle(composer *botform.BotFormComposer, key string) string {
    if t, ok := fixedTitles[key]; ok {
        return t
    }
    el, ok := composer.GetElement(key)
    if !ok {
        return key
    }
    title := ""
    switch r := el.Renderer.(type) {
    case botform.BotTextKeyboardControl:
        title = r.Title
    case botform.BotMultiChoiceKeyboardControl:
        title = r.Title
    case botform.BotChoiceKeyboardControl:
        title = r.Title
    }
    if title = strings.TrimSpace(strings.ReplaceAll(title, " ,", ",")); title == "" {
        return key
    }
    return title
}

// lastSegment is the leaf of a category path: «Квартири › Продаж квартир» →
// «Продаж квартир».
func lastSegment(label string) string {
    parts := strings.Split(label, " › ")
    return parts[len(parts)-1]
}

// lowerFirst lowercases the first letter only, keeping abbreviations
// («Оренда МАФів» → «оренда МАФів»).
func lowerFirst(s string) string {
    for i, r := range s {
        return string(unicode.ToLower(r)) + s[i+utf8.RuneLen(r):]
    }
    return s
}

// groupThousands renders "10000" as "10 000"; other strings pass through.
func groupThousands(s string) string {
    whole, frac, _ := strings.Cut(s, ".")
    if len(whole) <= 4 || strings.Trim(whole, "0123456789") != "" {
        return s
    }
    var b strings.Builder
    for i, c := range whole {
        if i > 0 && (len(whole)-i)%3 == 0 {
            b.WriteRune(' ')
        }
        b.WriteRune(c)
    }
    if frac != "" {
        b.WriteString("." + frac)
    }
    return b.String()
}
