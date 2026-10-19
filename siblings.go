package olxjev

import "strings"

// MinSiblingProbability is the Jev probability a near-duplicate city needs to
// be offered as an alternative to the accepted one.
const MinSiblingProbability = 0.2

// citySiblings returns the shortlisted cities that may be the same place as
// the accepted one under another spelling. OLX lists such pairs in one region
// («Яремче» / «Яремча»); city is single-choice, so they become
// Result.CityAlternatives and the user picks a URL. Name similarity alone is far too loose — 2 429
// same-region pairs differ by one letter, mostly distinct villages
// (Гаївка / Раївка) — so Jev must also give the sibling real probability.
// A city with districts is never doubled: same-name villages next to it
// («Запоріжжя» 194 vs village 25423) are other places.
func citySiblings(accepted CityCandidate, cities []CityCandidate, probabilities map[string]float64) []CityCandidate {
    siblings := []CityCandidate{}
    if accepted.HasDistricts {
        return siblings
    }
    acceptedName := []rune(foldReplacer.Replace(strings.ToLower(accepted.Name)))
    for _, c := range cities {
        if c.CityID == accepted.CityID || c.RegionID != accepted.RegionID {
            continue
        }
        if probabilities[c.CityID] < MinSiblingProbability {
            continue
        }
        if levenshtein(acceptedName, []rune(foldReplacer.Replace(strings.ToLower(c.Name)))) > 1 {
            continue
        }
        siblings = append(siblings, c)
    }
    return siblings
}
