package request

import (
    "context"
    "crypto/tls"
    "github.com/parnurzeal/gorequest"
    "sync"
)

type Local struct {
    Gor    *gorequest.SuperAgent
    GorMtx sync.Mutex
}

func NewLocalRequest() *Local {
    pc := &Local{Gor: gorequest.New()}
    return pc
}

func (r *Local) Q(_ context.Context, requestUrl string, params ...string) (interface{}, string, []error) {
    reps := 3

onemore:

    r.GorMtx.Lock()
    gor := CompileParams(r.Gor.Get(requestUrl).TLSClientConfig(&tls.Config{InsecureSkipVerify: true}), params...)
    rsp, b, e := gor.End()
    r.GorMtx.Unlock()

    if reps > 0 && (len(e) > 0 || rsp.StatusCode > 400) {
        reps--
        goto onemore
    } else {
        return rsp, b, e
    }
}
