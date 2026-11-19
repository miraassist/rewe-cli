// Package config handles korb's on-disk state. The layout is compatible with the
// Haskell implementation so both binaries can share tokens and the selected store.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Dir returns $KORB_CONFIG_DIR if set, else $XDG_CONFIG_HOME/korb, falling back to ~/.config/korb on every
// platform (matching Haskell's getXdgDirectory XdgConfig).
func Dir() (string, error) {
	if d := os.Getenv("KORB_CONFIG_DIR"); d != "" {
		return filepath.Abs(d)
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "korb"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "korb"), nil
}

// TokenDir is where access and refresh tokens live.
func TokenDir() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "tokens"), nil
}

// Service types supported by the REWE API.
const (
	ServicePickup   = "PICKUP"
	ServiceDelivery = "DELIVERY"
)

// Store is the selected market and service type.
type Store struct {
	WwIdent     string `json:"wwIdent"`
	ZipCode     string `json:"zipCode"`
	ServiceType string `json:"serviceType"`
}

// ErrNoStore is returned when no store has been selected yet.
var ErrNoStore = errors.New("no store selected - run 'korb store search <ZIP>' and 'korb store set <ID> <ZIP>' first")

// ReadStore loads the selected store. A missing selected_service file means PICKUP
// (the only mode the Haskell implementation knows).
func ReadStore() (Store, error) {
	d, err := Dir()
	if err != nil {
		return Store{}, err
	}
	id, err1 := os.ReadFile(filepath.Join(d, "selected_store"))
	zip, err2 := os.ReadFile(filepath.Join(d, "selected_zip"))
	if errors.Is(err1, os.ErrNotExist) || errors.Is(err2, os.ErrNotExist) {
		return Store{}, ErrNoStore
	}
	if err := errors.Join(err1, err2); err != nil {
		return Store{}, err
	}
	st := ServicePickup
	if b, err := os.ReadFile(filepath.Join(d, "selected_service")); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			st = s
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Store{}, err
	}
	return Store{
		WwIdent:     strings.TrimSpace(string(id)),
		ZipCode:     strings.TrimSpace(string(zip)),
		ServiceType: st,
	}, nil
}

// WriteStore persists the selected store.
func WriteStore(s Store) error {
	d, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	files := map[string]string{
		"selected_store":   s.WwIdent,
		"selected_zip":     s.ZipCode,
		"selected_service": s.ServiceType,
	}
	for name, val := range files {
		if err := WriteFileAtomic(filepath.Join(d, name), []byte(val), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// WriteFileAtomic writes data to a temp file in the same directory and renames it into place.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Modification is a running change of an already placed order.
type Modification struct {
	OrderID    string    `json:"orderId"`
	CheckoutID string    `json:"checkoutId"`
	BasketID   string    `json:"basketId"`
	Started    time.Time `json:"started"`
	// Basket is the last known modification basket (REWE API JSON), used if
	// REWE does not return it on its own.
	Basket json.RawMessage `json:"basket,omitempty"`
}

func modificationPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "order_modification.json"), nil
}

// ReadModification returns the running order modification, or nil if there is none.
func ReadModification() (*Modification, error) {
	p, err := modificationPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Modification
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("corrupt %s: %w - remove it or run 'korb orders modify cancel'", p, err)
	}
	return &m, nil
}

// WriteModification stores (m != nil) or clears (m == nil) the running modification.
func WriteModification(m *Modification) error {
	p, err := modificationPath()
	if err != nil {
		return err
	}
	if m == nil {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return WriteFileAtomic(p, b, 0o600)
}
