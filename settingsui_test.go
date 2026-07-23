package stremio_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	stremio "github.com/mosaic-media/module-stremio-addons"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// TestSettingsUIRendersSections proves the module contributes a settings screen
// (ADR 0038): a Screen carrying the add-by-URL form (a SubmitField whose action
// carries the "$value" placeholder and configureModule), the installed addon
// with a Remove control, and a browse grid of installable addons.
//
// The browse section also consults Stremio's official addon directory, which the
// module names by URL — the one thing that survived the bundled default's
// removal, because it is a directory rather than a content source (ADR 0072).
// The rewriting transport is what keeps that hermetic: every outbound request
// lands on the fake whatever host it names, so the directory is exercised
// without reaching the real one.
func TestSettingsUIRendersSections(t *testing.T) {
	server := fakeAddon(withStreams)
	defer server.Close()
	cap := stremio.New(toFake(server))

	resp, err := cap.SettingsUI(context.Background(), v1.SettingsUIRequest{
		Caller: v1.CallerFromSession("s-1"), Settings: addonSettings(server.URL),
	})
	if err != nil {
		t.Fatalf("SettingsUI: %v", err)
	}

	// The UI is a serialised UINode Screen.
	var root map[string]any
	if err := json.Unmarshal(resp.UI, &root); err != nil {
		t.Fatalf("settings UI is not valid JSON: %v", err)
	}
	if root["type"] != "Screen" {
		t.Fatalf("root type = %v, want Screen", root["type"])
	}

	s := string(resp.UI)
	// The add form, the installed addon rendered as a card (its manifest name and
	// logo, a Configure control since it declares configurable, and Remove), and
	// a Grid layout (ADR 0038 improvements).
	for _, want := range []string{
		"SubmitField", "$value", "configureModule", "Add an addon", "Installed addons",
		"Fake Addon", "http://fake/logo.png", "Configure", "Remove", "Grid", "Browse addons",
		// The browse grid is populated from the addon directory, not an empty
		// state — this is the surface the bundled default used to provide and
		// that the directory pointer keeps alive.
		"Installable Addon", "Install",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("settings UI missing %q", want)
		}
	}
	// The raw manifest URL should no longer be shown as a card label — the name is.
	if strings.Contains(s, `"text":"`+server.URL) {
		t.Errorf("settings UI shows the raw addon URL as a label; want the addon name")
	}
	// No settings document at all must still render the screen rather than
	// failing. With no bundled addon that is what a fresh install looks like, and
	// this screen is the only way out of it — so it has to say so.
	fresh, err := cap.SettingsUI(context.Background(), v1.SettingsUIRequest{Caller: v1.CallerFromSession("s-1")})
	if err != nil {
		t.Fatalf("SettingsUI with no settings: %v", err)
	}
	if !strings.Contains(string(fresh.UI), "No Stremio addons yet") {
		t.Error("a fresh install's settings screen must offer an empty state, not an empty grid")
	}
	if !strings.Contains(string(fresh.UI), "Installable Addon") {
		t.Error("a fresh install must still be able to browse installable addons")
	}
}

// toFake returns an HTTP client that sends every request to the fake addon,
// whatever host the URL names. It is how a test exercises code paths that name a
// real host by constant without reaching it.
func toFake(server *httptest.Server) *http.Client {
	target, err := url.Parse(server.URL)
	if err != nil {
		panic(err)
	}
	return &http.Client{Transport: rewriteHost{target: target, base: server.Client().Transport}}
}

type rewriteHost struct {
	target *url.URL
	base   http.RoundTripper
}

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host = r.target.Scheme, r.target.Host
	clone.Host = ""
	return r.base.RoundTrip(clone)
}
