// Package rewecli holds what the rewe-cli module shares at its root: the
// version and the REWE client certificate.
package rewecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/miraassist/rewe-cli/internal/config"
)

// Version of rewe-cli.
const Version = "0.8.0"

// The REWE mobile API requires the mTLS client certificate of the REWE app.
// rewe-cli does not ship it: on first use korb fetches it from the korb
// repository it is a port of (a fixed commit) and checks it against fixed
// SHA-256 sums, then keeps it in its config directory. KORB_CERT_DIR points
// to a directory with private.pem and private.key instead.

// certSource is one file of the certificate: where it comes from and its sum.
type certSource struct{ name, url, sha256 string }

const certBase = "https://raw.githubusercontent.com/yannick-cw/korb/9069f532cdb8be93becd440378a18a42ffcae0fa/certs/mobile-clients-api.rewe.de/"

// certSources is a variable for the tests.
var certSources = []certSource{
	{"private.pem", certBase + "private.pem", "cfec10d1c2d987affc2e54cc561c2130fb20afbf320bab42dbe1a6b67edbd3b7"},
	{"private.key", certBase + "private.key", "0d0f9c7c0f8191c2b366448b12d75e5a94b0e446fc7ad1579ccfcf2ab9c4aa86"},
}

// CertDir is where korb keeps the certificate: $KORB_CERT_DIR, else certs/
// in korb's config directory.
func CertDir() (string, error) {
	if dir := os.Getenv("KORB_CERT_DIR"); dir != "" {
		return dir, nil
	}
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "certs"), nil
}

// ClientCert returns the PEM encoded client certificate and key, fetching
// them first when they are not there yet (not with KORB_CERT_DIR: a
// directory someone names is theirs to fill).
func ClientCert(ctx context.Context) (certPEM, keyPEM []byte, err error) {
	dir, err := CertDir()
	if err != nil {
		return nil, nil, err
	}
	certPEM, err1 := os.ReadFile(filepath.Join(dir, "private.pem"))
	keyPEM, err2 := os.ReadFile(filepath.Join(dir, "private.key"))
	if err1 == nil && err2 == nil {
		return certPEM, keyPEM, nil
	}
	if os.Getenv("KORB_CERT_DIR") != "" {
		return nil, nil, fmt.Errorf("KORB_CERT_DIR %s: %w", dir, errors.Join(err1, err2))
	}
	if err := FetchCert(ctx); err != nil {
		return nil, nil, err
	}
	return ClientCert(ctx)
}

// FetchCert downloads the certificate, checks its sums and writes it to
// CertDir. Nothing is written unless both files are right.
func FetchCert(ctx context.Context) error {
	dir, err := CertDir()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	files := map[string][]byte{}
	for _, s := range certSources {
		b, err := download(ctx, s.url)
		if err != nil {
			return fmt.Errorf("fetching the REWE client certificate (%s): %w", s.name, err)
		}
		if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != s.sha256 {
			return fmt.Errorf("the REWE client certificate (%s) from %s is not the expected one (SHA-256 %x) - not used",
				s.name, s.url, sum)
		}
		files[s.name] = b
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for name, b := range files {
		tmp := filepath.Join(dir, "."+name+".tmp")
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
