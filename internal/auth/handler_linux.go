package auth

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	desktopFileName = "korb-rewe-login.desktop"
	schemeMime      = "x-scheme-handler/de.rewe.app"
)

// HandlerSupported reports whether korb can register itself for de.rewe.app:// links.
const HandlerSupported = true

func applicationsDir() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "applications"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "applications"), nil
}

func desktopEntry(exe string) string {
	// Exec quoting per the Desktop Entry spec: quote the path, escape \ " ` $.
	r := strings.NewReplacer(`\`, `\\\\`, `"`, `\"`, "`", "\\`", `$`, `\$`)
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=korb REWE login
Comment=Receives the REWE login redirect for the korb CLI
Exec="%s" login callback %%u
Terminal=false
NoDisplay=true
MimeType=%s;
`, r.Replace(exe), schemeMime)
}

func currentExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	if strings.HasPrefix(exe, os.TempDir()) && strings.Contains(exe, "go-build") {
		return "", errors.New("refusing to register a temporary 'go run' binary as URL handler - install korb first")
	}
	return exe, nil
}

// HandlerInstalled reports whether de.rewe.app:// links are routed to this korb binary.
func HandlerInstalled() bool {
	dir, err := applicationsDir()
	if err != nil {
		return false
	}
	exe, err := currentExe()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, desktopFileName))
	if err != nil || string(b) != desktopEntry(exe) {
		return false
	}
	out, err := exec.Command("xdg-mime", "query", "default", schemeMime).Output()
	return err == nil && strings.TrimSpace(string(out)) == desktopFileName
}

// InstallHandler registers this korb binary for de.rewe.app:// links (per user,
// no root needed). It returns the path of the desktop file.
func InstallHandler() (string, error) {
	exe, err := currentExe()
	if err != nil {
		return "", err
	}
	if _, err := exec.LookPath("xdg-mime"); err != nil {
		return "", errors.New("xdg-mime not found (install xdg-utils) - cannot register URL handler")
	}
	dir, err := applicationsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, desktopFileName)
	if err := os.WriteFile(path, []byte(desktopEntry(exe)), 0o644); err != nil {
		return "", err
	}
	if out, err := exec.Command("xdg-mime", "default", desktopFileName, schemeMime).CombinedOutput(); err != nil {
		return "", fmt.Errorf("xdg-mime default: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if _, err := exec.LookPath("update-desktop-database"); err == nil {
		_ = exec.Command("update-desktop-database", dir).Run()
	}
	return path, nil
}

// UninstallHandler removes the desktop file again.
func UninstallHandler() error {
	dir, err := applicationsDir()
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, desktopFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if _, lerr := exec.LookPath("update-desktop-database"); lerr == nil {
		_ = exec.Command("update-desktop-database", dir).Run()
	}
	return err
}

// Notify shows a desktop notification (best effort).
func Notify(title, body string) {
	if p, err := exec.LookPath("notify-send"); err == nil {
		_ = exec.Command(p, "-a", "korb", title, body).Run()
	}
}

// OpenBrowser opens url in the default browser (best effort).
func OpenBrowser(url string) error {
	return exec.Command("xdg-open", url).Start()
}
