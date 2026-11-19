// Package auth implements REWE's Keycloak OAuth flows (PKCE login, refresh) and
// token storage compatible with the Haskell implementation.
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/miraassist/rewe-cli/internal/config"
	"github.com/miraassist/rewe-cli/internal/rewe"
)

// The Keycloak realm can be overridden with KORB_AUTH_URL (e.g. for korb-fake).
func authEndpoint() string  { return realmURL() + "/auth" }
func tokenEndpoint() string { return realmURL() + "/token" }

func realmURL() string {
	if v := os.Getenv("KORB_AUTH_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://account.rewe.de/realms/sso/protocol/openid-connect"
}

const (
	clientID    = "reweios"
	redirectURI = "de.rewe.app://redirect"
	scope       = "openid email customer offline_access profile"
	// refresh this long before the access token actually expires
	expiryLeeway = 30 * time.Second
)

// ErrNotLoggedIn means no usable tokens are stored.
var ErrNotLoggedIn = errors.New("not logged in - run 'korb login'")

// Manager stores tokens and refreshes them when needed.
type Manager struct {
	Dir  string // token directory
	HTTP *rewe.Doer
}

// NewManager uses the default token directory.
func NewManager(d *rewe.Doer) (*Manager, error) {
	dir, err := config.TokenDir()
	if err != nil {
		return nil, err
	}
	return &Manager{Dir: dir, HTTP: d}, nil
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
}

type oauthError struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// lock takes an exclusive, cross-process lock on the token directory so that
// concurrent korb invocations never refresh (and thereby rotate) tokens twice.
func (m *Manager) lock() (unlock func(), err error) {
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(m.Dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("locking token directory: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func (m *Manager) read(name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(m.Dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotLoggedIn
	}
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", ErrNotLoggedIn
	}
	return s, nil
}

func (m *Manager) save(t tokenResponse) error {
	if t.AccessToken == "" {
		return errors.New("token response contained no access token")
	}
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(m.Dir, 0o700)
	if err := config.WriteFileAtomic(filepath.Join(m.Dir, "access_token"), []byte(t.AccessToken), 0o600); err != nil {
		return err
	}
	if t.RefreshToken == "" { // keep the existing refresh token
		return nil
	}
	return config.WriteFileAtomic(filepath.Join(m.Dir, "refresh_token"), []byte(t.RefreshToken), 0o600)
}

// AccessToken returns a valid access token, refreshing it if it expires soon.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	unlock, err := m.lock()
	if err != nil {
		return "", err
	}
	defer unlock()

	access, err := m.read("access_token")
	if err != nil {
		return "", err
	}
	if exp, err := jwtExpiry(access); err == nil && time.Until(exp) > expiryLeeway {
		return access, nil
	}
	refresh, err := m.read("refresh_token")
	if err != nil {
		return "", err
	}
	t, err := m.tokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refresh},
	})
	if err != nil {
		return "", fmt.Errorf("refreshing session: %w", err)
	}
	if err := m.save(t); err != nil {
		return "", err
	}
	return t.AccessToken, nil
}

func (m *Manager) tokenRequest(ctx context.Context, form url.Values) (tokenResponse, error) {
	var t tokenResponse
	raw, err := m.HTTP.Do(ctx, rewe.Request{Method: http.MethodPost, URL: tokenEndpoint(), Form: form})
	if err != nil {
		var apiErr *rewe.APIError
		if errors.As(err, &apiErr) {
			var oe oauthError
			if json.Unmarshal([]byte(apiErr.Msg), &oe) == nil && oe.Error != "" {
				if oe.Error == "invalid_grant" {
					return t, fmt.Errorf("%s (%s) - run 'korb login' again", oe.Description, oe.Error)
				}
				return t, fmt.Errorf("%s: %s", oe.Error, oe.Description)
			}
		}
		return t, err
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return t, fmt.Errorf("decoding token response: %w", err)
	}
	return t, nil
}

// Status describes the stored session.
type Status struct {
	LoggedIn              bool       `json:"loggedIn"`
	AccessTokenExpires    *time.Time `json:"accessTokenExpires"`
	RefreshTokenExpires   *time.Time `json:"refreshTokenExpires"`
	LoginHandlerInstalled bool       `json:"loginHandlerInstalled"`
}

// Status reports token expiry without contacting REWE.
func (m *Manager) Status() Status {
	var s Status
	if a, err := m.read("access_token"); err == nil {
		if exp, err := jwtExpiry(a); err == nil {
			s.AccessTokenExpires = &exp
		}
	}
	if r, err := m.read("refresh_token"); err == nil {
		s.LoggedIn = true
		// Offline tokens may carry no (or a zero) exp claim, meaning they do not expire.
		if exp, err := jwtExpiry(r); err == nil && !exp.Equal(time.Unix(0, 0)) {
			s.RefreshTokenExpires = &exp
			s.LoggedIn = time.Now().Before(exp)
		}
	}
	return s
}

func jwtExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, errors.New("token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, err
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, err
	}
	return time.Unix(claims.Exp, 0), nil
}
