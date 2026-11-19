package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miraassist/rewe-cli/internal/rewe"
)

func TestParseRedirect(t *testing.T) {
	cases := []struct {
		in, code, state, err string
	}{
		{in: "de.rewe.app://redirect?state=s1&session_state=x&code=abc.def-1", code: "abc.def-1", state: "s1"},
		{in: "  'de.rewe.app://redirect?code=abc&amp;state=s2'\n", code: "abc", state: "s2"},
		{in: "code=abc&state=s3", code: "abc", state: "s3"},
		{in: "de.rewe.app://redirect#state=s4&code=frag", code: "frag", state: "s4"},
		{in: "abc.def-ghi", code: "abc.def-ghi"},
		{in: "de.rewe.app://redirect?error=access_denied&error_description=nope", err: "access_denied"},
		{in: "de.rewe.app://redirect?state=x", err: "no 'code'"},
		{in: "https://example.com/some path", err: "neither"},
		{in: "   ", err: "empty"},
	}
	for _, c := range cases {
		code, state, err := parseRedirect(c.in)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: want error containing %q, got %v", c.in, c.err, err)
			}
			continue
		}
		if err != nil || code != c.code || state != c.state {
			t.Errorf("%q: got (%q, %q, %v), want (%q, %q)", c.in, code, state, err, c.code, c.state)
		}
	}
}

func fakeJWT(exp time.Time) string {
	p, _ := json.Marshal(map[string]int64{"exp": exp.Unix()})
	return "h." + base64.RawURLEncoding.EncodeToString(p) + ".s"
}

// rewriteTransport sends every request to the test server.
type rewriteTransport struct{ target *url.URL }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = rt.target.Scheme, rt.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

func newTestManager(t *testing.T, h http.HandlerFunc) *Manager {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return &Manager{Dir: t.TempDir(), HTTP: &rewe.Doer{HTTP: &http.Client{Transport: rewriteTransport{u}}}}
}

func TestLoginRoundTrip(t *testing.T) {
	var gotVerifier string
	m := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "the-code" {
			http.Error(w, `{"error":"invalid_grant","error_description":"bad"}`, 400)
			return
		}
		gotVerifier = r.Form.Get("code_verifier")
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"r1"}`, fakeJWT(time.Now().Add(time.Hour)))
	})
	authURL, state, err := m.StartLogin()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	if u.Query().Get("state") != state || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("bad auth URL %s", authURL)
	}
	if err := m.CompleteLogin(context.Background(), "de.rewe.app://redirect?state=wrong&code=the-code"); err == nil {
		t.Fatal("state mismatch must fail")
	}
	if err := m.CompleteLogin(context.Background(), "de.rewe.app://redirect?state="+state+"&code=the-code"); err != nil {
		t.Fatal(err)
	}
	if gotVerifier == "" {
		t.Fatal("verifier not sent")
	}
	if b, _ := os.ReadFile(filepath.Join(m.Dir, "refresh_token")); string(b) != "r1" {
		t.Fatalf("refresh token not stored: %q", b)
	}
	// The URL handler may deliver the same redirect again: that is not an error.
	if err := m.CompleteLogin(context.Background(), "de.rewe.app://redirect?state="+state+"&code=the-code"); err != nil {
		t.Fatalf("duplicate completion: %v", err)
	}
	r, err := m.WaitForCompletion(context.Background(), state)
	if err != nil || !r.OK {
		t.Fatalf("result %+v %v", r, err)
	}
}

func TestAccessTokenRefresh(t *testing.T) {
	calls := 0
	m := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "old-refresh" {
			http.Error(w, `{"error":"invalid_grant","error_description":"Invalid refresh token"}`, 400)
			return
		}
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"new-refresh"}`, fakeJWT(time.Now().Add(time.Hour)))
	})
	_ = os.WriteFile(filepath.Join(m.Dir, "access_token"), []byte(fakeJWT(time.Now().Add(-time.Minute))), 0o600)
	_ = os.WriteFile(filepath.Join(m.Dir, "refresh_token"), []byte("old-refresh\n"), 0o600)

	tok, err := m.AccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := m.AccessToken(context.Background()); again != tok || calls != 1 {
		t.Fatalf("valid token must be reused without refresh (calls=%d)", calls)
	}
	if b, _ := os.ReadFile(filepath.Join(m.Dir, "refresh_token")); string(b) != "new-refresh" {
		t.Fatalf("rotated refresh token not stored: %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(m.Dir, "access_token")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode %v", fi.Mode().Perm())
	}

	_ = os.WriteFile(filepath.Join(m.Dir, "access_token"), []byte(fakeJWT(time.Now())), 0o600)
	_ = os.WriteFile(filepath.Join(m.Dir, "refresh_token"), []byte("revoked"), 0o600)
	if _, err := m.AccessToken(context.Background()); err == nil || !strings.Contains(err.Error(), "korb login") {
		t.Fatalf("want hint to log in again, got %v", err)
	}
}
