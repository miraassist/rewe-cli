package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/miraassist/rewe-cli/internal/config"
)

// REWE's Keycloak only allows the authorization-code flow for the app client:
// the device flow endpoint is blocked by REWE's firewall and direct password
// grants are disabled for "reweios". The redirect goes to the app's custom URL
// scheme de.rewe.app://, which korb can receive via a desktop URL handler
// (see handler_*.go) or which the user can paste.

const pendingMaxAge = 30 * time.Minute

type pendingLogin struct {
	Verifier string    `json:"verifier"`
	State    string    `json:"state"`
	Created  time.Time `json:"created"`
}

// LoginResult is written when a login completes, so a waiting `korb login`
// process notices a completion done by the URL handler process.
type LoginResult struct {
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Finished time.Time `json:"finished"`
	State    string    `json:"state"`
}

func (m *Manager) pendingPath() string { return filepath.Join(m.Dir, "login_pending.json") }
func (m *Manager) resultPath() string  { return filepath.Join(m.Dir, "login_result.json") }

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// StartLogin creates a PKCE verifier and state, stores them, and returns the
// URL the user has to open in a browser. Starting again replaces an older attempt.
func (m *Manager) StartLogin() (authURL, state string, err error) {
	verifier, err := randomString(32)
	if err != nil {
		return "", "", err
	}
	state, err = randomString(16)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	unlock, err := m.lock()
	if err != nil {
		return "", "", err
	}
	defer unlock()
	p, _ := json.Marshal(pendingLogin{Verifier: verifier, State: state, Created: time.Now()})
	if err := config.WriteFileAtomic(m.pendingPath(), p, 0o600); err != nil {
		return "", "", err
	}
	_ = os.Remove(m.resultPath())

	q := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"scope":                 {scope},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	return authEndpoint() + "?" + q.Encode(), state, nil
}

// parseRedirect extracts code and state from what the user pasted: the full
// de.rewe.app://redirect?... URL (possibly HTML-escaped), a query string, or the bare code.
func parseRedirect(input string) (code, state string, err error) {
	s := strings.TrimSpace(input)
	s = strings.Trim(s, `"'<>`)
	s = strings.ReplaceAll(s, "&amp;", "&")
	if s == "" {
		return "", "", errors.New("empty input")
	}
	query := s
	if i := strings.Index(s, "?"); i >= 0 {
		query = s[i+1:]
	}
	if i := strings.Index(query, "#"); i >= 0 { // response_mode=fragment fallback
		query = query[:i] + "&" + query[i+1:]
	}
	if strings.Contains(query, "=") {
		v, perr := url.ParseQuery(query)
		if perr != nil {
			return "", "", fmt.Errorf("cannot parse redirect URL: %w", perr)
		}
		if e := v.Get("error"); e != "" {
			return "", "", fmt.Errorf("REWE login failed: %s %s", e, v.Get("error_description"))
		}
		if v.Get("code") == "" {
			return "", "", errors.New("no 'code' parameter found - copy the complete de.rewe.app://redirect?... URL")
		}
		return v.Get("code"), v.Get("state"), nil
	}
	if strings.ContainsAny(s, " \t/:") {
		return "", "", errors.New("input is neither a de.rewe.app://redirect?... URL nor an authorization code")
	}
	return s, "", nil // bare code
}

// CompleteLogin exchanges the code from the redirect for tokens.
func (m *Manager) CompleteLogin(ctx context.Context, redirect string) (err error) {
	code, state, err := parseRedirect(redirect)
	if err != nil {
		return err
	}
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()

	raw, rerr := os.ReadFile(m.pendingPath())
	if errors.Is(rerr, os.ErrNotExist) {
		if r, ok := m.readResult(); ok && r.OK && state != "" && r.State == state {
			return nil // this redirect was already handled (e.g. by the URL handler)
		}
		return errors.New("no login in progress - run 'korb login' first")
	}
	if rerr != nil {
		return rerr
	}
	var p pendingLogin
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("corrupt pending login state: %w - run 'korb login' again", err)
	}
	defer func() {
		res := LoginResult{OK: err == nil, Finished: time.Now(), State: p.State}
		if err != nil {
			res.Error = err.Error()
		}
		b, _ := json.Marshal(res)
		_ = config.WriteFileAtomic(m.resultPath(), b, 0o600)
	}()
	if state != "" && state != p.State {
		return errors.New("redirect belongs to a different login attempt - use the URL from the latest 'korb login'")
	}
	if time.Since(p.Created) > pendingMaxAge {
		_ = os.Remove(m.pendingPath())
		return errors.New("login attempt expired - run 'korb login' again")
	}
	t, err := m.tokenRequest(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {p.Verifier},
	})
	if err != nil {
		return fmt.Errorf("exchanging authorization code: %w", err)
	}
	if err := m.save(t); err != nil {
		return err
	}
	_ = os.Remove(m.pendingPath())
	return nil
}

func (m *Manager) readResult() (LoginResult, bool) {
	var r LoginResult
	b, err := os.ReadFile(m.resultPath())
	if err != nil || json.Unmarshal(b, &r) != nil {
		return r, false
	}
	return r, true
}

// WaitForCompletion polls until the login with the given state has been
// completed by another process (the URL handler), or ctx ends.
func (m *Manager) WaitForCompletion(ctx context.Context, state string) (LoginResult, error) {
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for {
		if r, ok := m.readResult(); ok && r.State == state {
			return r, nil
		}
		select {
		case <-ctx.Done():
			return LoginResult{}, ctx.Err()
		case <-t.C:
		}
	}
}
