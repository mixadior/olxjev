package olxurl

import (
    "context"
    "encoding/json"
    "fmt"
    "net/url"
    "strings"

    "github.com/mixadior/olxjev/request"
)

func Build(ctx context.Context, params map[string]string, rq request.Request) (string, error) {
    // detect final category_id

    categoryV := params["category_1"]
    maxCycles := 20
    for {
        maxCycles--
        categoryVV, ok := params[categoryV]
        if ok {
            categoryV = categoryVV
        } else {
            break
        }

        if maxCycles <= 0 {
            return "", fmt.Errorf("infinite category %s", categoryV)
        }
    }

    regionId := params["region"]
    cityId := params["city"]
    finalCategoryId := strings.TrimPrefix(categoryV, "category_")

    _, b, errs := rq.Q(ctx, fmt.Sprintf("https://www.olx.ua/api/v1/offers/metadata/breadcrumbs/?category_id=%s&region_id=%s&city_id=%s", finalCategoryId, regionId, cityId))

    if len(errs) > 0 {
        return "", errs[0]
    }

    var seoUrl struct {
        Data struct {
            Breadcrumbs []struct {
                Href string
            }
        }
    }

    err := json.Unmarshal([]byte(b), &seoUrl)
    if err != nil {
        return "", err
    }
    seoUrlLast := seoUrl.Data.Breadcrumbs[len(seoUrl.Data.Breadcrumbs)-1]

    urlParsed, _ := url.Parse("https://www.olx.ua/")
    urlParsed.Path = seoUrlLast.Href

    urlQ := urlParsed.Query()
    // search[district_id]
    districtId, ok := params["district"]
    if ok {
        urlQ.Set("search[district_id]", districtId)
    }

    // rewrite boolean filters

    for paramKey, paramValue := range params {
        if strings.HasPrefix(paramKey, "filter_float_") {
            // from & to values
            urlQ.Set(fmt.Sprintf("search[%s]", paramKey), paramValue)
        } else if strings.HasPrefix(paramKey, "filter_bool_") && len(paramValue) > 0 {
            tmpKeyName := strings.TrimPrefix(paramKey, "filter_bool_")
            urlQ.Set(fmt.Sprintf("search[filter_enum_%s][0]", tmpKeyName), "1")
        } else if strings.HasPrefix(paramKey, "filter_enum_") {
            splittedValues := strings.Split(paramValue, ";")
            for k, v := range splittedValues {
                urlQ.Set(fmt.Sprintf("search[%s][%d]", paramKey, k), v)
            }
        }
    }
    urlParsed.RawQuery = urlQ.Encode()
    // decode multiple(enum) filters

    /**
    https://www.olx.ua/d/uk/nedvizhimost/doma/prodazha-domov/kharkov/?currency=UAH&search%5Bfilter_float_total_floors:from%5D=2&search%5Bfilter_enum_repair%5D%5B0%5D=1&search%5Bfilter_enum_repair%5D%5B1%5D=2&search%5Bfilter_enum_repair%5D%5B2%5D=3
    */
    return urlParsed.String(), nil
}
