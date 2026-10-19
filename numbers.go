package olxjev

import (
    "regexp"
    "strconv"
    "strings"
)

// NumberMention is one number found in the user's text.
type NumberMention struct {
    Value float64 // normalized value: "10к" -> 10000
    Raw   string  // the mention with up to two words of context on each side
    Start int     // byte offset of the mention in the normalized text

    end        int     // byte offset right after the mention (incl. multiplier word)
    multiplier float64 // 1, 1e3 or 1e6
    lowerBound bool    // said as a minimum: «від 8000», «не менше 40», the 8 of «8-12к»
}

// Name is the option name used for this number in Jev questions.
func (m NumberMention) Name() string {
    return strconv.FormatFloat(m.Value, 'f', -1, 64)
}

var (
    // a number ("10", "10 000", "1,5") optionally followed by one word ("к", "тис", "м")
    // "10 000", then "10,000" / "10.000" (groups of exactly three digits are
    // thousands), then plain or decimal "10", "1,5"
    numberRe = regexp.MustCompile(`(\d{1,3}(?:[ \x{00A0}]\d{3})+|\d{1,3}(?:[.,]\d{3})+|\d+(?:[.,]\d+)?)(?:[ \x{00A0}]?(\pL+))?\.?`)
    // a whole match of the second alternative above
    thousandsRe = regexp.MustCompile(`^\d{1,3}(?:[.,]\d{3})+$`)
    phoneRe  = regexp.MustCompile(`\+?(?:38)?0\d{2}[ \-]?\d{3}[ \-]?\d{2}[ \-]?\d{2}`)
    squareRe = regexp.MustCompile(`(?i)(кв\.?\s?м|м2|m2)`)
)

var multipliers = map[string]float64{
    "к": 1e3, "k": 1e3, "тис": 1e3, "тисяч": 1e3, "тисячі": 1e3, "тисяча": 1e3,
    "тыс": 1e3, "тысяч": 1e3, "тысячи": 1e3, "тысяча": 1e3,
    "млн": 1e6, "мільйон": 1e6, "мільйона": 1e6, "мільйонів": 1e6,
    "миллион": 1e6, "миллиона": 1e6, "миллионов": 1e6,
}

var yearWords = map[string]bool{
    "р": true, "рік": true, "року": true, "г": true, "год": true, "года": true, "году": true, "рр": true,
}

var rangeGlue = map[string]bool{"-": true, "–": true, "—": true, "до": true, "по": true}

// lowerBoundWords make the number right after them a minimum: «від 8000»,
// «не менше 40»; checked against the one or two words before the number.
var lowerBoundWords = map[string]bool{
    "від": true, "вiд": true, "от": true, "понад": true, "більше": true, "більш": true,
    "больше": true, "более": true, "свыше": true, "мінімум": true, "минимум": true, "min": true,
    "дорожче": true, "дороже": true, "вище": true, "выше": true,
    "не менше": true, "не менш": true, "не меньше": true, "не менее": true,
    "не нижче": true, "не ниже": true, "не дешевше": true, "не дешевле": true,
}

// ExtractNumbers returns every number mentioned in text, normalized, in order
// of appearance and de-duplicated by value. Phone numbers and years
// ("2015 року") are skipped; "від 8 до 12 тис" yields 8000 and 12000.
func ExtractNumbers(text string) []NumberMention {
    text = squareRe.ReplaceAllString(text, "м²")
    text = phoneRe.ReplaceAllStringFunc(text, func(s string) string {
        return strings.Repeat(" ", len(s))
    })

    mentions := []NumberMention{}
    for _, loc := range numberRe.FindAllStringSubmatchIndex(text, -1) {
        digits := text[loc[2]:loc[3]]
        if thousandsRe.MatchString(digits) {
            digits = strings.NewReplacer(",", "", ".", "").Replace(digits)
        }
        digits = strings.NewReplacer(" ", "", " ", "", ",", ".").Replace(digits)
        value, err := strconv.ParseFloat(digits, 64)
        if err != nil {
            continue
        }
        m := NumberMention{Value: value, Start: loc[0], end: loc[1], multiplier: 1, lowerBound: saidAsMinimum(text[:loc[0]])}
        if loc[4] >= 0 {
            word := strings.ToLower(text[loc[4]:loc[5]])
            if yearWords[word] && value >= 1950 && value <= 2035 {
                continue
            }
            if mult, ok := multipliers[word]; ok && !(isRoomsShorthand(word, text[loc[1]:])) {
                m.multiplier = mult
                m.Value = value * mult
            } else {
                // the word is not part of the number; keep it only as context
                m.end = loc[3]
            }
        }
        mentions = append(mentions, m)
    }

    // "від 8 до 12 тис", "8-12к": the first number inherits the multiplier
    for i := 0; i+1 < len(mentions); i++ {
        cur, next := &mentions[i], mentions[i+1]
        glue := strings.ToLower(strings.TrimSpace(text[cur.end:next.Start]))
        if !rangeGlue[glue] {
            continue
        }
        if cur.multiplier == 1 && next.multiplier > 1 && cur.Value < 1000 {
            cur.multiplier = next.multiplier
            cur.Value *= next.multiplier
        }
        if cur.Value < next.Value {
            cur.lowerBound = true
        }
    }

    result := []NumberMention{}
    seen := map[string]int{}
    for _, m := range mentions {
        m.Raw = contextAround(text, m.Start, m.end, 2)
        if idx, ok := seen[m.Name()]; ok {
            result[idx].Raw += " / " + m.Raw
            result[idx].lowerBound = result[idx].lowerBound || m.lowerBound
            continue
        }
        seen[m.Name()] = len(result)
        result = append(result, m)
    }
    return result
}

// saidAsMinimum reports whether before ends with a lower-bound word or phrase.
func saidAsMinimum(before string) bool {
    words := strings.Fields(strings.ToLower(before))
    n := len(words)
    if n >= 1 && lowerBoundWords[strings.Trim(words[n-1], ",.:;")] {
        return true
    }
    return n >= 2 && lowerBoundWords[words[n-2]+" "+strings.Trim(words[n-1], ",.:;")]
}

// isRoomsShorthand reports whether "2к" means "2-room" ("2к квартира").
func isRoomsShorthand(word, rest string) bool {
    if word != "к" && word != "k" {
        return false
    }
    next := strings.ToLower(strings.TrimLeft(rest, " . "))
    return strings.HasPrefix(next, "кв") || strings.HasPrefix(next, "кім") || strings.HasPrefix(next, "ком")
}

// contextAround returns text[start:end] widened by up to n words on each side.
func contextAround(text string, start, end, n int) string {
    left := start
    for w := 0; w < n; w++ {
        for left > 0 && text[left-1] == ' ' {
            left--
        }
        for left > 0 && text[left-1] != ' ' {
            left--
        }
    }
    right := end
    for w := 0; w < n; w++ {
        for right < len(text) && text[right] == ' ' {
            right++
        }
        for right < len(text) && text[right] != ' ' {
            right++
        }
    }
    return strings.TrimSpace(text[left:right])
}
