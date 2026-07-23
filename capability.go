package stremio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

const (
	// CapabilityID is the id the Platform registers this module under and a
	// caller names to invoke it.
	CapabilityID = "stremio"
	// modulePath is this module's import path, which is how it looks its own
	// version up in the build graph rather than carrying a constant that has to
	// be remembered at release time (SDK v0.12.0). The constant this replaces
	// had drifted across two releases — it read 0.7.0 while the repository was
	// tagged v0.9.0, so the Platform logged a version that had not been true for
	// months.
	modulePath = "github.com/mosaic-media/module-stremio-addons"
	// providerScheme is the external-id scheme and source-binding provider the
	// module keys content under: Stremio content is identified by IMDB id.
	providerScheme = "imdb"
	// streamProvider names the resolving service recorded on a RemoteLocation
	// Part. The bytes are resolved later (the future Remote Media module); the
	// binding only records where the reference came from.
	streamProvider = "stremio"
	// addonCatalogSource is Stremio's official addon directory, used *only* to
	// populate the browse grid (ADR 0038) — never as a content source.
	//
	// Cinemeta used to be bundled here as a metadata default so that metadata and
	// search worked with no configuration (ADR 0035). It is not any more:
	// `module-cinemeta` is that guarantee now, as a core module that cannot be
	// switched off or misconfigured (ADR 0072), and leaving a second Cinemeta in
	// this module's sourcing list would show every title twice in search.
	//
	// What the bundled default *also* quietly provided was the `addon_catalog`
	// resource, which is the entire reason a user can browse installable addons
	// rather than having to paste a manifest URL. That is a discovery surface, not
	// a content source, so it survives the removal — pointed at directly by
	// browseSection and never merged into the addons the provider roles read.
	addonCatalogSource = "https://v3-cinemeta.strem.io/manifest.json"
)

// moduleVersion is resolved once from the build graph rather than maintained by
// hand (SDK v0.12.0). A var rather than a const because it is a fact about the
// binary, discovered at startup, not a literal — which is also why the constant
// it replaces could drift: nothing forced it to agree with anything.
var moduleVersion = v1.ModuleVersion(modulePath)

// Capability satisfies the SDK's capability contract and every provider role it
// declares in its Manifest. The assertions fail to compile if the module drifts
// from what the Platform invokes or from a role it claims to fill (ADR 0027).
var (
	_ v1.Capability         = (*Capability)(nil)
	_ v1.MetadataProvider   = (*Capability)(nil)
	_ v1.SearchProvider     = (*Capability)(nil)
	_ v1.CatalogProvider    = (*Capability)(nil)
	_ v1.StreamProvider     = (*Capability)(nil)
	_ v1.SubtitlesProvider  = (*Capability)(nil)
	_ v1.SettingsUIProvider = (*Capability)(nil)
)

// Capability is the Stremio addon-source module (ADR 0008's capability
// surface, first populated). It holds only an HTTP client; the addons it sources
// from are whatever a user adds through its settings, handed in at invocation
// time (ADR 0021), so one registered module serves whatever each user
// configures. It owns no schema and imports no Platform internals.
//
// It bundles no addon of its own. It used to bundle Cinemeta so that a fresh
// install had metadata (ADR 0035); that guarantee now belongs to
// `module-cinemeta`, a core module that cannot be switched off (ADR 0072), and
// this module is purely what a user chose.
type Capability struct {
	httpClient *http.Client
}

// New builds the capability over an HTTP client (nil for a default). Addon URLs
// are not supplied here — they arrive as settings on each invocation.
func New(httpClient *http.Client) *Capability {
	return &Capability{httpClient: httpClient}
}

// moduleSettings is the shape the Stremio module reads from its user-managed
// settings document: the list of Stremio addon base URLs to source from.
//
// A document written before the bundled default was removed may still carry a
// `disableDefaultAddons` key. Unknown fields are ignored, so such a document
// keeps working and simply means what it says — this module sources only the
// addons named — with no migration to run.
type moduleSettings struct {
	Addons []string `json:"addons"`
}

// addonsFrom parses the module's settings document into a clean addon list.
func addonsFrom(settings []byte) ([]string, error) {
	if len(settings) == 0 {
		return nil, nil
	}
	var s moduleSettings
	if err := json.Unmarshal(settings, &s); err != nil {
		return nil, fmt.Errorf("parse module settings: %w", err)
	}
	var out []string
	for _, a := range s.Addons {
		if t := strings.TrimSpace(a); t != "" {
			out = append(out, t)
		}
	}
	return out, nil
}

// clientFrom builds a client over the addons the user configured, deduped by
// base URL and left in configured order — which is the priority rule the
// metadata merge reads (metadataPriority).
//
// No addons is now the state of a fresh install rather than an edge case, and it
// is an error: every provider role needs something to ask. The Platform skips a
// search provider that errors rather than failing the search, so this reads as
// "this module contributes nothing yet", which is the truth. The settings screen
// renders without going through here, so the path to fixing it stays open.
func (c *Capability) clientFrom(settings []byte) (*Client, error) {
	configured, err := addonsFrom(settings)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(configured))
	addons := make([]string, 0, len(configured))
	for _, u := range configured {
		base := normaliseAddonURL(u)
		if base == "" || seen[base] {
			continue
		}
		seen[base] = true
		addons = append(addons, u)
	}
	if len(addons) == 0 {
		return nil, fmt.Errorf(`no Stremio addons configured; add one in Settings › Stremio addons, or with configureModule (settings {"addons":["<manifest-url>"]})`)
	}
	return NewClient(c.httpClient, addons...), nil
}

// Manifest is the module's self-declaration, including the provider roles it
// fills (ADR 0027). It sources metadata and searches and browses catalogs, and
// resolves streams — the four Stremio addon resources.
func (c *Capability) Manifest() v1.Manifest {
	return v1.Manifest{
		ID: CapabilityID, Version: v1.ModuleVersion(modulePath), Name: "Stremio addon source",
		Provides: []v1.Role{v1.RoleMetadata, v1.RoleSearch, v1.RoleCatalog, v1.RoleStream, v1.RoleSubtitles, v1.RoleSettingsUI},
	}
}

// Import materialises the virtual item named by req.Ref — a result a search or
// catalog browse produced (ADR 0028) — from the configured addons into the
// Platform. It fetches metadata (required), searches to avoid duplicating,
// creates the Work with an external-id binding, builds the tree, and attaches
// a RemoteLocation Part wherever a stream addon serves one. Metadata alone is
// a complete import; streams are additive.
func (c *Capability) Import(ctx context.Context, svc v1.ContentService, req v1.ImportRequest) (v1.ImportResult, error) {
	client, err := c.clientFrom(req.Settings)
	if err != nil {
		return v1.ImportResult{}, err
	}
	caller := req.Caller

	typ, id := req.Ref.NativeType, req.Ref.NativeID
	if typ == "" || id == "" {
		return v1.ImportResult{}, fmt.Errorf("ref needs a native type and id, got type=%q id=%q", typ, id)
	}

	meta, _, ok, err := client.MetaMerged(ctx, typ, id)
	if err != nil {
		return v1.ImportResult{}, fmt.Errorf("fetch metadata: %w", err)
	}
	if !ok {
		return v1.ImportResult{}, fmt.Errorf("no configured addon served metadata for %s/%s", typ, id)
	}

	// Search existing content: if this id already resolves to a work, refresh
	// its candidate releases rather than creating a second copy.
	//
	// It used to return here and do nothing else, which quietly stranded every
	// item imported before the module stored a whole candidate set: a re-import
	// was a no-op, so the item kept its single release forever and selection had
	// nothing to choose between. That is indistinguishable, from outside, from
	// selection being broken.
	if existing, ok, err := c.find(ctx, svc, caller, id); err != nil {
		return v1.ImportResult{}, err
	} else if ok {
		result := v1.ImportResult{WorkID: existing, AlreadyKnown: true}
		if err := c.refreshCandidates(ctx, client, svc, caller, existing, typ, id, &result); err != nil {
			return v1.ImportResult{}, err
		}
		return result, nil
	}

	title := meta.Name
	if title == "" {
		title = id
	}
	work, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
		Caller: caller, MediaType: mediaTypeFor(typ), Title: title,
		ExternalIDs: externalIDs(id),
		// Store the art alongside the work so a list surface (the continue-
		// watching rail) renders it without re-fetching metadata per card, and so
		// it can later be user-overridden (ADR 0071). These are the same fields
		// the metadata read already decodes.
		Artwork: v1.Artwork{
			Poster: meta.Poster, Landscape: meta.LandscapePoster,
			Backdrop: meta.Background, Logo: meta.Logo,
		},
	})
	if err != nil {
		return v1.ImportResult{}, fmt.Errorf("create work: %w", err)
	}
	result := v1.ImportResult{WorkID: work.Work.ID}

	if _, err := svc.BindContentSource(ctx, v1.BindContentSourceCommand{
		Caller: caller, NodeID: work.Work.ID,
		SourceProvider: providerScheme, SourceRef: id,
		MatchConfidence: 1, MatchMethod: v1.MatchExternalIDExact, Status: v1.BindingConfirmed,
	}); err != nil {
		return v1.ImportResult{}, fmt.Errorf("bind source: %w", err)
	}

	switch typ {
	case "movie":
		err = c.importMovie(ctx, client, svc, caller, work.Work.ID, id, &result)
	case "series":
		err = c.importSeries(ctx, client, svc, caller, work.Work.ID, id, meta, &result)
	default:
		// An unknown type still has a Work and a binding; there is simply no
		// tree shape defined for it here, so it lands as a bare work.
	}
	if err != nil {
		return v1.ImportResult{}, err
	}

	return result, nil
}

// importMovie builds a film as Work -> feature item, attaching the stream to
// the item (a Part attaches to an item, never a work — ADR 0013).
func (c *Capability) importMovie(ctx context.Context, client *Client, svc v1.ContentService, caller v1.Caller, workID v1.NodeID, id string, result *v1.ImportResult) error {
	item, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
		Caller: caller, ParentID: workID,
		Kind: v1.NodeItem, ItemType: v1.ItemFeature,
		Title: "Feature", NaturalOrder: 1,
	})
	if err != nil {
		return fmt.Errorf("create feature item: %w", err)
	}
	result.Items++
	return c.attachStream(ctx, client, svc, caller, item.Node.ID, "movie", id, result)
}

// importSeries builds a series as Work -> season container -> episode item,
// grouping the meta's flat video list by season, and attaching each episode's
// stream to its item.
func (c *Capability) importSeries(ctx context.Context, client *Client, svc v1.ContentService, caller v1.Caller, workID v1.NodeID, id string, meta Meta, result *v1.ImportResult) error {
	for _, season := range groupBySeason(meta.Videos) {
		container, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
			Caller: caller, ParentID: workID,
			Kind: v1.NodeContainer, ContainerType: v1.ContainerSeason,
			Title: fmt.Sprintf("Season %d", season.number), NaturalOrder: float64(season.number),
		})
		if err != nil {
			return fmt.Errorf("create season %d: %w", season.number, err)
		}
		result.Containers++

		for _, ep := range season.episodes {
			item, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
				Caller: caller, ParentID: container.Node.ID,
				Kind: v1.NodeItem, ItemType: v1.ItemEpisode,
				Title: ep.EpisodeTitle(), NaturalOrder: float64(ep.Episode),
			})
			if err != nil {
				return fmt.Errorf("create episode %d of season %d: %w", ep.Episode, season.number, err)
			}
			result.Items++

			episodeID := fmt.Sprintf("%s:%d:%d", id, season.number, ep.Episode)
			if err := c.attachStream(ctx, client, svc, caller, item.Node.ID, "series", episodeID, result); err != nil {
				return err
			}
		}
	}
	return nil
}

// attachStream fetches every stream a configured addon offers for the content id
// and attaches each as a RemoteLocation Part. No stream is not an error: a
// meta-only import creates the tree without Parts.
//
// All of them, not the first (ADR 0049). A source returns dozens of releases for
// one film, differing in container, codec, language and size, and which one a
// viewer can actually play depends on the client asking — a fact not available
// at import time and different for two clients of the same install. Storing one
// bakes in an answer to a question that had not been asked. Candidates never
// expire, so keeping the whole set costs nothing to keep correct; only the
// resolved URL is perishable, and that is cached elsewhere.
//
// Each carries the release detail parsed at this boundary (ADR 0051), so a
// consumer ranks on typed fields rather than re-deriving them from a URL.
func (c *Capability) attachStream(ctx context.Context, client *Client, svc v1.ContentService, caller v1.Caller, itemID v1.NodeID, typ, id string, result *v1.ImportResult) error {
	streams, err := client.Streams(ctx, typ, id)
	if err != nil {
		return fmt.Errorf("fetch streams for %s: %w", id, err)
	}
	streams = selectCandidates(streams)
	for i, stream := range streams {
		meta := parseStreamMeta(stream)
		if _, err := svc.AttachContentPart(ctx, v1.AttachContentPartCommand{
			Caller: caller, NodeID: itemID, Role: v1.PartEdition,
			// The release name is the edition label: it is what a source picker
			// shows a user choosing between two candidates by hand.
			EditionLabel: releaseLabel(stream),
			// NaturalOrder preserves the source's own ranking, so a consumer
			// that expresses no preference still gets the order the addon
			// intended rather than whatever the database returns.
			NaturalOrder: float64(i),
			Location:     v1.MediaLocation{Scheme: v1.RemoteLocation, Provider: streamProvider, Ref: stream.Ref()},
			Container:    meta.container,
			VideoCodec:   meta.videoCodec,
			AudioCodec:   meta.audioCodec,
			Width:        meta.width,
			Height:       meta.height,
			SizeBytes:    meta.sizeBytes,
		}); err != nil {
			return fmt.Errorf("attach stream part for %s: %w", id, err)
		}
		result.Parts++
	}
	return nil
}

// find looks for an existing work already bound to the IMDB id.
func (c *Capability) find(ctx context.Context, svc v1.ContentService, caller v1.Caller, id string) (v1.NodeID, bool, error) {
	found, err := svc.FindContentByExternalID(ctx, v1.FindContentByExternalIDQuery{
		Caller: caller, Scheme: providerScheme, Value: id,
	})
	if err != nil {
		return "", false, fmt.Errorf("search existing content: %w", err)
	}
	for _, node := range found.Nodes {
		if node.IsRoot() {
			return node.ID, true, nil
		}
	}
	return "", false, nil
}

// Metadata resolves descriptive detail for a ref (RoleMetadata — the addon
// `meta` resource). It is the enrichment surface: the descriptive fields, not
// the containment tree, which Import builds where the source's structure is
// known (ADR 0027).
func (c *Capability) Metadata(ctx context.Context, req v1.MetadataRequest) (v1.ContentMetadata, error) {
	client, err := c.clientFrom(req.Settings)
	if err != nil {
		return v1.ContentMetadata{}, err
	}
	meta, prov, ok, err := client.MetaMerged(ctx, req.Ref.NativeType, req.Ref.NativeID)
	if err != nil {
		return v1.ContentMetadata{}, fmt.Errorf("fetch metadata: %w", err)
	}
	if !ok {
		return v1.ContentMetadata{}, fmt.Errorf("no configured addon served metadata for %s/%s", req.Ref.NativeType, req.Ref.NativeID)
	}
	// Which source supplied what, recorded once per lookup. A record assembled
	// from several addons is the kind of thing that is hard to explain after the
	// fact, and "the artwork came from Cinemeta because your first addon had
	// none" is a far better answer than a shrug.
	//
	// Through the SDK's telemetry rather than log.Printf: this lands in the
	// Platform's records, attributed to this module, correlated with the request
	// that caused it, and filterable. The addon names are the interesting part
	// and they are a user's configuration — an addon URL can carry an API key —
	// so they are classified rather than written verbatim. The type and id are
	// a provider's own identifiers and are safe.
	v1.TelemetryFrom(ctx).Info("metadata merged",
		v1.String("native_type", req.Ref.NativeType),
		v1.String("native_id", req.Ref.NativeID),
		v1.Sensitive("identity_from", prov.Identity),
		v1.Sensitive("artwork_from", prov.Artwork),
		v1.Int("contributors", len(prov.Contributors)))
	return v1.ContentMetadata{
		Ref:      req.Ref,
		Title:    meta.Name,
		Year:     parseYear(meta.ReleaseInfo),
		Overview: meta.Description,
		Poster:   meta.Poster,
		Backdrop: meta.Background,
		Logo:     meta.Logo,
		Genres:   meta.Genres,
		Rating:   parseRating(meta.ImdbRating),
		Runtime:  meta.Runtime,
		Cast:     castOf(meta),
		Episodes: episodesOf(meta.Videos),
	}, nil
}

// castOf reads the cast names from a meta, preferring the modern `links` array
// (category "Cast") and falling back to the legacy top-level `cast` list. Names
// are de-duplicated and capped, since a detail shows the *top* cast, not all of
// it. Cinemeta gives names only, so Role is left empty (ADR 0034).
func castOf(meta Meta) []v1.Person {
	const maxCast = 18
	seen := make(map[string]bool)
	out := make([]v1.Person, 0, maxCast)
	add := func(p v1.Person) {
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" || seen[p.Name] || len(out) >= maxCast {
			return
		}
		seen[p.Name] = true
		out = append(out, p)
	}

	// The rich block first: it is the only source carrying a character name and
	// a photograph, and a cast rail with faces is a different thing from a list
	// of names. `links` can express neither, which is why an addon with better
	// data puts it elsewhere.
	for _, c := range meta.AppExtras.Cast {
		add(v1.Person{Name: c.Name, Role: c.Character, Photo: c.Photo})
	}

	// Then the standard shapes, which fill in for a source that has no rich
	// block at all. A name with no face still belongs on the screen.
	for _, l := range meta.Links {
		if strings.EqualFold(l.Category, "Cast") || strings.EqualFold(l.Category, "actor") {
			add(v1.Person{Name: l.Name})
		}
	}
	for _, name := range meta.Cast {
		add(v1.Person{Name: name})
	}
	return out
}
func episodesOf(videos []Video) []v1.EpisodePreview {
	if len(videos) == 0 {
		return nil
	}
	out := make([]v1.EpisodePreview, 0, len(videos))
	for _, s := range groupBySeason(videos) {
		for _, v := range s.episodes {
			out = append(out, v1.EpisodePreview{
				Season:    s.number,
				Episode:   v.Episode,
				Title:     v.EpisodeTitle(),
				Overview:  v.Overview,
				Thumbnail: v.Thumbnail,
				Released:  v.Released,
			})
		}
	}
	return out
}

// parseRating reads Cinemeta's string rating ("8.0") into a float, 0 when absent
// or unparseable.
func parseRating(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

// Search returns virtual candidates for free text (RoleSearch — the addon
// `catalog/…/search` resource). No raw id: this is what makes user search in
// Mosaic work over source content that is not in the library (ADR 0028).
func (c *Capability) Search(ctx context.Context, req v1.SearchRequest) (v1.SearchResponse, error) {
	client, err := c.clientFrom(req.Settings)
	if err != nil {
		return v1.SearchResponse{}, err
	}
	metas, err := client.Search(ctx, req.Text)
	if err != nil {
		return v1.SearchResponse{}, fmt.Errorf("search: %w", err)
	}
	results := make([]v1.SearchResult, 0, len(metas))
	for _, m := range metas {
		if req.MediaType != "" && mediaTypeFor(m.Type) != req.MediaType {
			continue
		}
		results = append(results, v1.SearchResult{
			Ref: refFrom(m), Title: m.Name, Year: parseYear(m.ReleaseInfo), Poster: m.Poster,
		})
	}
	return v1.SearchResponse{Results: results}, nil
}

// Catalogs lists the collections the configured addons expose (RoleCatalog —
// the addon `catalog` resource), for the admin collection browser.
func (c *Capability) Catalogs(ctx context.Context, req v1.CatalogsRequest) (v1.CatalogsResponse, error) {
	client, err := c.clientFrom(req.Settings)
	if err != nil {
		return v1.CatalogsResponse{}, err
	}
	decls, err := client.Catalogs(ctx)
	if err != nil {
		return v1.CatalogsResponse{}, fmt.Errorf("list catalogs: %w", err)
	}
	cats := make([]v1.Catalog, 0, len(decls))
	for _, d := range decls {
		cats = append(cats, v1.Catalog{ID: d.ID, NativeType: d.Type, Name: catalogName(d)})
	}
	return v1.CatalogsResponse{Catalogs: cats}, nil
}

// CatalogItems lists one collection's entries as virtual candidates the admin
// can select to publish (ADR 0028). It does not touch the object graph.
func (c *Capability) CatalogItems(ctx context.Context, req v1.CatalogItemsRequest) (v1.CatalogItemsResponse, error) {
	client, err := c.clientFrom(req.Settings)
	if err != nil {
		return v1.CatalogItemsResponse{}, err
	}
	metas, err := client.CatalogItems(ctx, req.NativeType, req.CatalogID, req.Skip)
	if err != nil {
		return v1.CatalogItemsResponse{}, fmt.Errorf("list catalog items: %w", err)
	}
	items := make([]v1.CatalogItem, 0, len(metas))
	for _, m := range metas {
		items = append(items, v1.CatalogItem{
			Ref: refFrom(m), Title: m.Name, Year: parseYear(m.ReleaseInfo), Poster: m.Poster,
		})
	}
	return v1.CatalogItemsResponse{Items: items}, nil
}

// Streams resolves playable locations for a materialised item's ref (RoleStream
// — the addon `stream` resource). Import snapshots streams at materialise time;
// this exposes the same resolution as a role other flows can call. It returns an
// empty response, no error, when no addon serves a stream (the meta-only case).
func (c *Capability) Streams(ctx context.Context, req v1.StreamRequest) (v1.StreamResponse, error) {
	client, err := c.clientFrom(req.Settings)
	if err != nil {
		return v1.StreamResponse{}, err
	}
	stream, ok, err := client.Stream(ctx, req.Ref.NativeType, req.Ref.NativeID)
	if err != nil {
		return v1.StreamResponse{}, fmt.Errorf("resolve streams: %w", err)
	}
	if !ok {
		return v1.StreamResponse{}, nil
	}
	return v1.StreamResponse{Streams: []v1.StreamLink{streamLinkFrom(stream)}}, nil
}

// streamLinkFrom maps a Stremio stream to the SDK StreamLink, parsing the release
// detail (quality, size, seeders) a source-picker will rank on (ADR 0037).
func streamLinkFrom(stream Stream) v1.StreamLink {
	meta := parseStreamMeta(stream)
	return v1.StreamLink{
		Label:     stream.Name,
		Title:     stream.Title,
		Quality:   meta.quality,
		SizeBytes: meta.sizeBytes,
		Seeders:   meta.seeders,
		Location:  v1.MediaLocation{Scheme: v1.RemoteLocation, Provider: streamProvider, Ref: stream.Ref()},
	}
}

// Subtitles resolves subtitle tracks for a materialised item's ref (RoleSubtitles
// — the addon `subtitles` resource, ADR 0037). Like Streams it is a source role;
// the consumer is a player that does not exist yet, so this is built ahead of it.
// It returns an empty response, no error, when no addon serves subtitles.
func (c *Capability) Subtitles(ctx context.Context, req v1.SubtitlesRequest) (v1.SubtitlesResponse, error) {
	client, err := c.clientFrom(req.Settings)
	if err != nil {
		return v1.SubtitlesResponse{}, err
	}
	subs, ok, err := client.Subtitles(ctx, req.Ref.NativeType, req.Ref.NativeID)
	if err != nil {
		return v1.SubtitlesResponse{}, fmt.Errorf("resolve subtitles: %w", err)
	}
	if !ok {
		return v1.SubtitlesResponse{}, nil
	}
	out := make([]v1.Subtitle, 0, len(subs))
	for _, s := range subs {
		out = append(out, v1.Subtitle{Language: s.Lang, URL: s.URL, ID: s.ID})
	}
	return v1.SubtitlesResponse{Subtitles: out}, nil
}

// refFrom builds a ContentRef from a catalog/search preview. Stremio content is
// keyed by IMDB id, so the native id doubles as the external id the Platform
// dedups and binds on (ADR 0028).
func refFrom(m MetaPreview) v1.ContentRef {
	return v1.ContentRef{
		Provider: CapabilityID, NativeID: m.ID, NativeType: m.Type,
		MediaType: mediaTypeFor(m.Type), ExternalScheme: providerScheme, ExternalID: m.ID,
	}
}

// parseYear reads the leading year from a Stremio releaseInfo ("2017",
// "2008-2013"), returning 0 when there is none.
func parseYear(s string) int {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return 0
	}
	y, err := strconv.Atoi(s[:4])
	if err != nil {
		return 0
	}
	return y
}

// catalogName is the catalog's declared name, or a type/id fallback when it
// declares none.
func catalogName(d CatalogDecl) string {
	if d.Name != "" {
		return d.Name
	}
	return d.Type + " " + d.ID
}

// mediaTypeFor maps a Stremio content type to a Platform media type, using the
// known constants for the two Stremio types and canonicalising anything else
// as open text (ADR 0015).
func mediaTypeFor(typ string) v1.MediaType {
	switch typ {
	case "movie":
		return v1.MediaMovie
	case "series":
		return v1.MediaTVSeries
	default:
		return v1.NormaliseMediaType(typ)
	}
}

// externalIDs builds the Work's external-id document — the flat scheme-to-id
// shape FindContentByExternalID reads.
func externalIDs(id string) []byte {
	b, _ := json.Marshal(map[string]string{providerScheme: id})
	return b
}

// season groups a series' episodes under one season number.
type season struct {
	number   int
	episodes []Video
}

// groupBySeason collects the meta's flat video list into ordered seasons, each
// with its episodes ordered by episode number.
func groupBySeason(videos []Video) []season {
	byNumber := make(map[int][]Video)
	for _, v := range videos {
		byNumber[v.Season] = append(byNumber[v.Season], v)
	}
	seasons := make([]season, 0, len(byNumber))
	for number, eps := range byNumber {
		sort.Slice(eps, func(i, j int) bool { return eps[i].Episode < eps[j].Episode })
		seasons = append(seasons, season{number: number, episodes: eps})
	}
	sort.Slice(seasons, func(i, j int) bool { return seasons[i].number < seasons[j].number })
	return seasons
}
