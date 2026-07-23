package stremio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormaliseAddonURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"base url", "https://v3-cinemeta.strem.io", "https://v3-cinemeta.strem.io"},
		{"trailing slash", "https://v3-cinemeta.strem.io/", "https://v3-cinemeta.strem.io"},
		{"manifest url (what a user pastes)", "https://v3-cinemeta.strem.io/manifest.json", "https://v3-cinemeta.strem.io"},
		{"manifest url with trailing slash", "https://v3-cinemeta.strem.io/manifest.json/", "https://v3-cinemeta.strem.io"},
		{"stremio scheme", "stremio://v3-cinemeta.strem.io/manifest.json", "https://v3-cinemeta.strem.io"},
		{"config path preserved", "https://torrentio.strem.fun/providers=yts/manifest.json", "https://torrentio.strem.fun/providers=yts"},
		{"query stripped", "https://addon.example/manifest.json?x=1", "https://addon.example"},
		{"surrounding whitespace", "  https://addon.example/manifest.json  ", "https://addon.example"},
		{"empty", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normaliseAddonURL(tc.in); got != tc.want {
				t.Fatalf("normaliseAddonURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestAddonsAreOnlyWhatTheUserConfigured pins that this module bundles no addon
// of its own.
//
// It used to bundle Cinemeta so a fresh install had metadata (ADR 0035). That
// guarantee moved to `module-cinemeta`, a core module that cannot be switched
// off (ADR 0072), and leaving a second Cinemeta here would have shown every
// title twice in search — the Platform unions search providers without
// cross-provider dedup.
func TestAddonsAreOnlyWhatTheUserConfigured(t *testing.T) {
	cap := New(nil)
	base := func(addons []*resolvedAddon) []string {
		out := make([]string, len(addons))
		for i, a := range addons {
			out[i] = a.baseURL
		}
		return out
	}

	// No settings → nothing to source from, and that is an error rather than a
	// silent empty answer. It is now the state a fresh install is in.
	if _, err := cap.clientFrom(nil); err == nil {
		t.Fatal("clientFrom with no settings must error: this module bundles no addon")
	}

	// Configured addons are used in configured order, which is the priority rule
	// the metadata merge reads: the first addon a user lists wins a conflict.
	c, err := cap.clientFrom([]byte(`{"addons":["https://torrentio.strem.fun/manifest.json","https://v3-cinemeta.strem.io/manifest.json"]}`))
	if err != nil {
		t.Fatalf("clientFrom(user addons): %v", err)
	}
	got := base(c.addons)
	if len(got) != 2 || got[0] != "https://torrentio.strem.fun" || got[1] != "https://v3-cinemeta.strem.io" {
		t.Fatalf("addons = %v, want [torrentio, cinemeta] in configured order", got)
	}
	// The configured position is what carries the priority downstream.
	if c.addons[0].order != 0 || c.addons[1].order != 1 {
		t.Errorf("addon order not recorded: %d, %d", c.addons[0].order, c.addons[1].order)
	}

	// The same addon written two ways is one addon.
	c, err = cap.clientFrom([]byte(`{"addons":["https://torrentio.strem.fun/manifest.json","stremio://torrentio.strem.fun/manifest.json"]}`))
	if err != nil {
		t.Fatalf("clientFrom(duplicate): %v", err)
	}
	if got := base(c.addons); len(got) != 1 {
		t.Fatalf("addons = %v, want the duplicate deduped by base URL", got)
	}

	// A settings document written before the default was removed still parses;
	// the retired key is ignored rather than needing a migration.
	c, err = cap.clientFrom([]byte(`{"addons":["https://torrentio.strem.fun/manifest.json"],"disableDefaultAddons":true}`))
	if err != nil {
		t.Fatalf("clientFrom(legacy document): %v", err)
	}
	if got := base(c.addons); len(got) != 1 || got[0] != "https://torrentio.strem.fun" {
		t.Fatalf("legacy document addons = %v, want just torrentio", got)
	}
}

// TestUsefulToMosaic pins the browse compatibility filter (ADR 0038): an addon
// is offered only if it fills a role the Platform sources.
func TestUsefulToMosaic(t *testing.T) {
	useful := func(names ...string) Manifest {
		m := Manifest{}
		for _, n := range names {
			m.Resources = append(m.Resources, ResourceDecl{Name: n})
		}
		return m
	}
	cases := []struct {
		name string
		m    Manifest
		want bool
	}{
		{"meta", useful("meta"), true},
		{"stream", useful("stream"), true},
		{"subtitles only", useful("subtitles"), true},
		{"catalog", useful("catalog"), true},
		{"addon_catalog only", useful("addon_catalog"), false},
		{"no resources", useful(), false},
		{"unknown only", useful("something"), false},
		{"mixed keeps", useful("addon_catalog", "stream"), true},
		{"denied id despite content resource", Manifest{ID: "org.stremio.discordpresence", Resources: []ResourceDecl{{Name: "meta"}}}, false},
	}
	for _, tc := range cases {
		if got := usefulToMosaic(tc.m); got != tc.want {
			t.Errorf("usefulToMosaic(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestClientSetsUserAgent pins that requests carry the module's own User-Agent
// rather than Go's default, which Cloudflare-fronted addons reject with 403.
// It also proves an addon configured by its manifest URL is reachable — the
// two fixes exercised together against one server.
func TestClientSetsUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/manifest.json":
			_, _ = w.Write([]byte(`{"id":"x","name":"X","version":"1.0.0","resources":["meta"],"types":["movie"]}`))
		case strings.HasPrefix(r.URL.Path, "/meta/movie/"):
			_, _ = w.Write([]byte(`{"meta":{"id":"tt1254207","type":"movie","name":"Blade Runner 2049"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Configure by the manifest URL, the form a user copies from Stremio.
	c := NewClient(nil, srv.URL+"/manifest.json")
	meta, ok, err := c.Meta(ctx, "movie", "tt1254207")
	if err != nil || !ok {
		t.Fatalf("Meta via manifest URL: ok=%v err=%v", ok, err)
	}
	if meta.Name != "Blade Runner 2049" {
		t.Fatalf("meta name = %q, want Blade Runner 2049", meta.Name)
	}
	if gotUA != userAgent {
		t.Fatalf("User-Agent = %q, want %q", gotUA, userAgent)
	}
	if strings.HasPrefix(gotUA, "Go-http-client") {
		t.Fatalf("client sent Go default User-Agent %q", gotUA)
	}
}
