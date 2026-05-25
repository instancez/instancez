package cloud

import (
	"errors"
	"os/exec"
	"runtime"
)

// OpenBrowser attempts to open url in the user's default browser. Returns
// an error if no suitable launcher is available on this OS; callers should
// not treat that as fatal — print the URL and let the user open it manually.
func OpenBrowser(url string) error {
	cmd := browserCommand(runtime.GOOS)
	if cmd == "" {
		return errors.New("no browser launcher for this OS")
	}
	args := []string{url}
	if cmd == "rundll32" {
		// Windows: rundll32 url.dll,FileProtocolHandler <url>
		args = []string{"url.dll,FileProtocolHandler", url}
	}
	return exec.Command(cmd, args...).Start()
}

// browserCommand returns the OS-specific browser launcher binary name, or
// "" if unsupported. Extracted so it's unit-testable.
func browserCommand(goos string) string {
	switch goos {
	case "linux":
		return "xdg-open"
	case "darwin":
		return "open"
	case "windows":
		return "rundll32"
	default:
		return ""
	}
}
