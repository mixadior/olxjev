package olxjev

import (
    "strings"
    "testing"

    "github.com/mixadior/olxjev/decision"
)

func optionNames(q decision.Question) []string {
    names := []string{}
    for _, o := range q.Options {
        names = append(names, o.Name)
    }
    return names
}

func TestStage1Questions(t *testing.T) {
    catalog, err := NewCatalog(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewCatalog: %v", err)
    }
    cities := []CityCandidate{{RegionID: "8", CityID: "280", Label: "Харків, Харківська область"}}
    qs := stage1Questions(catalog, cities)

    if got := len(optionNames(qs[QuestionCategory])); got != 42 {
        t.Errorf("category options = %d, want 41 leaves + none", got)
    }
    if got := len(optionNames(qs[QuestionRegion])); got != 26 {
        t.Errorf("region options = %d, want 25 + none", got)
    }
    if got := optionNames(qs[QuestionCity]); len(got) != 2 || got[0] != "280" || got[1] != NoneOption {
        t.Errorf("city options = %v", got)
    }

    if _, asked := stage1Questions(catalog, nil)[QuestionCity]; asked {
        t.Error("city question must be omitted for an empty shortlist")
    }
}

// Confusable categories carry structured descriptions (TypeSafe docs: what
// it is for, what belongs to the neighbour, examples); the rest keep their
// label. Live eval: «готель на морі» split between hotel stays and hotel sales.
func TestStage1ConfusableCategoriesDescribed(t *testing.T) {
    catalog, err := NewCatalog(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewCatalog: %v", err)
    }
    descs := map[string]any{}
    for _, o := range stage1Questions(catalog, nil)[QuestionCategory].Options {
        descs[o.Name] = o.Desc
    }
    pairs := map[string]string{ // leaf -> text its not_for must mention
        "category_3713": "Продаж готелів",
        "category_3684": "Житло подобово",
        "category_3715": "Готелі, бази відпочинку",
        "category_1760": "подобово",
        "category_3711": "довгострокова",
        "category_330":  "подобово",
        "category_3710": "довгострокова",
        "category_1756": "подобово",
        "category_3712": "довгострокова",
    }
    for leaf, mention := range pairs {
        d, ok := descs[leaf].(categoryDesc)
        if !ok {
            t.Errorf("%s: desc = %#v, want a categoryDesc", leaf, descs[leaf])
            continue
        }
        if label, _ := catalog.Leaf(leaf); d.Category != jevCategoryLabel(label.Label) || !strings.Contains(d.NotFor, mention) || len(d.Examples) == 0 {
            t.Errorf("%s: desc = %+v, want label, not_for mentioning %q and examples", leaf, d, mention)
        }
    }
    if label, _ := catalog.Leaf("category_21"); descs["category_21"] != jevCategoryLabel(label.Label) {
        t.Errorf("category_21: desc = %#v, want the plain label", descs["category_21"])
    }
}

// The category question sends Jev short paths (2026-10-17, golden ×2: 5102 →
// 3415 tokens, 62/62 both runs); results keep the full label.
func TestStage1CategoryLabelsShort(t *testing.T) {
    catalog, err := NewCatalog(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewCatalog: %v", err)
    }
    descs := map[string]any{}
    for _, o := range stage1Questions(catalog, nil)[QuestionCategory].Options {
        descs[o.Name] = o.Desc
    }
    want := map[string]string{
        "category_1758": "Квартири › Продаж квартир",
        "category_1522": "Нерухомість за кордоном",
        "category_3690": "Оренда офісів",
        "category_3684": "Продаж готелів, баз відпочинку",
        "category_3298": "Коворкінги",
        "category_3713": "Житло подобово › Готелі, бази відпочинку",
    }
    for leaf, label := range want {
        got := descs[leaf]
        if d, ok := got.(categoryDesc); ok {
            got = d.Category
        }
        if got != label {
            t.Errorf("%s: label sent to Jev = %#v, want %q", leaf, got, label)
        }
    }
    if leaf, _ := catalog.Leaf("category_3690"); leaf.Label != "Нерухомість › Комерційна нерухомість › Оренда комерційної нерухомості › Оренда офісів" {
        t.Errorf("catalog label changed: %q", leaf.Label)
    }
}

// Live eval: «зняти будинок» split 0.53/0.25 between long-term and daily rent.
func TestStage1CategoryDefaultsRentToLongTerm(t *testing.T) {
    catalog, err := NewCatalog(testComposer(t).Data)
    if err != nil {
        t.Fatalf("NewCatalog: %v", err)
    }
    instructions := stage1Questions(catalog, nil)[QuestionCategory].Instructions
    if !strings.Contains(instructions, "подобово") || !strings.Contains(instructions, "довгострокова") {
        t.Errorf("category instructions must default rent to long-term unless «подобово» is said: %q", instructions)
    }
}

// Live eval: «не вище 5 поверху» went to Поверховість because both titles
// read alike; number fields need a plain description of what they measure.
func TestStage2NumberQuestionsDescribeField(t *testing.T) {
    elements := []pendingElement{
        {Key: "filter_float_floor:to", Title: "Поверх , до", Kind: kindNumber},
        {Key: "filter_float_total_floors:to", Title: "Поверховість , до", Kind: kindNumber},
    }
    qs := stage2Questions(elements, ExtractNumbers("не вище 5 поверху"))
    floor := qs["filter_float_floor:to"].Instructions
    total := qs["filter_float_total_floors:to"].Instructions
    if !strings.Contains(floor, "на якому поверсі") {
        t.Errorf("floor instructions lack a description: %q", floor)
    }
    if !strings.Contains(total, "скільки поверхів") {
        t.Errorf("total floors instructions lack a description: %q", total)
    }
    // the live eval still tied the two fields sometimes; examples separate them
    if !strings.Contains(floor, "«на 3 поверсі»") || !strings.Contains(total, "«9-поверховий будинок»") {
        t.Errorf("floor fields need contrasting examples:\n%s\n%s", floor, total)
    }
}

// «у єсоеля» was missed: the question only repeated the filter title, and the
// programme's name gets written many ways.
func TestStage2EoseliaExplained(t *testing.T) {
    elements := []pendingElement{{
        Key: "filter_bool_eoselia", Title: "Підходить під вимоги єОселя", Kind: kindBool,
        Options: map[string]string{"1": "Підходить під вимоги єОселя"},
    }}
    instructions := stage2Questions(elements, nil)["filter_bool_eoselia"].Instructions
    for _, want := range []string{"пільгової іпотеки", "еоселя", "з помилками"} {
        if !strings.Contains(instructions, want) {
            t.Errorf("eoselia instructions lack %q: %s", want, instructions)
        }
    }
}

// On/off filters are yes/no conditions: one noul, high = the query asks for
// the filter (TypeSafe docs: Noul for yes/no, Choice for one-of-a-set).
func TestStage2BoolIsNoul(t *testing.T) {
    elements := []pendingElement{{
        Key: "filter_bool_commission", Title: "Без комісії", Kind: kindBool,
        Options: map[string]string{"1": "Без комісії"},
    }}
    q, ok := stage2Questions(elements, nil)["filter_bool_commission"]
    if !ok || q.Kind != decision.KindNoul || q.Options != nil {
        t.Fatalf("question = %+v, want a noul without criteria", q)
    }
    instructions := q.Instructions
    for _, want := range []string{"«Без комісії»", "Так —"} {
        if !strings.Contains(instructions, want) {
            t.Errorf("instructions lack %q: %s", want, instructions)
        }
    }
}

func TestApplicableElementsFlatRent(t *testing.T) {
    values := map[string]string{
        "region": "8", "city": "280",
        "category_1": "category_1757", "category_1757": "category_1760",
    }
    elements, err := applicableElements(testComposer(t), values)
    if err != nil {
        t.Fatalf("applicableElements: %v", err)
    }
    want := []struct {
        key  string
        kind elementKind
    }{
        {"district", kindChoice},
        {"currency", kindChoice},
        {"filter_float_price:from", kindNumber},
        {"filter_float_price:to", kindNumber},
        {"filter_bool_commission", kindBool},
        {"filter_float_floor:from", kindNumber},
        {"filter_float_floor:to", kindNumber},
        {"filter_float_total_floors:from", kindNumber},
        {"filter_float_total_floors:to", kindNumber},
        {"filter_float_total_area:from", kindNumber},
        {"filter_float_total_area:to", kindNumber},
        {"filter_enum_pets", kindMulti},
        {"filter_enum_blackout_autonomy", kindMulti},
        {"filter_enum_number_of_rooms_string", kindMulti},
        {"filter_enum_comfort", kindMulti},
        {"filter_enum_communications", kindMulti},
        {"filter_enum_infrastructure_500_m", kindMulti},
        {"filter_enum_inclusiveness", kindMulti},
        {"owner_type", kindMulti},
    }
    if len(elements) != len(want) {
        t.Fatalf("got %d elements, want %d: %+v", len(elements), len(want), elements)
    }
    for i, w := range want {
        if elements[i].Key != w.key || elements[i].Kind != w.kind {
            t.Errorf("element %d = %s/%d, want %s/%d", i, elements[i].Key, elements[i].Kind, w.key, w.kind)
        }
    }
    if n := len(elements[0].Options); n != 9 {
        t.Errorf("Kharkiv districts = %d, want 9", n)
    }
    commission := elements[4]
    if len(commission.Options) != 1 || commission.Options["1"] == "" {
        t.Errorf("bool options = %v, want only option 1", commission.Options)
    }
}

func TestApplicableElementsHouseRentNoCity(t *testing.T) {
    values := map[string]string{
        "region": "8",
        "category_1": "category_206", "category_206": "category_330",
    }
    elements, err := applicableElements(testComposer(t), values)
    if err != nil {
        t.Fatalf("applicableElements: %v", err)
    }
    keys := map[string]bool{}
    for _, el := range elements {
        keys[el.Key] = true
    }
    for _, k := range []string{"filter_float_land_area:from", "filter_float_number_of_rooms:to", "owner_type"} {
        if !keys[k] {
            t.Errorf("missing %s", k)
        }
    }
    for _, k := range []string{"district", "filter_enum_number_of_rooms_string", "search_name", "city"} {
        if keys[k] {
            t.Errorf("%s must not be asked", k)
        }
    }
}

func TestStage2Questions(t *testing.T) {
    elements := []pendingElement{
        {Key: "currency", Title: "Валюта", Kind: kindChoice, Options: map[string]string{"UAH": "грн.", "USD": "$"}},
        {Key: "filter_enum_pets", Title: "Домашні улюбленці", Kind: kindMulti, Options: map[string]string{"no": "Ні", "yes_cat": "Так, котик"}, Separator: ";"},
        {Key: "filter_float_price:to", Title: "Ціна , до", Kind: kindNumber},
    }
    numbers := ExtractNumbers("до 10к гривень")

    qs := stage2Questions(elements, numbers)
    if len(qs) != 3 {
        t.Fatalf("got %d questions, want 3: %v", len(qs), qs)
    }
    if got := optionNames(qs["currency"]); len(got) != 3 || got[2] != NoneOption {
        t.Errorf("currency options = %v", got)
    }
    if qs[gateKey("filter_enum_pets")].Kind != decision.KindNoul {
        t.Error("a multi-choice element is asked one gate noul in stage 2")
    }
    options := multiOptionQuestions(elements[1])
    if len(options) != 2 || options["filter_enum_pets#yes_cat"].Kind != decision.KindNoul || options["filter_enum_pets#no"].Kind != decision.KindNoul {
        t.Errorf("multi-choice options must be noul questions: %v", options)
    }
    if got := optionNames(qs["filter_float_price:to"]); len(got) != 2 || got[0] != "10000" {
        t.Errorf("price options = %v", got)
    }

    if _, asked := stage2Questions(elements, nil)["filter_float_price:to"]; asked {
        t.Error("number questions must not be asked when the query has no numbers")
    }
}
