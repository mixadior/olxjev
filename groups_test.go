package olxjev

import (
    "context"
    "strings"
    "testing"

    "github.com/mixadior/olxjev/decision"
)

var allPets = "yes_big_dog;yes_cat;yes_medium_dog;yes_other;yes_small_dog"

func petsElement(t *testing.T) pendingElement {
    t.Helper()
    elements, err := applicableElements(testComposer(t), map[string]string{
        "region": "8", "city": "280", "category_1": "category_1757", "category_1757": "category_1760",
    })
    if err != nil {
        t.Fatalf("applicableElements: %v", err)
    }
    for _, el := range elements {
        if el.Key == "filter_enum_pets" {
            return el
        }
    }
    t.Fatal("no filter_enum_pets element")
    return pendingElement{}
}

// «з питомцем» names no species: each species noul stays low (live: cat
// 0.38, dogs 0.16–0.19). A group noul asks about any pet.
func TestStage2PetsAsksAnyPet(t *testing.T) {
    qs := multiOptionQuestions(petsElement(t))
    q, ok := qs["filter_enum_pets#"+groupOption]
    if !ok || q.Kind != decision.KindNoul || !strings.Contains(q.Instructions, "«з питомцем»") {
        t.Fatalf("any-pet question = %+v, want a noul mentioning «з питомцем»", q)
    }
    rooms := applicableRooms(t)
    if _, asked := multiOptionQuestions(rooms)["filter_enum_number_of_rooms_string#"+groupOption]; asked {
        t.Error("only elements with an option group get the group question")
    }
}

// Live: «з питомцем» put «котик» at 0.38–0.41, around the cutoff, and a cat
// then beat the any-pet group. Species questions say a generic pet is no.
func TestStage2PetSpeciesExcludeGenericPet(t *testing.T) {
    qs := multiOptionQuestions(petsElement(t))
    cat := qs["filter_enum_pets#yes_cat"].Instructions
    if !strings.Contains(cat, "«з питомцем»") || !strings.Contains(cat, "— ні") {
        t.Errorf("cat question must rule out a generic pet: %s", cat)
    }
    rooms := multiOptionQuestions(applicableRooms(t))["filter_enum_number_of_rooms_string#odnokomnatnye"].Instructions
    if strings.Contains(rooms, "питомцем") {
        t.Errorf("only grouped elements get the hint: %s", rooms)
    }
}

func applicableRooms(t *testing.T) pendingElement {
    t.Helper()
    return pendingElement{Key: "filter_enum_number_of_rooms_string", Title: "Кількість кімнат", Kind: kindMulti,
        Options: map[string]string{"odnokomnatnye": "1 кімната"}, Separator: ";"}
}

// resolvePets resolves a pet query whose pets gate passes; stage3 answers
// the options.
func resolvePets(t *testing.T, stage3 map[string]*decision.Answer) *Result {
    t.Helper()
    f := &fakeDecider{answers: []map[string]*decision.Answer{flatRentKharkiv(), {gateKey("filter_enum_pets"): noul(0.9)}, stage3}}
    res, err := newTestResolver(t, f).Resolve(context.Background(), "зняти квартиру в Харкові з питомцем")
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    return res
}

func TestResolveAnyPetSelectsAllSpecies(t *testing.T) {
    res := resolvePets(t, map[string]*decision.Answer{
        "filter_enum_pets#yes_cat": noul(0.38),
        "filter_enum_pets#" + groupOption: noul(0.9),
    })
    if res.Values["filter_enum_pets"] != allPets {
        t.Errorf("pets = %q, want every species %q", res.Values["filter_enum_pets"], allPets)
    }
    if f := res.Fields["filter_enum_pets"]; f.Label != "Так, будь-яка тваринка" || f.Probability != 0.9 {
        t.Errorf("field = %+v, want the group label and its probability", f)
    }
}

// A named species wins over the group: «з котом» is a pet too.
func TestResolveSpecificPetWinsOverGroup(t *testing.T) {
    res := resolvePets(t, map[string]*decision.Answer{
        "filter_enum_pets#yes_cat":        noul(0.9),
        "filter_enum_pets#" + groupOption: noul(0.95),
    })
    if res.Values["filter_enum_pets"] != "yes_cat" {
        t.Errorf("pets = %q, want yes_cat only", res.Values["filter_enum_pets"])
    }
}

// When the expected value is the whole group, calibration grades the group
// noul, not each species (each is rightly low for «з питомцем»).
func TestCollectSamplesGradesGroup(t *testing.T) {
    questions := map[string]decision.Question{
        "filter_enum_pets#yes_cat":        {Kind: decision.KindNoul},
        "filter_enum_pets#" + groupOption: {Kind: decision.KindNoul},
    }
    answers := map[string]*decision.Answer{
        "filter_enum_pets#yes_cat":        noul(0.38),
        "filter_enum_pets#" + groupOption: noul(0.92),
    }
    got := collectSamples(map[string]string{"filter_enum_pets": allPets}, questions, answers)
    if len(got) != 1 || got[0] != (calSample{Type: "Multi", P: 0.92, Correct: true, Pick: "filter_enum_pets#" + groupOption}) {
        t.Errorf("samples = %+v, want only the group noul, right at 0.92", got)
    }
}

func TestResolveNoPetNoFilter(t *testing.T) {
    res := resolvePets(t, map[string]*decision.Answer{"filter_enum_pets#" + groupOption: noul(0.1)})
    if v, ok := res.Values["filter_enum_pets"]; ok {
        t.Errorf("pets = %q, want unset", v)
    }
}
