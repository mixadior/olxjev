package olxjev

import (
    "context"
    "net/url"
    "sort"
    "strings"

    "github.com/mixadior/olxjev/request"
    "github.com/mixadior/olxjev/olxurl"
)

// inURL lists the non-filter keys olxurl.Build turns into the URL.
var inURL = map[string]bool{"region": true, "city": true, "district": true}

// CityURL is one OLX search URL offered to the user.
type CityURL struct {
    City  string `json:"city,omitempty"`  // city id; "" for a region or nationwide search
    Label string `json:"label,omitempty"` // "Яремче, Івано-Франківська область"
    URL   string `json:"url"`
}

// SearchURLs returns the URL for res.Values first, then one per city in
// res.CityAlternatives (OLX lists some places twice: «Яремче» / «Яремча»), so
// the user can pick the right one. res is not modified.
func SearchURLs(ctx context.Context, res *Result, rq request.Request) ([]CityURL, []string, error) {
    primary, notInURL, err := SearchURL(ctx, res.Values, rq)
    if err != nil {
        return nil, nil, err
    }
    urls := []CityURL{{City: res.Values["city"], Label: res.Fields["city"].Label, URL: primary}}
    for _, alt := range res.CityAlternatives {
        values := make(map[string]string, len(res.Values))
        for k, v := range res.Values {
            values[k] = v
        }
        values["city"] = alt.Value
        delete(values, "district") // districts belong to the primary city
        u, _, err := SearchURL(ctx, values, rq)
        if err != nil {
            return nil, nil, err
        }
        urls = append(urls, CityURL{City: alt.Value, Label: alt.Label, URL: u})
    }
    return urls, notInURL, nil
}

// SearchURL builds the OLX search URL for values with olxurl.Build
// and adds the "currency" query parameter, which olxurl.Build does not emit
// (prices are otherwise read as UAH). It also returns the resolved keys that
// still do not reach the URL (e.g. owner_type), so callers can show them.
func SearchURL(ctx context.Context, values map[string]string, rq request.Request) (string, []string, error) {
    base, err := olxurl.Build(ctx, values, rq)
    if err != nil {
        return "", nil, err
    }
    u, err := url.Parse(base)
    if err != nil {
        return "", nil, err
    }
    if currency := values["currency"]; currency != "" {
        q := u.Query()
        q.Set("currency", currency)
        u.RawQuery = q.Encode()
    }

    notInURL := []string{}
    for key := range values {
        if inURL[key] || key == "currency" || strings.HasPrefix(key, "category_") || strings.HasPrefix(key, "filter_") {
            continue
        }
        notInURL = append(notInURL, key)
    }
    sort.Strings(notInURL)
    return u.String(), notInURL, nil
}
