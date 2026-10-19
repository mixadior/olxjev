package olxjev

import (
    "errors"
    "sort"
    "strings"

    botform "github.com/mixadior/bfc"
)

// Leaf is one selectable category: a leaf of the category tree.
type Leaf struct {
    Key   string   // leaf key, e.g. "category_1760"
    Label string   // "Нерухомість › Квартири › Довгострокова оренда квартир"
    Chain []string // keys from "category_1" down to Key
}

// Values returns the bfc values selecting this leaf, in the shape
// olxurl.Build walks: category_1=category_1757, category_1757=category_1760.
func (l Leaf) Values() map[string]string {
    values := map[string]string{}
    for i := 0; i+1 < len(l.Chain); i++ {
        values[l.Chain[i]] = l.Chain[i+1]
    }
    return values
}

// Region is one top-level location option.
type Region struct {
    ID    string
    Label string
}

// Catalog holds the closed option lists asked in stage 1.
type Catalog struct {
    Leaves  []Leaf   // sorted by Label
    Regions []Region // sorted by Label

    leafByKey   map[string]Leaf
    regionLabel map[string]string
}

// NewCatalog flattens the category tree and region list of the bfc data.
func NewCatalog(data *botform.BotFormDataNode) (*Catalog, error) {
    root, ok := data.Children["category_1"]
    if !ok {
        return nil, errors.New("olxjev: data has no category_1")
    }
    regions, ok := data.Children["region"]
    if !ok {
        return nil, errors.New("olxjev: data has no region")
    }

    c := &Catalog{leafByKey: map[string]Leaf{}, regionLabel: map[string]string{}}
    var walk func(node *botform.BotFormDataNode, chain, labels []string)
    walk = func(node *botform.BotFormDataNode, chain, labels []string) {
        if len(node.Children) == 0 {
            leaf := Leaf{
                Key:   chain[len(chain)-1],
                Label: strings.Join(labels, " › "),
                Chain: chain,
            }
            c.Leaves = append(c.Leaves, leaf)
            c.leafByKey[leaf.Key] = leaf
            return
        }
        for key, child := range node.Children {
            walk(child, append(append([]string{}, chain...), key), append(append([]string{}, labels...), child.Value))
        }
    }
    walk(root, []string{"category_1"}, []string{root.Value})
    sort.Slice(c.Leaves, func(i, j int) bool { return c.Leaves[i].Label < c.Leaves[j].Label })

    for id, region := range regions.Children {
        c.Regions = append(c.Regions, Region{ID: id, Label: region.Value})
        c.regionLabel[id] = region.Value
    }
    sort.Slice(c.Regions, func(i, j int) bool { return c.Regions[i].Label < c.Regions[j].Label })
    return c, nil
}

// Leaf returns the leaf with the given key.
func (c *Catalog) Leaf(key string) (Leaf, bool) {
    leaf, ok := c.leafByKey[key]
    return leaf, ok
}

// RegionLabel returns the region's label, or "" for an unknown id.
func (c *Catalog) RegionLabel(id string) string {
    return c.regionLabel[id]
}

// LeafOf follows the category chain in values the way olxurl.Build does and
// returns the final category key, or "" when no category is set.
func (c *Catalog) LeafOf(values map[string]string) string {
    key := values["category_1"]
    for i := 0; i < 20 && key != ""; i++ {
        next, ok := values[key]
        if !ok {
            return key
        }
        key = next
    }
    return key
}
