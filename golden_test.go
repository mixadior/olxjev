package olxjev

import (
    "cmp"
    "context"
    "errors"
    "fmt"
    "maps"
    "os"
    "slices"
    "sort"
    "strings"
    "testing"
    "time"

    "github.com/mattn/go-jev"
    botform "github.com/mixadior/bfc"
    "gopkg.in/yaml.v2"

    "github.com/mixadior/olxjev/decision"
    "github.com/mixadior/olxjev/decision/subq"
    "github.com/mixadior/olxjev/decision/typesafe"
)

type goldenCase struct {
    Q      string            `yaml:"q"`
    Expect map[string]string `yaml:"expect"`
}

// Accuracy floors enforced by TestGolden; other fields are reported only.
var goldenFloors = map[string]float64{"category": 0.9, "city": 0.9}

// goldenErrorKind names Resolve's error for the corpus key "error":
// "" (none), "location" or "category".
func goldenErrorKind(err error) string {
    switch {
    case err == nil:
        return ""
    case errors.Is(err, ErrLocationUnresolved):
        return "location"
    case errors.Is(err, ErrCategoryUnresolved):
        return "category"
    }
    return err.Error()
}

func loadGolden(t *testing.T) []goldenCase {
    t.Helper()
    raw, err := os.ReadFile("testdata/golden.yaml")
    if err != nil {
        t.Fatalf("read golden.yaml: %v", err)
    }
    cases := []goldenCase{}
    if err := yaml.Unmarshal(raw, &cases); err != nil {
        t.Fatalf("parse golden.yaml: %v", err)
    }
    return cases
}

// TestGoldenCorpusIsValid runs offline: every expected category exists and
// every expected field applies to that category, so corpus typos fail fast.
func TestGoldenCorpusIsValid(t *testing.T) {
    bfc := testComposer(t)
    catalog, err := NewCatalog(bfc.Data)
    if err != nil {
        t.Fatalf("NewCatalog: %v", err)
    }
    cases := loadGolden(t)
    if len(cases) < 40 {
        t.Errorf("golden corpus has %d cases, want at least 40", len(cases))
    }
    for _, c := range cases {
        if e, ok := c.Expect["error"]; ok && e != "" && e != "location" && e != "category" {
            t.Errorf("%q: unknown error kind %q", c.Q, e)
        }
        leaf, ok := catalog.Leaf(c.Expect["category"])
        if !ok {
            t.Errorf("%q: unknown category %q", c.Q, c.Expect["category"])
            continue
        }
        values := leaf.Values()
        if region, ok := c.Expect["region"]; ok {
            if catalog.RegionLabel(region) == "" {
                t.Errorf("%q: unknown region %q", c.Q, region)
            }
            values["region"] = region
        }
        if cities, ok := c.Expect["city"]; ok {
            known := true
            for _, city := range alternatives(cities) {
                if cityRegion(bfc.Data.Children["region"].Children, city) == "" {
                    t.Errorf("%q: unknown city %q", c.Q, city)
                    known = false
                }
            }
            if !known {
                continue
            }
            city := alternatives(cities)[0]
            values["region"], values["city"] = cityRegion(bfc.Data.Children["region"].Children, city), city
        }
        elements, err := applicableElements(bfc, values)
        if err != nil {
            t.Fatalf("%q: applicableElements: %v", c.Q, err)
        }
        applicable := map[string]pendingElement{}
        for _, el := range elements {
            applicable[el.Key] = el
        }
        for key, want := range c.Expect {
            if key == "category" || key == "region" || key == "city" || key == "error" {
                continue
            }
            el, ok := applicable[key]
            if !ok {
                t.Errorf("%q: %s does not apply to %s", c.Q, key, leaf.Key)
                continue
            }
            if el.Kind == kindNumber || want == "" { // "" = must stay unset
                continue
            }
            for _, alt := range alternatives(want) {
                options := []string{alt}
                if el.Kind == kindMulti {
                    options = strings.Split(alt, el.Separator)
                }
                for _, option := range options {
                    if _, ok := el.Options[option]; !ok {
                        t.Errorf("%q: %s has no option %q", c.Q, key, option)
                    }
                }
            }
        }
    }
}

// TestGoldenCitiesShortlisted runs offline: Jev can only pick a city the
// gazetteer offers, so every expected city must be in its case's shortlist.
func TestGoldenCitiesShortlisted(t *testing.T) {
    g, err := NewGazetteer(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewGazetteer: %v", err)
    }
    for _, c := range loadGolden(t) {
        want, ok := c.Expect["city"]
        if !ok {
            continue
        }
        shortlist := g.Shortlist(c.Q, DefaultShortlist)
        found := false
        for _, cand := range shortlist {
            found = found || slices.Contains(alternatives(want), cand.CityID)
        }
        if !found {
            t.Errorf("%q: city %s not shortlisted: %v", c.Q, want, shortlist)
        }
    }
}

// liveDecider is the backend OLXJEV_BACKEND names (typesafe by default),
// with its API key from the environment; the test skips without
// OLXJEV_LIVE=1 or the key, since live runs spend credits.
func liveDecider(t *testing.T) decision.Decider {
    t.Helper()
    if os.Getenv("OLXJEV_LIVE") != "1" {
        t.Skip("set OLXJEV_LIVE=1 and the backend's API key to run the live eval (spends API credits)")
    }
    opts := []jev.ClientOption{jev.WithTimeout(30 * time.Second), jev.WithMaxRetries(3)}
    switch b := os.Getenv("OLXJEV_BACKEND"); b {
    case "", typesafe.Name:
        if os.Getenv("OLXJEV_VIA") == "opencode" {
            // OpenCode Zen's free Jev needs no key; OPENCODE_API_KEY is sent when set
            opts = append(opts, jev.WithURL(typesafe.OpenCodeURL), jev.WithModel(cmp.Or(os.Getenv("OLXJEV_MODEL"), typesafe.OpenCodeModel)))
            return typesafe.New(os.Getenv("OPENCODE_API_KEY"), opts...)
        }
        if os.Getenv("TYPESAFE_API_KEY") == "" {
            t.Skip("set TYPESAFE_API_KEY")
        }
        return typesafe.New(os.Getenv("TYPESAFE_API_KEY"), opts...)
    case subq.Name:
        if os.Getenv("SUBQ_API_KEY") == "" {
            t.Skip("set SUBQ_API_KEY")
        }
        return subq.New(os.Getenv("SUBQ_API_KEY"), opts...)
    default:
        t.Fatalf("OLXJEV_BACKEND=%q: want typesafe or subq", b)
    }
    return nil
}

// TestGolden calls the live decision API and reports per-field accuracy.
// Run: OLXJEV_LIVE=1 TYPESAFE_API_KEY=... go test -run TestGolden -v ./olxjev/
// OpenCode Zen (free Jev, no key): OLXJEV_LIVE=1 OLXJEV_VIA=opencode go test -run TestGolden -v ./olxjev/
// SubQ: OLXJEV_LIVE=1 OLXJEV_BACKEND=subq SUBQ_API_KEY=... go test -run TestGolden -v ./olxjev/
func TestGolden(t *testing.T) {
    decider := liveDecider(t)
    r, err := NewResolver(testComposer(t), decider)
    if err != nil {
        t.Fatalf("NewResolver: %v", err)
    }
    t.Logf("backend %s, model %s, thresholds %+v", decider.Name(), decider.Model(), r.Thresholds)

    // every question and raw answer of the current case, for calibration
    asked, answered := map[string]decision.Question{}, map[string]*decision.Answer{}
    stageTokens := map[string]int{} // input tokens per stage, the whole corpus
    r.Trace = func(stage string, _ any, questions map[string]decision.Question, d *decision.Decision) {
        maps.Copy(asked, questions)
        if d != nil {
            maps.Copy(answered, d.Answers)
            stageTokens[stage] += d.Usage.InputTokens
        }
    }
    samples := []calSample{}
    var usage decision.Usage
    var elapsed time.Duration

    type stat struct{ hit, total int }
    stats := map[string]*stat{}
    cases := loadGolden(t)
    for _, c := range cases {
        clear(asked)
        clear(answered)
        start := time.Now()
        res, err := r.Resolve(context.Background(), c.Q)
        elapsed += time.Since(start)
        if res != nil {
            usage.Add(res.Usage)
        }
        for _, s := range collectSamples(c.Expect, asked, answered) {
            s.Query = c.Q
            samples = append(samples, s)
        }
        values := map[string]string{}
        if res != nil {
            values = res.Values
        }
        if err != nil {
            t.Logf("%q: %v", c.Q, err)
        }
        for key, want := range c.Expect {
            got := values[key]
            switch key {
            case "category":
                got = r.Catalog.LeafOf(values)
            case "error":
                got = goldenErrorKind(err)
            }
            if stats[key] == nil {
                stats[key] = &stat{}
            }
            stats[key].total++
            if slices.Contains(alternatives(want), got) {
                stats[key].hit++
            } else {
                t.Logf("MISS %q: %s = %q, want %q", c.Q, key, got, want)
            }
        }
    }

    keys := make([]string, 0, len(stats))
    for k := range stats {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    report := []string{"field accuracy:"}
    for _, k := range keys {
        s := stats[k]
        report = append(report, fmt.Sprintf("  %-40s %3d/%-3d %5.1f%%", k, s.hit, s.total, 100*float64(s.hit)/float64(s.total)))
    }
    t.Log(strings.Join(report, "\n"))
    t.Log(calibrationReport(samples))
    t.Logf("usage: %d input tokens (%d per query; per stage %v), %d calls, %s per query, $%.4f total",
        usage.InputTokens, usage.InputTokens/len(cases), stageTokens, usage.Calls, (elapsed / time.Duration(len(cases))).Round(time.Millisecond), usage.CostUSD)

    for field, floor := range goldenFloors {
        if s := stats[field]; s != nil && float64(s.hit)/float64(s.total) < floor {
            t.Errorf("%s accuracy %.1f%% is below the %.0f%% floor", field, 100*float64(s.hit)/float64(s.total), 100*floor)
        }
    }
}

// alternatives splits an expectation like "157|2569" into its accepted values.
func alternatives(want string) []string {
    return strings.Split(want, "|")
}

func cityRegion(regions map[string]*botform.BotFormDataNode, city string) string {
    for id, region := range regions {
        if _, ok := region.Children[city]; ok {
            return id
        }
    }
    return ""
}
