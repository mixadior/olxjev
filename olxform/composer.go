// Package olxform builds the bfc form composer for the OLX.ua real-estate
// wizard (/olxwizard) from the embedded YAML resources produced by
// cmd/olxtoolkit in feodosian.com/olxsearch (see CLAUDE.md).
package olxform

import (
    _ "embed"
    "errors"
    "fmt"

    botform "github.com/mixadior/bfc"
    "gopkg.in/yaml.v2"
)

//go:embed resources/out_data.yml
var dataYaml []byte

//go:embed resources/out_elements_filters.yml
var elementsYaml []byte

//go:embed resources/out_data_order_tree.yml
var orderTreeYaml []byte

// NewComposer unmarshals the embedded YAMLs and returns the composer used by
// /olxwizard. It takes about a second; build it once and share it — the
// composer is read-only and safe for concurrent use.
func NewComposer() (*botform.BotFormComposer, error) {
    data := &botform.BotFormDataNode{}
    if err := yaml.Unmarshal(dataYaml, data); err != nil {
        return nil, fmt.Errorf("olxform: data: %w", err)
    }

    elements := map[string]*botform.BotFormElementNode{}
    if err := yaml.Unmarshal(elementsYaml, &elements); err != nil {
        return nil, fmt.Errorf("olxform: elements: %w", err)
    }

    cityOrder := &botform.BotFormDataNode{}
    if err := yaml.Unmarshal(orderTreeYaml, cityOrder); err != nil {
        return nil, fmt.Errorf("olxform: order tree: %w", err)
    }

    elements["region"] = &botform.BotFormElementNode{
        Renderer: botform.BotChoiceKeyboardControl{
            Title:         "Область",
            ButtonsPerRow: 2,
        },
        DataPath: []string{"region"},
    }
    elements["city"] = &botform.BotFormElementNode{
        Renderer: botform.BotChoiceKeyboardControl{
            Type:          "BotMultiChoiceKeyboardControl",
            Title:         "Місто",
            ButtonsPerRow: 2,
        },
        DataPath: []string{"region", "city"},
    }
    elements["district"] = &botform.BotFormElementNode{
        Renderer: botform.BotChoiceKeyboardControl{
            Title:         "Район міста",
            ButtonsPerRow: 2,
        },
        DataPath: []string{"region", "city", "district"},
    }

    currency, ok := elements["currency"]
    if !ok {
        return nil, errors.New("olxform: elements: currency element is missing")
    }
    currency.Required = true

    return &botform.BotFormComposer{
        OrderTree: &botform.BotFormDataNode{Children: map[string]*botform.BotFormDataNode{
            "*": {
                Name: "region",
                Children: map[string]*botform.BotFormDataNode{
                    "*": cityOrder,
                }},
        }, Name: "root"},
        Data:     data,
        Elements: elements,
    }, nil
}
