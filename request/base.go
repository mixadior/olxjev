package request

import (
    "context"
    "encoding/json"
    "github.com/parnurzeal/gorequest"
    "net/http"
    "strconv"
    "strings"
    "time"
)

const PARAM_SKIP_REDIRECT = "skip_redirect"

type Request interface {
    Q(ctx context.Context, requestUrl string, params ...string) (interface{}, string, []error)
}

func CompileParams(gor *gorequest.SuperAgent, params ...string) *gorequest.SuperAgent {
    for _, p := range params {
        if p == PARAM_SKIP_REDIRECT {
            gor = gor.RedirectPolicy(func(req gorequest.Request, via []gorequest.Request) error {
                return http.ErrUseLastResponse
            })
        } else if strings.HasPrefix(p, "headers_json:") {
            headersRawJson := strings.TrimPrefix(p, "headers_json:")
            headersJson := map[string]string{}
            json.Unmarshal([]byte(headersRawJson), &headersJson)
            for header, headerValue := range headersJson {
                gor.AppendHeader(header, headerValue)
            }
        } else if p == "skip_reqirect_except_301" {
            gor = gor.RedirectPolicy(func(req gorequest.Request, via []gorequest.Request) error {
                if req.Response.StatusCode == 301 {
                    return nil
                }
                return http.ErrUseLastResponse
            })
        } else if strings.HasPrefix(p, "timeout:") {
            timeoutRaw := strings.TrimPrefix(p, "timeout:")
            parsedTimeout, e := strconv.ParseInt(timeoutRaw, 10, 64)
            if e == nil {
                gor = gor.Timeout(time.Duration(parsedTimeout) * time.Second)
            }
        } else if strings.HasPrefix(p, "post_json:") {
            postJson := strings.TrimPrefix(p, "post_json:")
            gor.DoNotClearSuperAgent = true
            gor.Post(gor.Url).Send(postJson)
            gor.DoNotClearSuperAgent = false
        }
    }
    return gor
}
