package main

import (
    "bytes"
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "sync"
    "testing"

    "github.com/mixadior/olxjev/decision/typesafe"
)

// recordingServer is fakeJevServer remembering each request's model and
// Authorization header.
func recordingServer(t *testing.T) (*httptest.Server, func() (models, auths []string)) {
    t.Helper()
    var mu sync.Mutex
    var models, auths []string
    inner := fakeJevHandler(t, "category_1760")
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        body, _ := io.ReadAll(r.Body)
        var req struct {
            Model string `json:"model"`
        }
        json.Unmarshal(body, &req)
        mu.Lock()
        models, auths = append(models, req.Model), append(auths, r.Header.Get("Authorization"))
        mu.Unlock()
        r.Body = io.NopCloser(bytes.NewReader(body))
        inner(w, r)
    }))
    t.Cleanup(srv.Close)
    return srv, func() ([]string, []string) {
        mu.Lock()
        defer mu.Unlock()
        return append([]string(nil), models...), append([]string(nil), auths...)
    }
}

func env(kv map[string]string) func(string) string {
    return func(k string) string { return kv[k] }
}

// -via opencode routes only the typesafe backend: OpenCode's URL, its free
// Jev by default, OPENCODE_API_KEY (optional).
func TestBackendForVia(t *testing.T) {
    b, ok := backendFor(typesafe.Name, "opencode")
    if !ok || b.url != typesafe.OpenCodeURL || b.model != typesafe.OpenCodeModel || b.keyEnv != "OPENCODE_API_KEY" || !b.keyOptional {
        t.Errorf("typesafe via opencode = %+v, %v", b, ok)
    }
    if b, _ := backendFor(typesafe.Name, "typesafe"); b.url != typesafe.DefaultURL || b.keyEnv != "TYPESAFE_API_KEY" || b.keyOptional {
        t.Errorf("typesafe via typesafe = %+v", b)
    }
    if b, _ := backendFor("subq", "opencode"); b.keyEnv != "SUBQ_API_KEY" || b.model != "" {
        t.Errorf("subq via opencode = %+v; -via must leave subq alone", b)
    }
}

// The free Jev goes without a key and never sends TypeSafe's; a set
// OPENCODE_API_KEY is sent, and -model picks the paid jev-1.13.
func TestRunViaOpenCode(t *testing.T) {
    srv, seen := recordingServer(t)
    q := []string{"-via", "opencode", "-endpoint", srv.URL, "-no-url", "-q", "зняти квартиру в Харкові"}
    var stdout, stderr bytes.Buffer
    if code := run(q, nil, &stdout, &stderr, env(map[string]string{"TYPESAFE_API_KEY": "ts-key"})); code != 0 {
        t.Fatalf("exit %d: %s", code, stderr.String())
    }
    models, auths := seen()
    if models[0] != "jev-1.13-free" || auths[0] != "" {
        t.Errorf("keyless: model %q, auth %q; want jev-1.13-free and no Authorization", models[0], auths[0])
    }

    run(append([]string{"-model", "jev-1.13"}, q...), nil, &stdout, &stderr, env(map[string]string{"OPENCODE_API_KEY": "oc-key"}))
    models, auths = seen()
    if last := len(models) - 1; models[last] != "jev-1.13" || auths[last] != "Bearer oc-key" {
        t.Errorf("with key and -model: model %q, auth %q", models[last], auths[last])
    }
}

// OpenCode's free Jev needs no key: -via opencode starts without one (the
// listener fails before any call) and the web switch offers it.
func TestRunViaOpenCodeNeedsNoKey(t *testing.T) {
    var stderr bytes.Buffer
    run([]string{"-via", "opencode", "-serve", "127.0.0.1:-1"}, nil, io.Discard, &stderr, env(nil))
    if strings.Contains(stderr.String(), "is not set") || !strings.Contains(stderr.String(), `"backends":"typesafe via opencode (default)"`) {
        t.Errorf("stderr:\n%s", stderr.String())
    }

    srv := fakeJevServer(t)
    defer srv.Close()
    stderr.Reset()
    run([]string{"-backend", "subq", "-endpoint", srv.URL, "-via", "opencode", "-serve", "127.0.0.1:-1"}, nil, io.Discard, &stderr, env(nil))
    if !strings.Contains(stderr.String(), `"backends":"typesafe via opencode, subq (default)"`) {
        t.Errorf("the switch must offer typesafe via opencode without a key:\n%s", stderr.String())
    }
}

func TestRunUnknownVia(t *testing.T) {
    var stderr bytes.Buffer
    code := run([]string{"-via", "nope", "-q", "x"}, nil, io.Discard, &stderr, env(map[string]string{"TYPESAFE_API_KEY": "k"}))
    if code != 2 || !strings.Contains(stderr.String(), `unknown -via "nope" (typesafe or opencode)`) {
        t.Errorf("exit %d, stderr %q", code, stderr.String())
    }
}
