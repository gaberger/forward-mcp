package forward

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// authHeader returns the Authorization header for the configured Forward
// API auth type (config.ForwardConfig.AuthType):
//
//	basic  Basic base64(APIKey:APISecret), built in a zeroed buffer
//	token  "<APITokenScheme or Bearer> APIToken"
//	jwt    Bearer <JWT>, from APIJWTFile (re-read when it changes, so a
//	       sidecar or tokend can rotate it) or APIJWT; an expired JWT is
//	       refused before any request is sent
//
// Errors name the configuration, never the credential.
func (c *Client) authHeader() (string, error) {
	switch c.config.EffectiveAuthType() {
	case "basic":
		if c.config.APIKey == "" || c.config.APISecret == "" {
			return "", fmt.Errorf("Forward API credentials missing: set FORWARD_API_KEY and FORWARD_API_SECRET")
		}
		return c.makeSecureAuthHeader()
	case "token":
		if c.config.APIToken == "" {
			return "", fmt.Errorf("Forward API token missing: set FORWARD_API_TOKEN")
		}
		scheme := strings.TrimSpace(c.config.APITokenScheme)
		if scheme == "" {
			scheme = "Bearer"
		}
		return scheme + " " + c.config.APIToken, nil
	case "jwt":
		tok, err := c.currentJWT()
		if err != nil {
			return "", err
		}
		return "Bearer " + tok, nil
	default:
		return "", fmt.Errorf("unsupported FORWARD_API_AUTH_TYPE %q (basic|token|jwt)", c.config.AuthType)
	}
}

type jwtFileCache struct {
	mu    sync.Mutex
	token string
	mtime time.Time
	size  int64
}

func (c *Client) currentJWT() (string, error) {
	tok := c.config.APIJWT
	if path := c.config.APIJWTFile; path != "" {
		st, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("Forward API JWT file (FORWARD_API_JWT_FILE): %w", err)
		}
		c.jwt.mu.Lock()
		if c.jwt.token == "" || !st.ModTime().Equal(c.jwt.mtime) || st.Size() != c.jwt.size {
			b, err := os.ReadFile(path)
			if err != nil {
				c.jwt.mu.Unlock()
				return "", fmt.Errorf("Forward API JWT file (FORWARD_API_JWT_FILE): %w", err)
			}
			c.jwt.token, c.jwt.mtime, c.jwt.size = strings.TrimSpace(string(b)), st.ModTime(), st.Size()
		}
		tok = c.jwt.token
		c.jwt.mu.Unlock()
	}
	if tok == "" {
		return "", errors.New("Forward API JWT missing: set FORWARD_API_JWT_FILE or FORWARD_API_JWT")
	}
	if exp, ok := jwtExpiry(tok); ok && time.Now().After(exp) {
		return "", fmt.Errorf("Forward API JWT expired at %s; refresh FORWARD_API_JWT_FILE", exp.UTC().Format(time.RFC3339))
	}
	return tok, nil
}

// jwtExpiry reads the exp claim without verifying the token: forward-mcp
// is the bearer here, not the verifier, so this is only an early, clear
// error instead of a 401 from Forward.
func jwtExpiry(tok string) (time.Time, bool) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp *int64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Exp == nil {
		return time.Time{}, false
	}
	return time.Unix(*claims.Exp, 0), true
}

// permanentError stops retryWithBackoff: retrying a missing or expired
// credential cannot succeed.
type permanentError struct{ err error }

func (p permanentError) Error() string { return p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

func backoffPermanent(err error) error { return permanentError{err: err} }
