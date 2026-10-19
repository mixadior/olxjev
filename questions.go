package olxjev

import (
    "fmt"
    "slices"
    "sort"
    "strings"

    "github.com/mixadior/olxjev/decision"
    botform "github.com/mixadior/bfc"

    "github.com/mixadior/olxjev/olxform"
)

// NoneOption is the option name meaning "not mentioned in the query".
const NoneOption = "none"

// Stage-1 question keys.
const (
    QuestionCategory = "category"
    QuestionCity     = "city"
    QuestionRegion   = "region"
    // QuestionPlace asks whether the query names a place at all; the
    // gazetteer shortlists villages for ordinary words («тур» → Тур).
    QuestionPlace = "place"
)

const noneDesc = "не вказано в запиті"

type elementKind int

const (
    kindChoice elementKind = iota // one "choice" over the element's data options
    kindMulti                     // one "noul" per data option
    kindNumber                    // one "choice" over numbers extracted from the query
    kindBool                      // one "noul": does the query ask for the on/off filter
)

// pendingElement is a stage-2 element still to be resolved.
type pendingElement struct {
    Key       string
    Title     string
    Kind      elementKind
    Options   map[string]string // option key -> label; empty for kindNumber
    Separator string            // kindMulti value separator
}

// stage1Questions builds the category, city and region questions. The city
// question is omitted when the shortlist is empty.
// categoryDesc is the structured description of a category Jev confuses
// with a neighbour (TypeSafe: describe what an option is for, what belongs
// to the neighbouring option, and examples).
type categoryDesc struct {
    Category string   `json:"category"`
    For      string   `json:"for"`
    NotFor   string   `json:"not_for"`
    Examples []string `json:"examples"`
}

const (
    notLongTerm = "оренда подобово, погодинно, на добу, посуточно — це «Житло подобово»"
    notDaily    = "оренда без слів «подобово», «погодинно», «на добу» — це довгострокова оренда"
)

// categoryNotes describe the confusable categories; the live golden eval
// split «готель на морі» between hotel stays and hotel sales.
var categoryNotes = map[string]categoryDesc{
    "category_3713": {
        For:      "переночувати чи відпочити в готелі, на базі відпочинку, у пансіонаті",
        NotFor:   "купівля готелю чи бази відпочинку як об'єкта — це «Продаж готелів, баз відпочинку»; готовий тур чи путівка — «Пропозиції Туроператорів»",
        Examples: []string{"готель на морі", "база відпочинку на вихідні", "номер у готелі в Буковелі"},
    },
    "category_3715": {
        For:      "готовий тур чи путівка від туроператора або турагенції",
        NotFor:   "окремо готель, база відпочинку чи житло без туру — це «Готелі, бази відпочинку» чи інше житло подобово",
        Examples: []string{"тур від туроператора", "путівка на море", "гарячий тур до Єгипту"},
    },
    "category_3684": {
        For:      "купити готель, базу відпочинку чи пансіонат як об'єкт або бізнес",
        NotFor:   "проживання чи відпочинок у готелі — це «Житло подобово › Готелі, бази відпочинку»",
        Examples: []string{"купити готель в Одесі", "продаж бази відпочинку"},
    },
    "category_1760": {For: "зняти квартиру на місяці чи роки", NotFor: notLongTerm, Examples: []string{"зняти квартиру", "оренда однушки"}},
    "category_3711": {For: "зняти квартиру на добу, кілька днів чи години", NotFor: notDaily, Examples: []string{"квартира подобово", "квартира на добу", "квартира посуточно"}},
    "category_330":  {For: "зняти будинок на місяці чи роки", NotFor: notLongTerm, Examples: []string{"зняти будинок", "оренда будинку"}},
    "category_3710": {For: "зняти будинок на добу, вихідні чи кілька днів", NotFor: notDaily, Examples: []string{"будинок подобово", "котедж на вихідні"}},
    "category_1756": {For: "зняти кімнату на місяці чи роки", NotFor: notLongTerm, Examples: []string{"зняти кімнату", "оренда кімнати"}},
    "category_3712": {For: "зняти кімнату на добу чи кілька днів", NotFor: notDaily, Examples: []string{"кімната подобово", "кімната на добу"}},
}

// commercialSection is the category branch whose leaves name themselves:
// «Оренда офісів», «Продаж готелів, баз відпочинку».
const commercialSection = "Комерційна нерухомість"

// jevCategoryLabel is the category path the category question shows Jev:
// without the root every leaf shares, and only the leaf's own name under
// commercialSection. Live golden (2026-10-17, two runs): 5102 → 3415 tokens,
// 62/62 accepted both runs, lowest right answer 0.86–0.88 → 0.90–0.92.
func jevCategoryLabel(label string) string {
    parts := strings.Split(label, " › ")
    if len(parts) > 1 {
        parts = parts[1:]
    }
    if len(parts) > 1 && parts[0] == commercialSection {
        return parts[len(parts)-1]
    }
    return strings.Join(parts, " › ")
}

func stage1Questions(catalog *Catalog, cities []CityCandidate) map[string]decision.Question {
    categories := []decision.Option{}
    for _, leaf := range catalog.Leaves {
        var desc any = jevCategoryLabel(leaf.Label)
        if note, ok := categoryNotes[leaf.Key]; ok {
            note.Category = jevCategoryLabel(leaf.Label)
            desc = note
        }
        categories = append(categories, decision.Option{Name: leaf.Key, Desc: desc})
    }
    categories = append(categories, decision.Option{Name: NoneOption, Desc: noneDesc})

    regions := []decision.Option{}
    for _, r := range catalog.Regions {
        regions = append(regions, decision.Option{Name: r.ID, Desc: r.Label})
    }
    regions = append(regions, decision.Option{Name: NoneOption, Desc: noneDesc})

    questions := map[string]decision.Question{
        QuestionCategory: {
            Kind:         decision.KindChoice,
            Instructions: "Яку категорію нерухомості на OLX шукає користувач (купівля, довгострокова чи подобова оренда; квартира, будинок, кімната тощо)? Оренда без слів «подобово», «погодинно», «на добу» чи «посуточно» — це довгострокова оренда.",
            Options:      categories,
        },
        QuestionRegion: {
            Kind:         decision.KindChoice,
            Instructions: "У якій області України користувач шукає нерухомість? Якщо область не випливає із запиту, обери \"none\".",
            Options:      regions,
        },
    }
    if len(cities) > 0 {
        options := []decision.Option{}
        for _, c := range cities {
            options = append(options, decision.Option{Name: c.CityID, Desc: c.Label})
        }
        options = append(options, decision.Option{Name: NoneOption, Desc: noneDesc})
        questions[QuestionCity] = decision.Question{
            Kind:         decision.KindChoice,
            Instructions: "У якому населеному пункті користувач шукає нерухомість? Якщо жоден варіант не згаданий у запиті, обери \"none\".",
            Options:      options,
        }
        questions[QuestionPlace] = decision.Question{
            Kind:         decision.KindNoul,
            Instructions: "Чи називає користувач конкретний населений пункт або область в Україні, де шукати нерухомість? Так — лише якщо в запиті є назва міста, селища, села чи області. Країна, «море», «центр», «за містом» — ні.",
        }
    }
    return questions
}

// skipInStage2 lists elements stage 2 never asks about: location is settled
// in stage 1 and the search name is not part of the URL.
var skipInStage2 = map[string]bool{"region": true, "city": true, "search_name": true}

// applicableElements walks the order tree with the stage-1 values and returns
// the elements stage 2 should resolve, in wizard order.
func applicableElements(bfc *botform.BotFormComposer, values map[string]string) ([]pendingElement, error) {
    seq, err := olxform.Walk(bfc, values)
    if err != nil {
        return nil, err
    }
    state := botform.NewState(values, botform.BfcKey{"root"})

    elements := []pendingElement{}
    for _, key := range seq {
        if _, set := values[key]; set || skipInStage2[key] || strings.HasPrefix(key, "category_") {
            continue
        }
        el, ok := bfc.GetElement(key)
        if !ok {
            continue
        }
        switch r := el.Renderer.(type) {
        case botform.BotTextKeyboardControl:
            if strings.HasPrefix(key, "filter_float_") {
                elements = append(elements, pendingElement{Key: key, Title: r.Title, Kind: kindNumber})
            }
        case botform.BotMultiChoiceKeyboardControl:
            options, err := bfc.GetChoicesByPath(state, el.DataPath, true)
            if err != nil || len(options) == 0 {
                continue
            }
            separator := r.ValuesSeparator
            if separator == "" {
                separator = ";"
            }
            elements = append(elements, pendingElement{Key: key, Title: r.Title, Kind: kindMulti, Options: options, Separator: separator})
        case botform.BotChoiceKeyboardControl:
            options, err := bfc.GetChoicesByPath(state, el.DataPath, true)
            if err != nil {
                continue
            }
            kind := kindChoice
            if strings.HasPrefix(key, "filter_bool_") {
                // a yes/no condition: one noul for «1». "0" means "all ads"
                // (same as not asking) and olxurl.Build would turn any
                // non-empty bool value into an active filter.
                if options["1"] == "" {
                    continue
                }
                options = map[string]string{"1": options["1"]}
                kind = kindBool
            }
            if len(options) == 0 {
                continue
            }
            elements = append(elements, pendingElement{Key: key, Title: r.Title, Kind: kind, Options: options})
        }
    }
    return elements, nil
}

// stage2Questions builds one choice per choice/number element, one noul per
// bool element and one gate noul per multi-choice element; the options of a
// multi-choice element (multiOptionQuestions) wait for its gate. Number
// questions are only asked when the query contains numbers.
func stage2Questions(elements []pendingElement, numbers []NumberMention) map[string]decision.Question {
    questions := map[string]decision.Question{}
    for _, el := range elements {
        switch el.Kind {
        case kindChoice:
            options := []decision.Option{}
            for _, key := range sortedKeys(el.Options) {
                options = append(options, decision.Option{Name: key, Desc: el.Options[key]})
            }
            options = append(options, decision.Option{Name: NoneOption, Desc: noneDesc})
            questions[el.Key] = decision.Question{
                Kind:         decision.KindChoice,
                Instructions: fmt.Sprintf("%s%s: який варіант відповідає запиту користувача? Якщо в запиті про це не сказано, обери \"none\".", el.Title, familyHint(el.Key)),
                Options:      options,
            }
        case kindBool:
            questions[el.Key] = decision.Question{
                Kind:         decision.KindNoul,
                Instructions: fmt.Sprintf("%s%s: чи шукає користувач лише оголошення з умовою «%s»? Так — лише якщо запит прямо або однозначно на це вказує.", el.Title, familyHint(el.Key), el.Options["1"]),
            }
        case kindMulti:
            questions[gateKey(el.Key)] = gateQuestion(el)
        case kindNumber:
            if len(numbers) == 0 {
                continue
            }
            options := []decision.Option{}
            for _, m := range numbers {
                options = append(options, decision.Option{Name: m.Name(), Desc: "«" + m.Raw + "»"})
            }
            options = append(options, decision.Option{Name: NoneOption, Desc: noneDesc})
            questions[el.Key] = decision.Question{
                Kind:         decision.KindChoice,
                Instructions: fmt.Sprintf("Поле «%s»%s (%s): яке число із запиту користувача є значенням цього поля? Якщо жодне, обери \"none\".", el.Title, familyHint(el.Key), boundHint(el.Key)),
                Options:      options,
            }
        }
    }
    return questions
}

// multiOptionQuestions builds one noul per option of a multi-choice element,
// plus its option group's noul. Stage 3 asks them once the element's gate
// passed.
func multiOptionQuestions(el pendingElement) map[string]decision.Question {
    questions := map[string]decision.Question{}
    g, grouped := elementGroup(el)
    for _, key := range sortedKeys(el.Options) {
        instructions := fmt.Sprintf("%s: чи підходить користувачу варіант «%s»? Так — лише якщо запит прямо або однозначно на це вказує.", el.Title, el.Options[key])
        if grouped && slices.Contains(g.Options, key) {
            instructions += g.Specific
        }
        questions[multiKey(el.Key, key)] = decision.Question{Kind: decision.KindNoul, Instructions: instructions}
    }
    if grouped {
        questions[multiKey(el.Key, groupOption)] = decision.Question{Kind: decision.KindNoul, Instructions: g.Question}
    }
    return questions
}

// gateKey is the question key of a multi-choice element's gate.
func gateKey(element string) string {
    return "gate:" + element
}

// gateQuestion asks whether the query says anything about a multi-choice
// element at all. It is deliberately loose (a wrong yes costs a stage-3
// question, a wrong no loses the filter) and lists the options: with the
// title alone wanted fields scored 0.35–0.44 and nearly every gate passed.
// Golden ×2 (2026-10-17): wanted fields 0.85–0.99, untouched ones 0.06–0.10;
// 9% of gates passed and stage 2+3 fell from 8.7k to 4.4k tokens per query.
func gateQuestion(el pendingElement) decision.Question {
    labels := []string{}
    for _, key := range sortedKeys(el.Options) {
        labels = append(labels, el.Options[key])
    }
    return decision.Question{
        Kind: decision.KindNoul,
        Instructions: fmt.Sprintf("%s (варіанти: %s): чи каже запит хоч щось про цю характеристику — будь-який із варіантів або щось близьке за змістом, навіть непрямо? Ні — лише якщо запит про це зовсім не згадує.",
            el.Title, strings.Join(labels, "; ")),
    }
}

// groupOption is the multiKey option name of an element's option group.
const groupOption = "any"

// optionGroup is one extra noul for a multi-choice element whose options are
// too specific for a general request: «з питомцем» names no species, so no
// pet option is a clear yes (live: cat 0.38, dogs 0.16–0.19). When no
// specific option is accepted, an accepted group selects all its options
// (OLX ORs the values of a multi-choice filter).
type optionGroup struct {
    Label    string   // the field label when the group is selected
    Question string   // the noul's instructions
    Options  []string // the options it selects, sorted
    // Specific is appended to each option's own question, so a general
    // request is left to the group (live: «з питомцем» put «котик» at
    // 0.38–0.41, and a cat then beat the any-pet group).
    Specific string
}

var optionGroups = map[string]optionGroup{
    "filter_enum_pets": {
        Label: "Так, будь-яка тваринка",
        Question: "Домашні улюбленці: чи хоче користувач житло, де можна жити з домашньою твариною, не уточнюючи з якою " +
            "(напр. «з питомцем», «з тваринкою», «можна з тваринами», «pet friendly»)? Так — якщо запит про це каже, навіть без виду тварини.",
        Options:  []string{"yes_big_dog", "yes_cat", "yes_medium_dog", "yes_other", "yes_small_dog"},
        Specific: " Лише якщо запит називає саму тварину (кіт, собака тощо); загальне «з питомцем», «з тваринкою», «з тваринами» — ні.",
    },
}

// elementGroup returns the element's option group when the element offers
// every option of it.
func elementGroup(el pendingElement) (optionGroup, bool) {
    g, ok := optionGroups[el.Key]
    if !ok {
        return optionGroup{}, false
    }
    for _, o := range g.Options {
        if _, offered := el.Options[o]; !offered {
            return optionGroup{}, false
        }
    }
    return g, true
}

// multiKey is the question key for one option of a multi-choice element.
func multiKey(element, option string) string {
    return element + "#" + option
}

// familyHints describe what a numeric field measures; YAML titles alone
// («Поверх» vs «Поверховість») are too alike for Jev to tell apart.
var familyHints = map[string]string{
    "filter_bool_eoselia": "єОселя — державна програма пільгової іпотеки; назву пишуть по-різному і з помилками: " +
        "«є-Оселя», «єоселя», «еоселя», «эоселя», «по єОселі», «під єОселю»; будь-яка згадка програми — варіант «1», " +
        "а звичайне слово «оселя» (житло) — ні",
    "filter_float_price":           "ціна оголошення",
    "filter_float_floor":           "на якому поверсі розташоване житло, напр. «на 3 поверсі», «не нижче 2 поверху»",
    "filter_float_total_floors":    "скільки поверхів у будинку загалом, напр. «9-поверховий будинок», «у п'ятиповерхівці»",
    "filter_float_total_area":      "загальна площа житла чи приміщення, м²",
    "filter_float_land_area":       "площа земельної ділянки",
    "filter_float_number_of_rooms": "кількість кімнат",
}

// familyHint returns " — <description>" for known numeric families, or "".
func familyHint(key string) string {
    if hint, ok := familyHints[family(key)]; ok {
        return " — " + hint
    }
    return ""
}

func boundHint(key string) string {
    switch {
    case key == "filter_float_price:from":
        return "нижня межа, «від»; лише якщо в запиті прямо сказано «від», «не менше», «дорожче за»"
    case key == "filter_float_price:to":
        return "верхня межа, «до»; ціна без «від» чи «до» («за 10к», «10000 грн») — це бюджет, тобто теж верхня межа"
    case strings.HasSuffix(key, ":from"):
        return "нижня межа, «від»"
    case strings.HasSuffix(key, ":to"):
        return "верхня межа, «до»"
    }
    return "точне значення"
}

func sortedKeys(m map[string]string) []string {
    keys := make([]string, 0, len(m))
    for k := range m {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    return keys
}
