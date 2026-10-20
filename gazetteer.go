package olxjev

import (
    "errors"
    "sort"
    "strings"
    "unicode"

    botform "github.com/mixadior/bfc"
)

// CityCandidate is one shortlisted city for the stage-1 city question.
type CityCandidate struct {
    RegionID string
    CityID   string
    Name     string // "Харків"
    Label    string // "Харків, Харківська область"
    Distance int    // summed edit distance of the matched words
    // HasDistricts marks the 14 biggest cities (they have district options)
    HasDistricts bool
}

// Gazetteer shortlists cities mentioned in free text (any spelling variant,
// any grammatical case) so Jev only chooses among a handful of candidates.
type Gazetteer struct {
    cities  []gazCity
    regions []gazRegion
}

// gazRegion is a region's adjective, folded: «Одеська область» → «одеська».
type gazRegion struct {
    id   string
    word []rune
}

// locativeEndings map a folded locative/dative ending back to the
// nominative one: «в Іванівці» → Іванівка, «в Ивановке» → Ивановка.
var locativeEndings = [][2]string{{"ци", "ка"}, {"зи", "га"}, {"си", "ха"}, {"ке", "ка"}}

type gazCity struct {
    regionID string
    cityID   string
    name     string
    label    string
    words    [][]rune
    runeLen  int
    big      bool // has districts: the 14 biggest cities
}

type scoredCity struct {
    city     *gazCity
    distance int
}

// foldReplacer maps spelling variants onto one alphabet so
// «Харкові» and «Харькове» both fold to «харков…».
var foldReplacer = strings.NewReplacer(
    "ё", "е", "і", "и", "ї", "и", "ы", "и", "є", "е", "э", "е", "ґ", "г",
    "ь", "", "ъ", "", "'", "", "ʼ", "", "’", "", "`", "",
)

// stopWords are frequent query words that would otherwise match village names.
var stopWords = map[string]bool{}

func init() {
    for _, w := range strings.Fields(`хочу шукаю зняти зніму купити куплю продам оренда оренду орендувати
        квартиру квартира квартири будинок будинку дом кімнату кімната комнату снять сниму купить
        центр центрі центре район районі області область гривень грн доларів долларов без комісії
        тварин котом собакою поверх поверсі этаж метрів метров подобово посуточно довгострокова
        новобудова недорого терміново`) {
        for _, f := range foldWords(w) {
            stopWords[f] = true
        }
    }
}

func foldWords(s string) []string {
    s = foldReplacer.Replace(strings.ToLower(s))
    return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// NewGazetteer indexes every city under data.Children["region"].
func NewGazetteer(data *botform.BotFormDataNode) (*Gazetteer, error) {
    regions, ok := data.Children["region"]
    if !ok {
        return nil, errors.New("olxjev: data has no region")
    }
    g := &Gazetteer{}
    for regionID, region := range regions.Children {
        if words := foldWords(region.Value); len(words) > 0 && len([]rune(words[0])) >= 6 {
            g.regions = append(g.regions, gazRegion{id: regionID, word: []rune(words[0])})
        }
        for cityID, city := range region.Children {
            c := gazCity{
                regionID: regionID,
                cityID:   cityID,
                name:     city.Value,
                label:    city.Value + ", " + region.Value,
                big:      len(city.Children) > 0,
            }
            for _, w := range foldWords(city.Value) {
                c.words = append(c.words, []rune(w))
                c.runeLen += len([]rune(w))
            }
            if len(c.words) > 0 {
                g.cities = append(g.cities, c)
            }
        }
    }
    return g, nil
}

// Shortlist returns up to n cities whose names appear in text, best first:
// cities in a region the text names, then cities with districts, then lower
// edit distance relative to name length, then longer names. A word naming a
// region («Одеська область», «Одесской области») is not matched as a city.
func (g *Gazetteer) Shortlist(text string, n int) []CityCandidate {
    tokens := [][][]rune{} // per word: the word and its nominative variants
    named := map[string]bool{}
    for _, w := range foldWords(text) {
        if len([]rune(w)) < 3 || stopWords[w] {
            continue
        }
        if id := g.regionNamed([]rune(w)); id != "" {
            named[id] = true
            continue
        }
        tokens = append(tokens, wordVariants(w))
    }

    scored := []scoredCity{}
    for i := range g.cities {
        if d, ok := matchCity(&g.cities[i], tokens); ok {
            scored = append(scored, scoredCity{city: &g.cities[i], distance: d})
        }
    }
    sort.Slice(scored, func(i, j int) bool {
        a, b := scored[i], scored[j]
        if na, nb := named[a.city.regionID], named[b.city.regionID]; na != nb {
            return na
        }
        if a.city.big != b.city.big {
            return a.city.big
        }
        // a full multi-word match («Нова Каховка») beats a village matching one word
        if len(a.city.words) != len(b.city.words) {
            return len(a.city.words) > len(b.city.words)
        }
        ra := float64(a.distance) / float64(a.city.runeLen)
        rb := float64(b.distance) / float64(b.city.runeLen)
        if ra != rb {
            return ra < rb
        }
        if a.city.runeLen != b.city.runeLen {
            return a.city.runeLen > b.city.runeLen
        }
        return a.city.cityID < b.city.cityID
    })

    result := []CityCandidate{}
    for i := 0; i < len(scored) && i < n; i++ {
        c := scored[i].city
        result = append(result, CityCandidate{RegionID: c.regionID, CityID: c.cityID, Name: c.name, Label: c.label, Distance: scored[i].distance, HasDistricts: c.big})
    }
    return result
}

// regionNamed returns the region whose adjective the word is («одескои»
// for Одеська), or "". Region adjectives carry -ськ/-цьк/-зьк (folded:
// ск/цк/зк) and the first three letters must agree, so a city's own
// inflection («Одессе», «Вінниці») is not taken for its region.
func (g *Gazetteer) regionNamed(word []rune) string {
    s := string(word)
    if !strings.Contains(s, "ск") && !strings.Contains(s, "цк") && !strings.Contains(s, "зк") {
        return ""
    }
    for _, r := range g.regions {
        if len(word) >= 3 && string(word[:3]) == string(r.word[:3]) && wordDistance(r.word, word) <= allowedDistance(len(r.word)) {
            return r.id
        }
    }
    return ""
}

// wordVariants returns the folded word and, for a locative/dative ending,
// the nominative it inflects («иванивци» → «иванивка»).
func wordVariants(w string) [][]rune {
    variants := [][]rune{[]rune(w)}
    if len([]rune(w)) < 5 {
        return variants
    }
    for _, e := range locativeEndings {
        if strings.HasSuffix(w, e[0]) {
            variants = append(variants, []rune(strings.TrimSuffix(w, e[0])+e[1]))
        }
    }
    return variants
}

// matchCity finds the best run of consecutive tokens matching every word of
// the city name; it returns the summed distance of that run.
func matchCity(c *gazCity, tokens [][][]rune) (int, bool) {
    best, found := 0, false
    for i := 0; i+len(c.words) <= len(tokens); i++ {
        sum, ok := 0, true
        for j, word := range c.words {
            d := len(word) + 1
            for _, v := range tokens[i+j] {
                d = min(d, wordDistance(word, v))
            }
            if d > allowedDistance(len(word)) && !shortTailWord(j, word, tokens[i+j][0]) {
                ok = false
                break
            }
            sum += d
        }
        if ok && (!found || sum < best) {
            best, found = sum, true
        }
    }
    return best, found
}

// shortTailWord lets a short non-first word of a multi-word name match on its
// first letter alone: inflection alternates its stem («Ріг» → «Розі», «Роге»)
// and the preceding words already pin the city down.
func shortTailWord(position int, word, token []rune) bool {
    return position > 0 && len(word) <= 3 && token[0] == word[0]
}

// allowedDistance is the edit budget for a name word of n runes.
func allowedDistance(n int) int {
    switch {
    case n <= 3:
        return 0
    case n <= 5:
        return 1
    case n <= 7:
        return 2
    }
    return 3
}

// wordDistance compares a name word with prefixes of the token around the
// word's length, so inflected endings («Харкові», «Львовом») are tolerated.
func wordDistance(word, token []rune) int {
    best := len(word) + len(token)
    for k := len(word) - 1; k <= len(word)+2; k++ {
        if k < 1 || k > len(token) {
            continue
        }
        if d := levenshtein(word, token[:k]); d < best {
            best = d
        }
    }
    return best
}

func levenshtein(a, b []rune) int {
    prev := make([]int, len(b)+1)
    cur := make([]int, len(b)+1)
    for j := range prev {
        prev[j] = j
    }
    for i := 1; i <= len(a); i++ {
        cur[0] = i
        for j := 1; j <= len(b); j++ {
            cost := 1
            if a[i-1] == b[j-1] {
                cost = 0
            }
            cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
        }
        prev, cur = cur, prev
    }
    return prev[len(b)]
}
