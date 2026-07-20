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
