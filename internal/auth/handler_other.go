//go:build !linux

package auth

import (
	"errors"
	"os/exec"
	"runtime"
)

// HandlerSupported reports whether korb can register itself for de.rewe.app:// links.
// On macOS this would require an .app bundle; the paste flow is used instead.
const HandlerSupported = false

func HandlerInstalled() bool { return false }

func InstallHandler() (string, error) {
	return "", errors.New("registering a de.rewe.app:// URL handler is only supported on Linux")
}

func UninstallHandler() error { return nil }

func Notify(title, body string) {}

func OpenBrowser(url string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", url).Start()
	}
	return errors.New("cannot open a browser on this platform")
}
