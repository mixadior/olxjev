package olxform

import (
    "fmt"

    botform "github.com/mixadior/bfc"
)

const maxWalkSteps = 100

// Walk returns the element names the order tree visits for values, in wizard
// order, starting after "root". Elements without a value are walked through
// their "*" branch, so the result lists every element a user would be asked
// about for the given category and location (search_name included).
func Walk(bfc *botform.BotFormComposer, values map[string]string) ([]string, error) {
    state := botform.NewState(values, botform.BfcKey{"root"})
    key := botform.BfcKey{"root"}
    seq := []string{}
    for len(seq) < maxWalkSteps {
        next, ok := bfc.GetNext(state, key)
        if !ok || next == "" {
            return seq, nil
        }
        key = append(key, next)
        seq = append(seq, next)
    }
    return nil, fmt.Errorf("olxform: order tree walk exceeded %d steps", maxWalkSteps)
}
