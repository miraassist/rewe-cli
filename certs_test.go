package rewecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// fakeSource serves the two files and points certSources at it.
func fakeSource(t *testing.T, pem, key string, wantPem, wantKey string) *int {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/private.pem":
			w.Write([]byte(pem))
		case "/private.key":
			w.Write([]byte(key))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	old := certSources
	certSources = []certSource{{"private.pem", srv.URL + "/private.pem", sum(wantPem)}, {"private.key", srv.URL + "/private.key", sum(wantKey)}}
	t.Cleanup(func() { certSources = old })
	t.Setenv("KORB_CONFIG_DIR", t.TempDir())
	t.Setenv("KORB_CERT_DIR", "")
	return &hits
}

// On first use korb fetches the certificate into its config directory,
// afterwards it reads it from there.
func TestClientCertIsFetchedOnce(t *testing.T) {
	hits := fakeSource(t, "PEM", "KEY", "PEM", "KEY")
	c, k, err := ClientCert(context.Background())
	if err != nil || string(c) != "PEM" || string(k) != "KEY" {
		t.Fatalf("%q %q %v", c, k, err)
	}
	dir, _ := CertDir()
	if st, err := os.Stat(filepath.Join(dir, "private.key")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", st, err)
	}
	if _, _, err := ClientCert(context.Background()); err != nil || *hits != 2 {
		t.Fatalf("fetched again: %d hits, %v", *hits, err)
	}
}

// A file that is not the expected one is refused, and nothing is written.
func TestAWrongCertificateIsRefused(t *testing.T) {
	fakeSource(t, "PEM", "OTHER KEY", "PEM", "KEY")
	_, _, err := ClientCert(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not the expected one") {
		t.Fatalf("err %v", err)
	}
	dir, _ := CertDir()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("written: %v", entries)
	}
}

// KORB_CERT_DIR is the user's: korb reads it and never fetches into it.
func TestCertDirOfTheUserIsNotFilled(t *testing.T) {
	hits := fakeSource(t, "PEM", "KEY", "PEM", "KEY")
	dir := t.TempDir()
	t.Setenv("KORB_CERT_DIR", dir)
	if _, _, err := ClientCert(context.Background()); err == nil || !strings.Contains(err.Error(), "KORB_CERT_DIR") || *hits != 0 {
		t.Fatalf("err %v, %d hits", err, *hits)
	}
	os.WriteFile(filepath.Join(dir, "private.pem"), []byte("MINE"), 0o600)
	os.WriteFile(filepath.Join(dir, "private.key"), []byte("MINE"), 0o600)
	if c, _, err := ClientCert(context.Background()); err != nil || string(c) != "MINE" {
		t.Fatalf("%q %v", c, err)
	}
}
