package e2e

import (
	"strings"
	"testing"
	"time"
)

func TestDesktopReconnectAfterBrowserNetworkDrop(t *testing.T) {
	if !chromeAvailable() {
		t.Skip("google-chrome is not available")
	}

	port := freePort(t)
	tunnel := startTunnel(t, port)
	defer tunnel.stop()

	page, cleanup := launchChromePage(t, tunnel.baseURL)
	defer cleanup()

	page.mustWaitForText(t, "#username", 5*time.Second)
	page.fillInput(t, "#username", cfg.User)
	page.fillInput(t, "#password", cfg.Pass)
	page.submitForm(t, "form")

	page.mustWaitForPathname(t, "/desktop", 10*time.Second)
	page.mustWaitForTextContains(t, "Connected", 10*time.Second)

	tunnel.stop()
	page.mustWaitForTextContains(t, "Reconnecting", 10*time.Second)
	page.mustWaitForPathname(t, "/desktop", 10*time.Second)

	tunnel = startTunnel(t, port)
	defer tunnel.stop()
	page.mustWaitForTextContains(t, "Connected", 15*time.Second)
	page.mustNotContainText(t, "Sign in", 2*time.Second)

	pathname := page.evalString(t, "window.location.pathname")
	if pathname != "/desktop" {
		t.Fatalf("expected to stay on /desktop after reconnect, got %q", pathname)
	}

	bodyText := page.evalString(t, "document.body.innerText")
	if !strings.Contains(bodyText, cfg.User) {
		t.Fatalf("expected page to remain logged in as %q after reconnect, body text: %q", cfg.User, bodyText)
	}
}
