package stremio_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	stremio "github.com/mosaic-media/mosaic-module-stremio"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// TestSettingsUIRendersSections proves the module contributes a settings screen
// (ADR 0038): a Screen carrying the add-by-URL form (a SubmitField whose action
// carries the "$value" placeholder and configureModule), the installed addon
// with a Remove control, and a browse section. addonSettings opts out of the
// bundled default, so the only installed addon is the fake, keeping it hermetic.
func TestSettingsUIRendersSections(t *testing.T) {
	server := fakeAddon(withStreams)
	defer server.Close()
	cap := stremio.New(server.Client())

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
	} {
		if !strings.Contains(s, want) {
			t.Errorf("settings UI missing %q", want)
		}
	}
	// The raw manifest URL should no longer be shown as a card label — the name is.
	if strings.Contains(s, `"text":"`+server.URL) {
		t.Errorf("settings UI shows the raw addon URL as a label; want the addon name")
	}
}
