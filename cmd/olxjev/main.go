// Command olxjev resolves free-text real-estate queries into OLX.ua search
// URLs with a decision model (TypeSafe's Jev by default, SubQ's Snap with
// -backend subq). Usage:
//
//     TYPESAFE_API_KEY=... olxjev -q "хочу зняти однушку в Харкові до 10к"
//     SUBQ_API_KEY=... olxjev -backend subq -q "..."
//     olxjev -json < queries.txt
//     olxjev -serve 127.0.0.1:8080   # web UI, see web.go
package main

import (
    "bufio"
    "cmp"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "flag"
    "io"
    "os"
    "sort"
    "strings"
    "time"

    "github.com/mattn/go-jev"

    "github.com/mixadior/olxjev"
    "github.com/mixadior/olxjev/decision"
    "github.com/mixadior/olxjev/decision/subq"
    "github.com/mixadior/olxjev/decision/typesafe"
    "github.com/mixadior/olxjev/request"
    "github.com/mixadior/olxjev/olxform"
)

func main() {
    os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

// output is the -json shape of one resolved query.
type output struct {
    Query    string           `json:"query"`
    Result   *olxjev.Result   `json:"result,omitempty"`
    URLs     []olxjev.CityURL `json:"urls,omitempty"`       // resolved city first, then city alternatives
    NotInURL []string         `json:"not_in_url,omitempty"` // resolved, but OLX URL has no parameter for them
    Error    string           `json:"error,omitempty"`

    err       error      // Resolve's error, for errors.Is
    urlErr    error      // building the OLX URL failed
    exchanges []exchange // the decision stages, with -web-debug
}

// backend is one decision provider the CLI can use.
type backend struct {
    keyEnv      string // the API key's environment variable
    keyOptional bool   // the default model answers without a key
    url         string // the default endpoint
    model       string // the default model; "" = the adapter's
    new         func(apiKey string, opts ...jev.ClientOption) decision.Decider
}

var backends = map[string]backend{
    typesafe.Name: {keyEnv: "TYPESAFE_API_KEY", url: typesafe.DefaultURL, new: typesafe.New},
    subq.Name:     {keyEnv: "SUBQ_API_KEY", url: subq.DefaultURL, new: subq.New},
}

// viaOpenCode is the typesafe backend served by OpenCode Zen (-via opencode):
// the same Jev and thresholds, its free jev-1.13-free by default.
var viaOpenCode = backend{keyEnv: "OPENCODE_API_KEY", keyOptional: true, url: typesafe.OpenCodeURL,
    model: typesafe.OpenCodeModel, new: typesafe.New}

// backendFor is the named backend as -via routes it: "opencode" serves the
// typesafe backend from OpenCode Zen and leaves the others alone.
func backendFor(name, via string) (backend, bool) {
    if name == typesafe.Name && via == "opencode" {
        return viaOpenCode, true
    }
    b, ok := backends[name]
    return b, ok
}

// debugCacheSize is -cache-size's default with -web-debug: an entry then
// keeps the resolve's questions, state and raw answers, ~30 KB instead of
// ~2 KB (18–27 KB measured with a fake model's short answers), so 100000
// entries would take ~3 GB.
const debugCacheSize = 2000

// backendOrder is the order the web UI's switch lists the backends in.
var backendOrder = []string{typesafe.Name, subq.Name}

// backendOptions select and configure the decision backend; empty endpoint
// and model mean the backend's defaults.
type backendOptions struct {
    name, via, apiKey, endpoint, model string
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
    fs := flag.NewFlagSet("olxjev", flag.ContinueOnError)
    fs.SetOutput(stderr)
    query := fs.String("q", "", "query text; when empty, queries are read from stdin, one per line")
    threshold := fs.Float64("threshold", 0, "minimum model probability to accept an answer, for every question type (default: the backend's calibrated per-type thresholds)")
    asJSON := fs.Bool("json", false, "print results as JSON lines")
    noURL := fs.Bool("no-url", false, "do not call OLX to build the search URL")
    debug := fs.Bool("debug", false, "print the decision state, questions and raw answers to stderr")
    backendName := fs.String("backend", typesafe.Name, "decision backend: typesafe (Jev) or subq (SubQ Snap)")
    endpoint := fs.String("endpoint", "", "decision API endpoint (default: the backend's)")
    model := fs.String("model", "", "model ID (default: the backend's)")
    via := fs.String("via", "typesafe", "where the typesafe backend's Jev is served: typesafe (TypeSafe's API, TYPESAFE_API_KEY) or opencode (OpenCode Zen, jev-1.13-free by default, OPENCODE_API_KEY optional)")
    serve := fs.String("serve", "", "serve the web UI on this address instead, e.g. 127.0.0.1:8080")
    cacheSize := fs.Int("cache-size", 100000, "with -serve: how many answered queries to keep in the in-memory LRU cache (0 disables it; 2000 by default with -web-debug)")
    webDebug := fs.Bool("web-debug", false, "with -serve: show every filter of the result's category and of the low-confidence ones, with the model's questions, raw requests and answers (exposes the prompts: not for a public server)")
    if err := fs.Parse(args); err != nil {
        return 2
    }

    if *via != "typesafe" && *via != "opencode" {
        fmt.Fprintf(stderr, "olxjev: unknown -via %q (typesafe or opencode)\n", *via)
        return 2
    }
    b, ok := backendFor(*backendName, *via)
    if !ok {
        fmt.Fprintf(stderr, "olxjev: unknown -backend %q (typesafe or subq)\n", *backendName)
        return 2
    }
    apiKey := getenv(b.keyEnv)
    if apiKey == "" && !b.keyOptional && (*endpoint == "" || *endpoint == b.url) {
        fmt.Fprintf(stderr, "olxjev: %s is not set\n", b.keyEnv)
        return 2
    }

    resolver, err := newResolver(backendOptions{name: *backendName, via: *via, apiKey: apiKey, endpoint: *endpoint, model: *model}, *threshold)
    if err != nil {
        fmt.Fprintln(stderr, "olxjev:", err)
        return 1
    }
    if *debug {
        resolver.Trace = func(stage string, state any, questions map[string]decision.Question, d *decision.Decision) {
            dump, _ := json.MarshalIndent(map[string]any{"stage": stage, "state": state, "questions": questions}, "", "  ")
            fmt.Fprintln(stderr, string(dump))
            if d != nil {
                for _, raw := range d.Raw {
                    fmt.Fprintln(stderr, string(raw))
                }
            }
        }
    }

    var rq request.Request
    if !*noURL {
        rq = request.NewLocalRequest()
    }

    if *serve != "" {
        resolvers, err := webResolvers(resolver, *via, *threshold, getenv)
        if err != nil {
            fmt.Fprintln(stderr, "olxjev:", err)
            return 1
        }
        cacheSizeSet := false
        fs.Visit(func(f *flag.Flag) { cacheSizeSet = cacheSizeSet || f.Name == "cache-size" })
        if *webDebug && !cacheSizeSet {
            *cacheSize = debugCacheSize
            fmt.Fprintf(stderr, "olxjev: -web-debug caches each answer's model exchanges (~30 KB): cache limited to %d entries (-cache-size to change)\n", debugCacheSize)
        }
        return serveWeb(*serve, resolvers, rq, webOptions{cacheSize: *cacheSize, debug: *webDebug, backend: *backendName, via: *via}, stdout, stderr)
    }

    queries := []string{*query}
    if *query == "" {
        queries = readLines(stdin)
    }

    exit := 0
    ctx := context.Background()
    for _, q := range queries {
        out := resolveOne(ctx, resolver, q, rq)
        if out.Error != "" {
            exit = 1
        }
        if *asJSON {
            line, _ := json.Marshal(out)
            fmt.Fprintln(stdout, string(line))
        } else {
            printText(stdout, out)
        }
    }
    return exit
}

// newResolver builds the composer, the decision backend and the resolver.
// A threshold > 0 replaces the backend's calibrated per-type cutoffs for
// every type but the multi-choice gate.
func newResolver(bo backendOptions, threshold float64) (*olxjev.Resolver, error) {
    b, ok := backendFor(bo.name, bo.via)
    if !ok {
        return nil, fmt.Errorf("unknown backend %q", bo.name)
    }
    composer, err := olxform.NewComposer()
    if err != nil {
        return nil, err
    }
    opts := []jev.ClientOption{jev.WithTimeout(30 * time.Second), jev.WithMaxRetries(3), jev.WithURL(b.url)}
    if bo.endpoint != "" {
        opts = append(opts, jev.WithURL(bo.endpoint))
    }
    if m := cmp.Or(bo.model, b.model); m != "" {
        opts = append(opts, jev.WithModel(m))
    }
    resolver, err := olxjev.NewResolver(composer, b.new(bo.apiKey, opts...))
    if err != nil {
        return nil, err
    }
    if threshold > 0 {
        // every type falls back to Threshold; the gate only decides which
        // options are asked, so it keeps its calibrated cutoff
        resolver.Thresholds = olxjev.Thresholds{Gate: resolver.Thresholds.Gate}
        resolver.Threshold = threshold
    }
    return resolver, nil
}

// webResolvers is what the web UI switches between: the default resolver
// and one per other backend whose API key is set (or, like the typesafe
// backend -via opencode, needs none), in backendOrder. -endpoint and -model
// are the default backend's; -via, -threshold and -debug apply to all.
func webResolvers(def *olxjev.Resolver, via string, threshold float64, getenv func(string) string) ([]*olxjev.Resolver, error) {
    var out []*olxjev.Resolver
    for _, name := range backendOrder {
        if name == def.Decider.Name() {
            out = append(out, def)
            continue
        }
        b, _ := backendFor(name, via)
        key := getenv(b.keyEnv)
        if key == "" && !b.keyOptional {
            continue
        }
        r, err := newResolver(backendOptions{name: name, via: via, apiKey: key}, threshold)
        if err != nil {
            return nil, err
        }
        r.Trace = def.Trace
        out = append(out, r)
    }
    return out, nil
}

func resolveOne(ctx context.Context, resolver *olxjev.Resolver, query string, rq request.Request) output {
    out := output{Query: query}
    res, err := resolver.Resolve(ctx, query)
    out.Result = res
    out.err = err
    if err != nil {
        out.Error = err.Error()
    }
    if rq == nil || res == nil || errors.Is(err, olxjev.ErrCategoryUnresolved) || errors.Is(err, olxjev.ErrLocationUnresolved) {
        return out
    }
    urls, notInURL, urlErr := olxjev.SearchURLs(ctx, res, rq)
    if urlErr != nil {
        out.urlErr = urlErr
        out.Error = strings.TrimPrefix(out.Error+"; ", "; ") + "url: " + urlErr.Error()
        return out
    }
    out.URLs = urls
    out.NotInURL = notInURL
    return out
}

func printText(w io.Writer, out output) {
    fmt.Fprintf(w, "query: %s\n", out.Query)
    if out.Result != nil {
        keys := make([]string, 0, len(out.Result.Fields))
        for k := range out.Result.Fields {
            keys = append(keys, k)
        }
        sort.Strings(keys)
        for _, k := range keys {
            f := out.Result.Fields[k]
            fmt.Fprintf(w, "  %s = %s  [%s, %.2f]\n", k, f.Value, f.Label, f.Probability)
        }
        for _, alt := range out.Result.CityAlternatives {
            fmt.Fprintf(w, "  city alternative = %s  [%s, %.2f]\n", alt.Value, alt.Label, alt.Probability)
        }
        for _, c := range out.Result.CategoryCandidates {
            fmt.Fprintf(w, "  category candidate = %s  [%s, %.2f]\n", c.Value, c.Label, c.Probability)
        }
        for _, c := range out.Result.CityCandidates {
            fmt.Fprintf(w, "  city candidate = %s  [%s, %.2f]\n", c.Value, c.Label, c.Probability)
        }
        if out.Result.Usage.Calls > 0 {
            fmt.Fprintf(w, "  cost: %s\n", costLine(out.Result.Usage))
        }
        if len(out.Result.Skipped) > 0 {
            fmt.Fprintf(w, "  skipped: %s\n", strings.Join(out.Result.Skipped, ", "))
        }
    }
    for i, u := range out.URLs {
        if i == 0 {
            fmt.Fprintf(w, "  url: %s\n", u.URL)
        } else {
            fmt.Fprintf(w, "  or (%s): %s\n", u.Label, u.URL)
        }
    }
    if len(out.NotInURL) > 0 {
        fmt.Fprintf(w, "  not in url: %s\n", strings.Join(out.NotInURL, ", "))
    }
    if out.Error != "" {
        fmt.Fprintf(w, "  error: %s\n", out.Error)
    }
}

// costLine summarises what a resolve spent on decisions:
// "≈$0.000126 (jev-1.14, 2 calls, 3000 input tokens)".
func costLine(u decision.Usage) string {
    calls := "calls"
    if u.Calls == 1 {
        calls = "call"
    }
    model := ""
    if u.Model != "" {
        model = u.Model + ", "
    }
    return fmt.Sprintf("≈$%.6f (%s%d %s, %d input tokens)", u.CostUSD, model, u.Calls, calls, u.InputTokens)
}

func readLines(r io.Reader) []string {
    lines := []string{}
    scanner := bufio.NewScanner(r)
    for scanner.Scan() {
        if line := strings.TrimSpace(scanner.Text()); line != "" {
            lines = append(lines, line)
        }
    }
    return lines
}
