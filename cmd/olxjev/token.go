package main

import (
    "crypto/hmac"
    "crypto/rand"
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "strconv"
    "strings"
    "time"

    "github.com/rs/zerolog"
)

// The web UI's own endpoints (/result, /api/resolve) answer only the page's
// script: the page carries a signed, expiring token that the script sends
// back in tokenHeader, and a browser's cross-site request is refused (Fetch
// Metadata). This keeps other sites and casual scripts off the model credits;
// it is not authentication — a client that loads the page first can read the
// token, so a public server still wants a rate limit in front.

// secretEnv names the key page tokens are signed with. Give every replica
// behind one address the same value; unset, each process draws its own and
// the pages it served lose the API on restart (the script then reloads).
const secretEnv = "OLXJEV_SECRET"

// minSecretLen is the shortest secretEnv accepted.
const minSecretLen = 16

// tokenTTL is how long a page's token opens the API.
const tokenTTL = 12 * time.Hour

// tokenHeader carries the page's token.
const tokenHeader = "X-OlxJev-Token"

// tokenSecret is secretEnv's value, or a random key when it is unset.
func tokenSecret(env string, log zerolog.Logger) ([]byte, error) {
    if env != "" {
        if len(env) < minSecretLen {
            return nil, fmt.Errorf("%s must be at least %d characters", secretEnv, minSecretLen)
        }
        return []byte(env), nil
    }
    key := make([]byte, 32)
    if _, err := rand.Read(key); err != nil {
        return nil, err
    }
    log.Info().Msgf("no %s: page tokens are signed with a random key and stop working on restart", secretEnv)
    return key, nil
}

// pageTokens issues and checks page tokens: "<expiry unix>.<HMAC-SHA256>".
type pageTokens struct {
    secret []byte
    now    func() time.Time
}

func (p *pageTokens) issue() string {
    exp := strconv.FormatInt(p.now().Add(tokenTTL).Unix(), 10)
    return exp + "." + p.sign(exp)
}

func (p *pageTokens) valid(token string) bool {
    exp, sig, ok := strings.Cut(token, ".")
    if !ok {
        return false
    }
    unix, err := strconv.ParseInt(exp, 10, 64)
    if err != nil || p.now().Unix() > unix {
        return false
    }
    return hmac.Equal([]byte(sig), []byte(p.sign(exp)))
}

func (p *pageTokens) sign(exp string) string {
    mac := hmac.New(sha256.New, p.secret)
    mac.Write([]byte("olxjev page\x00" + exp))
    return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// fromPage says why r is not the page's script calling; nil when it is, or
// when the web runs without tokens (tests of the handlers alone).
func (s *web) fromPage(r *http.Request) error {
    if s.tokens == nil {
        return nil
    }
    if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
        return errors.New("cross-site requests are not allowed")
    }
    if !s.tokens.valid(r.Header.Get(tokenHeader)) {
        return errors.New("this endpoint serves the olxjev web page: the page token is missing or expired, reload the page")
    }
    return nil
}

// pageOnly lets through only the page's own script (fromPage); anything else
// is a 403, as JSON for the API.
func (s *web) pageOnly(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        err := s.fromPage(r)
        if err == nil {
            next(w, r)
            return
        }
        s.log.Info().Str("path", r.URL.Path).Str("remote", r.RemoteAddr).Str("reason", err.Error()).Msg("refused a request from outside the page")
        w.Header().Set("Cache-Control", "no-store")
        if strings.HasPrefix(r.URL.Path, "/api/") {
            w.Header().Set("Content-Type", "application/json; charset=utf-8")
            w.WriteHeader(http.StatusForbidden)
            json.NewEncoder(w).Encode(output{Error: err.Error()})
            return
        }
        http.Error(w, err.Error(), http.StatusForbidden)
    }
}
