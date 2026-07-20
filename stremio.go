package stremio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal client of the Stremio addon protocol — the HTTP contract
// documented at stremio.github.io/stremio-addon-sdk. It talks to one or more
// addons and routes each request to an addon whose manifest declares the
// needed resource and type. A meta-only addon therefore serves metadata and a
// stream addon serves streams, and neither resource depends on the other.
type Client struct {
	http   *http.Client
	addons []*resolvedAddon
}

// resolvedAddon pairs an addon's base URL with its manifest, fetched lazily on
// first use and cached for the life of the client.
type resolvedAddon struct {
	baseURL  string
	manifest Manifest
	fetched  bool
}

// NewClient builds a client over the given addon base URLs. A nil httpClient
// gets a default with a sane timeout. Addon manifests are not fetched here;
// they are fetched on first use so construction stays offline.
func NewClient(httpClient *http.Client, addonURLs ...string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	addons := make([]*resolvedAddon, 0, len(addonURLs))
	for _, u := range addonURLs {
		trimmed := strings.TrimRight(strings.TrimSpace(u), "/")
		if trimmed == "" {
			continue
		}
		addons = append(addons, &resolvedAddon{baseURL: trimmed})
	}
	return &Client{http: httpClient, addons: addons}
}

// Manifest is the subset of a Stremio addon manifest this client reads.
type Manifest struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Version   string         `json:"version"`
	Resources []ResourceDecl `json:"resources"`
	Types     []string       `json:"types"`
}

// ResourceDecl is one entry of a manifest's resources array. Stremio allows
// each entry to be either a bare string ("meta") or an object carrying its own
// types and id prefixes; this unmarshals both shapes.
type ResourceDecl struct {
	Name       string
	Types      []string
	IDPrefixes []string
}

// UnmarshalJSON accepts either a bare string or the object form.
func (r *ResourceDecl) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		r.Name = s
		return nil
	}
	var obj struct {
		Name       string   `json:"name"`
		Types      []string `json:"types"`
		IDPrefixes []string `json:"idPrefixes"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	r.Name, r.Types, r.IDPrefixes = obj.Name, obj.Types, obj.IDPrefixes
	return nil
}

// Meta is the subset of a meta response this client reads. For a series,
// Videos lists the episodes, each carrying its season and episode number.
type Meta struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"`
	Name   string  `json:"name"`
	Poster string  `json:"poster"`
	Videos []Video `json:"videos"`
}

// Video is one episode of a series' meta.
type Video struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Name    string `json:"name"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
}

// EpisodeTitle is the video's title, falling back to its name and then a
// generated label, so an item always has something to show.
func (v Video) EpisodeTitle() string {
	if v.Title != "" {
		return v.Title
	}
	if v.Name != "" {
		return v.Name
	}
	return fmt.Sprintf("Episode %d", v.Episode)
}

// Stream is the subset of a stream object this client reads. A stream is
// either a direct URL or a torrent identified by InfoHash.
type Stream struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	InfoHash string `json:"infoHash"`
}

// Ref is the location reference to store for this stream: the direct URL when
// present, otherwise a magnet URI built from the torrent info hash. It is
// empty when the stream carries neither.
func (s Stream) Ref() string {
	if s.URL != "" {
		return s.URL
	}
	if s.InfoHash != "" {
		return "magnet:?xt=urn:btih:" + s.InfoHash
	}
	return ""
}

// Meta fetches metadata for a content id from the first configured addon whose
// manifest serves the meta resource for the type. It returns ok=false, no
// error, when no configured addon serves meta for it.
func (c *Client) Meta(ctx context.Context, typ, id string) (Meta, bool, error) {
	for _, a := range c.addons {
		if err := c.ensureManifest(ctx, a); err != nil {
			return Meta{}, false, err
		}
		if !supports(a.manifest, "meta", typ, id) {
			continue
		}
		var resp struct {
			Meta Meta `json:"meta"`
		}
		if err := c.getJSON(ctx, a.baseURL+"/meta/"+typ+"/"+id+".json", &resp); err != nil {
			return Meta{}, false, err
		}
		if resp.Meta.ID != "" || resp.Meta.Name != "" {
			return resp.Meta, true, nil
		}
	}
	return Meta{}, false, nil
}

// Stream fetches the best stream for a content id (a movie id, or an episode
// id of the form tt...:season:episode) from the first addon whose manifest
// serves the stream resource for the type. Stremio ranks streams best-first,
// so the first entry is taken. It returns ok=false, no error, when no
// configured addon serves a stream — the metadata-only case.
func (c *Client) Stream(ctx context.Context, typ, id string) (Stream, bool, error) {
	for _, a := range c.addons {
		if err := c.ensureManifest(ctx, a); err != nil {
			return Stream{}, false, err
		}
		if !supports(a.manifest, "stream", typ, id) {
			continue
		}
		var resp struct {
			Streams []Stream `json:"streams"`
		}
		if err := c.getJSON(ctx, a.baseURL+"/stream/"+typ+"/"+id+".json", &resp); err != nil {
			return Stream{}, false, err
		}
		for _, s := range resp.Streams {
			if s.Ref() != "" {
				return s, true, nil
			}
		}
	}
	return Stream{}, false, nil
}

// ensureManifest fetches and caches an addon's manifest on first use.
func (c *Client) ensureManifest(ctx context.Context, a *resolvedAddon) error {
	if a.fetched {
		return nil
	}
	var m Manifest
	if err := c.getJSON(ctx, a.baseURL+"/manifest.json", &m); err != nil {
		return fmt.Errorf("fetch manifest from %s: %w", a.baseURL, err)
	}
	a.manifest = m
	a.fetched = true
	return nil
}

// supports reports whether a manifest declares the resource for the type, and
// that the id matches any id-prefix constraint the resource carries. A
// bare-string resource inherits the manifest's top-level types.
func supports(m Manifest, resource, typ, id string) bool {
	for _, r := range m.Resources {
		if r.Name != resource {
			continue
		}
		types := r.Types
		if len(types) == 0 {
			types = m.Types
		}
		if !contains(types, typ) {
			continue
		}
		if len(r.IDPrefixes) > 0 && !hasAnyPrefix(id, r.IDPrefixes) {
			continue
		}
		return true
	}
	return false
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func (c *Client) getJSON(ctx context.Context, url string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
