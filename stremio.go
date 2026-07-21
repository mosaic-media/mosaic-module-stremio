package stremio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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

// NewClient builds a client over the given addon URLs. A nil httpClient gets a
// default with a sane timeout. Each URL is normalised to the addon's base URL
// (see normaliseAddonURL) so that either the base URL or the manifest URL a
// user copies from Stremio works. Manifests are not fetched here; they are
// fetched on first use so construction stays offline.
func NewClient(httpClient *http.Client, addonURLs ...string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	addons := make([]*resolvedAddon, 0, len(addonURLs))
	for _, u := range addonURLs {
		base := normaliseAddonURL(u)
		if base == "" {
			continue
		}
		addons = append(addons, &resolvedAddon{baseURL: base})
	}
	return &Client{http: httpClient, addons: addons}
}

// normaliseAddonURL turns whatever a user pastes into an addon's base URL — the
// prefix the client appends "/manifest.json", "/meta/..." and "/stream/..." to.
// Stremio's "Install"/"Copy link" hands out the manifest URL (ending in
// "/manifest.json"), and installs use the stremio:// scheme, so both are
// accepted alongside a bare base URL. A trailing "/manifest.json" is stripped
// rather than the whole path, preserving the configuration segment addons like
// Torrentio encode before it (".../providers=.../manifest.json"). A trailing
// slash is trimmed. Empty input yields "", which the caller skips.
func normaliseAddonURL(u string) string {
	s := strings.TrimSpace(u)
	if s == "" {
		return ""
	}
	// Stremio installs use stremio://; the transport underneath is HTTPS.
	if rest, ok := strings.CutPrefix(s, "stremio://"); ok {
		s = "https://" + rest
	}
	// Drop any query or fragment before deriving the base.
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, "/manifest.json")
	return strings.TrimRight(s, "/")
}

// Manifest is the subset of a Stremio addon manifest this client reads.
type Manifest struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Version   string         `json:"version"`
	Resources []ResourceDecl `json:"resources"`
	Types     []string       `json:"types"`
	Catalogs  []CatalogDecl  `json:"catalogs"`
}

// CatalogDecl is one entry of a manifest's catalogs array — a collection the
// addon exposes (Popular, Top). A catalog is typed and addressed by its id; the
// extra declarations say which query parameters it accepts, the one that
// matters here being "search".
type CatalogDecl struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
	// Extra is the modern shape: a list of accepted parameters, each named.
	Extra []ExtraDecl `json:"extra"`
	// ExtraSupported is the older shape: a bare list of accepted parameter
	// names. Both are read so search is detected whichever an addon uses.
	ExtraSupported []string `json:"extraSupported"`
}

// ExtraDecl is one accepted catalog parameter.
type ExtraDecl struct {
	Name string `json:"name"`
}

// SupportsSearch reports whether the catalog accepts a search query.
func (c CatalogDecl) SupportsSearch() bool {
	for _, e := range c.Extra {
		if e.Name == "search" {
			return true
		}
	}
	for _, n := range c.ExtraSupported {
		if n == "search" {
			return true
		}
	}
	return false
}

// MetaPreview is the subset of a catalog/search meta entry this client reads —
// the lightweight item shape a catalog returns, distinct from the full Meta a
// meta lookup returns. ReleaseInfo is a year ("2017") or a range ("2008-2013").
type MetaPreview struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Poster      string `json:"poster"`
	ReleaseInfo string `json:"releaseInfo"`
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
// Logo/ImdbRating/Runtime/Cast/Links back the rich detail surface (ADR 0034);
// Cinemeta provides them all — the module simply decoded none of them before.
type Meta struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Poster      string   `json:"poster"`
	Background  string   `json:"background"`
	Logo        string   `json:"logo"`
	Description string   `json:"description"`
	ReleaseInfo string   `json:"releaseInfo"`
	Genres      []string `json:"genres"`
	// ImdbRating is Cinemeta's rating, a string ("8.0") in its API.
	ImdbRating string `json:"imdbRating"`
	// Runtime is a display string whose format varies ("120 min").
	Runtime string `json:"runtime"`
	// Cast is the legacy top-level cast (names). Modern Cinemeta also puts cast
	// in Links with category "Cast"; both are read (see castNames).
	Cast   []string `json:"cast"`
	Links  []Link   `json:"links"`
	Videos []Video  `json:"videos"`
}

// Link is one entry of a meta's `links` array — the modern Cinemeta shape that
// carries cast, directors, writers and genres as categorised references.
type Link struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	URL      string `json:"url"`
}

// Video is one episode of a series' meta. Overview/Thumbnail/Released back the
// episode preview (ADR 0034); Cinemeta provides them on each video entry.
type Video struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Name     string `json:"name"`
	Season   int    `json:"season"`
	Episode  int    `json:"episode"`
	Overview string `json:"overview"`
	// Thumbnail is a still image URL for the episode.
	Thumbnail string `json:"thumbnail"`
	// Released is the episode's air date (an ISO datetime in Cinemeta).
	Released string `json:"released"`
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
// either a direct URL or a torrent identified by InfoHash. Description/
// BehaviorHints carry the release detail (quality, size) addons pack into the
// title, which parseStreamMeta teases back out (ADR 0037).
type Stream struct {
	Name          string        `json:"name"`
	Title         string        `json:"title"`
	Description   string        `json:"description"`
	URL           string        `json:"url"`
	InfoHash      string        `json:"infoHash"`
	FileIdx       int           `json:"fileIdx"`
	BehaviorHints behaviorHints `json:"behaviorHints"`
}

// behaviorHints is the subset of a stream's behaviorHints this client reads.
type behaviorHints struct {
	VideoSize int64  `json:"videoSize"`
	Filename  string `json:"filename"`
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

// text is the stream's descriptive text — the title, then name, then filename —
// where addons (Torrentio especially) pack quality, size and seeders.
func (s Stream) text() string {
	return strings.Join([]string{s.Title, s.Name, s.Description, s.BehaviorHints.Filename}, "\n")
}

// Subtitle is the subset of a subtitles response entry this client reads (ADR
// 0037): a track's language and the file URL.
type Subtitle struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Lang string `json:"lang"`
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

// Subtitles fetches subtitle tracks for a content id (a movie id or an episode
// id of the form tt...:season:episode) from the first addon whose manifest
// serves the subtitles resource for the type. It returns ok=false, no error,
// when no configured addon serves subtitles (ADR 0037).
func (c *Client) Subtitles(ctx context.Context, typ, id string) ([]Subtitle, bool, error) {
	for _, a := range c.addons {
		if err := c.ensureManifest(ctx, a); err != nil {
			return nil, false, err
		}
		if !supports(a.manifest, "subtitles", typ, id) {
			continue
		}
		var resp struct {
			Subtitles []Subtitle `json:"subtitles"`
		}
		if err := c.getJSON(ctx, a.baseURL+"/subtitles/"+typ+"/"+id+".json", &resp); err != nil {
			return nil, false, err
		}
		if len(resp.Subtitles) > 0 {
			return resp.Subtitles, true, nil
		}
	}
	return nil, false, nil
}

// Catalogs returns the catalog declarations across every configured addon, in
// configuration order. It is the source's collection list — what the admin
// collection browser enumerates before choosing what to publish.
func (c *Client) Catalogs(ctx context.Context) ([]CatalogDecl, error) {
	var out []CatalogDecl
	for _, a := range c.addons {
		if err := c.ensureManifest(ctx, a); err != nil {
			return nil, err
		}
		out = append(out, a.manifest.Catalogs...)
	}
	return out, nil
}

// CatalogItems lists one catalog's entries, from the first addon whose manifest
// declares a catalog of that type and id. skip pages through a large catalog
// (0 for the first page). It returns nil, no error, when no configured addon
// declares the catalog.
func (c *Client) CatalogItems(ctx context.Context, typ, id string, skip int) ([]MetaPreview, error) {
	for _, a := range c.addons {
		if err := c.ensureManifest(ctx, a); err != nil {
			return nil, err
		}
		if !hasCatalog(a.manifest, typ, id) {
			continue
		}
		u := a.baseURL + "/catalog/" + typ + "/" + id
		if skip > 0 {
			u += "/skip=" + strconv.Itoa(skip)
		}
		u += ".json"
		var resp struct {
			Metas []MetaPreview `json:"metas"`
		}
		if err := c.getJSON(ctx, u, &resp); err != nil {
			return nil, err
		}
		return resp.Metas, nil
	}
	return nil, nil
}

// Search queries every search-capable catalog across every configured addon and
// returns the union, de-duplicated by content id. A catalog that errors on the
// search query is skipped rather than failing the whole search, so one broken
// addon does not blank the results. It returns nil, no error, when nothing
// matches or no addon offers search.
func (c *Client) Search(ctx context.Context, query string) ([]MetaPreview, error) {
	var out []MetaPreview
	seen := make(map[string]bool)
	for _, a := range c.addons {
		if err := c.ensureManifest(ctx, a); err != nil {
			return nil, err
		}
		for _, cat := range a.manifest.Catalogs {
			if !cat.SupportsSearch() {
				continue
			}
			u := a.baseURL + "/catalog/" + cat.Type + "/" + cat.ID + "/search=" + url.PathEscape(query) + ".json"
			var resp struct {
				Metas []MetaPreview `json:"metas"`
			}
			if err := c.getJSON(ctx, u, &resp); err != nil {
				// A search-declared catalog may still refuse a particular query;
				// treat that as "no matches here", not a failed search.
				continue
			}
			for _, m := range resp.Metas {
				if !seen[m.ID] {
					seen[m.ID] = true
					out = append(out, m)
				}
			}
		}
	}
	return out, nil
}

// hasCatalog reports whether a manifest declares a catalog of the given type
// and id.
func hasCatalog(m Manifest, typ, id string) bool {
	for _, cat := range m.Catalogs {
		if cat.Type == typ && cat.ID == id {
			return true
		}
	}
	return false
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

// streamMeta is the release detail parsed out of a stream's descriptive text and
// behaviorHints (ADR 0037): quality, size and swarm health. All fields are
// best-effort — addons pack this into free text, so a miss leaves a field zero.
type streamMeta struct {
	quality   string
	sizeBytes int64
	seeders   int
}

var (
	qualityRe = regexp.MustCompile(`(?i)\b(2160p|4k|1080p|720p|480p|360p)\b`)
	// Torrentio and kin mark seeders with a person glyph before the count.
	seedersRe = regexp.MustCompile(`👤\s*(\d+)`)
	sizeRe    = regexp.MustCompile(`(?i)([\d.]+)\s*(TB|GB|MB)`)
)

// parseStreamMeta teases the release detail out of a stream.
func parseStreamMeta(s Stream) streamMeta {
	text := s.text()
	m := streamMeta{}
	if q := qualityRe.FindString(text); q != "" {
		m.quality = normaliseQuality(q)
	}
	if s.BehaviorHints.VideoSize > 0 {
		m.sizeBytes = s.BehaviorHints.VideoSize
	} else if sz := sizeRe.FindStringSubmatch(text); sz != nil {
		m.sizeBytes = sizeToBytes(sz[1], sz[2])
	}
	if sd := seedersRe.FindStringSubmatch(text); sd != nil {
		m.seeders, _ = strconv.Atoi(sd[1])
	}
	return m
}

// normaliseQuality collapses "4K" onto the resolution label the rest use.
func normaliseQuality(q string) string {
	if strings.EqualFold(q, "4k") {
		return "2160p"
	}
	return strings.ToLower(q)
}

// sizeToBytes converts a parsed "2.3 GB" pair to bytes (decimal units, as
// addons report them).
func sizeToBytes(num, unit string) int64 {
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0
	}
	switch strings.ToUpper(unit) {
	case "TB":
		return int64(f * 1e12)
	case "GB":
		return int64(f * 1e9)
	case "MB":
		return int64(f * 1e6)
	}
	return 0
}

// userAgent identifies this client to addons. It matters for reachability, not
// just courtesy: Cloudflare-fronted addons (Torrentio and many popular stream
// addons) reject Go's default "Go-http-client/1.1" User-Agent with a 403, while
// any honest custom identifier is served. So this is set on every request.
const userAgent = "mosaic-module-stremio/" + moduleVersion

func (c *Client) getJSON(ctx context.Context, url string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
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
