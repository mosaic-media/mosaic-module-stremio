package stremio_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	stremio "github.com/mosaic-media/mosaic-module-stremio"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// These tests run the capability against a hermetic fake Stremio addon over
// httptest and an in-memory ContentService. They prove the module's own
// behaviour — the mapping of Stremio meta and streams onto the Platform's
// graph, and that streams are opt-in by the addon's declared resources. The
// end-to-end path through the Platform registry and real PostgreSQL is a
// separate test in the platform repo.

func TestImportMovie(t *testing.T) {
	server := fakeAddon(withStreams)
	defer server.Close()
	cap := stremio.New(server.Client())
	content := newFakeContent()

	res, err := cap.Import(context.Background(), content, v1.ImportRequest{
		Caller: v1.CallerFromSession("s-1"), Ref: movieRef("tt1254207"), Settings: addonSettings(server.URL),
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if res.AlreadyKnown {
		t.Fatal("a first import must not be AlreadyKnown")
	}
	work := content.nodes[res.WorkID]
	if work.Kind != v1.NodeWork || work.MediaType != v1.MediaMovie {
		t.Fatalf("work kind/media = %q/%q, want work/movie", work.Kind, work.MediaType)
	}
	if work.Title != "Blade Runner 2049" {
		t.Fatalf("work title = %q, want the meta name", work.Title)
	}
	// A film is Work -> feature item, with the stream Part on the item.
	if res.Items != 1 || res.Containers != 0 || res.Parts != 1 {
		t.Fatalf("counts = items %d containers %d parts %d, want 1/0/1", res.Items, res.Containers, res.Parts)
	}
	if len(content.parts) != 1 {
		t.Fatalf("attached %d parts, want 1", len(content.parts))
	}
	part := content.parts[0]
	if part.Location.Scheme != v1.RemoteLocation {
		t.Fatalf("part scheme = %q, want remote", part.Location.Scheme)
	}
	if !strings.HasPrefix(part.Location.Ref, "http") {
		t.Fatalf("part ref = %q, want the stream url", part.Location.Ref)
	}
	if content.nodes[part.NodeID].Kind != v1.NodeItem {
		t.Fatal("the part must attach to an item, never a work or container")
	}
	// The source binding ties the work to its IMDB id.
	if len(content.binds) != 1 || content.binds[0].SourceProvider != "imdb" || content.binds[0].SourceRef != "tt1254207" {
		t.Fatalf("binding = %+v, want imdb/tt1254207", content.binds)
	}
}

func TestImportSeries(t *testing.T) {
	server := fakeAddon(withStreams)
	defer server.Close()
	cap := stremio.New(server.Client())
	content := newFakeContent()

	res, err := cap.Import(context.Background(), content, v1.ImportRequest{
		Caller: v1.CallerFromSession("s-1"), Ref: seriesRef("tt0903747"), Settings: addonSettings(server.URL),
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	work := content.nodes[res.WorkID]
	if work.MediaType != v1.MediaTVSeries {
		t.Fatalf("work media = %q, want tv_series", work.MediaType)
	}
	// The fake serves one season of two episodes: Work -> season -> 2 episodes,
	// a stream Part on each.
	if res.Containers != 1 || res.Items != 2 || res.Parts != 2 {
		t.Fatalf("counts = containers %d items %d parts %d, want 1/2/2", res.Containers, res.Items, res.Parts)
	}
	// Every part hangs off an item node.
	for _, p := range content.parts {
		if content.nodes[p.NodeID].Kind != v1.NodeItem {
			t.Fatalf("part %s attached to a %s, want an item", p.ID, content.nodes[p.NodeID].Kind)
		}
	}
}

func TestImportMetadataOnlyWhenAddonHasNoStreams(t *testing.T) {
	// A meta-only addon: the client never even requests streams, because the
	// manifest does not declare the stream resource. This is the decoupling —
	// metadata without adopting remote streaming.
	server := fakeAddon(metaOnly)
	defer server.Close()
	cap := stremio.New(server.Client())
	content := newFakeContent()

	res, err := cap.Import(context.Background(), content, v1.ImportRequest{
		Caller: v1.CallerFromSession("s-1"), Ref: movieRef("tt1254207"), Settings: addonSettings(server.URL),
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Items != 1 {
		t.Fatalf("items = %d, want the feature item", res.Items)
	}
	if res.Parts != 0 || len(content.parts) != 0 {
		t.Fatalf("parts = %d, want none from a meta-only addon", res.Parts)
	}
}

func TestImportIsIdempotent(t *testing.T) {
	server := fakeAddon(withStreams)
	defer server.Close()
	cap := stremio.New(server.Client())
	content := newFakeContent()
	ctx := context.Background()

	req := v1.ImportRequest{Caller: v1.CallerFromSession("s-1"), Ref: movieRef("tt1254207"), Settings: addonSettings(server.URL)}
	first, err := cap.Import(ctx, content, req)
	if err != nil {
		t.Fatalf("first Import: %v", err)
	}
	second, err := cap.Import(ctx, content, req)
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if !second.AlreadyKnown {
		t.Fatal("a repeated import must report AlreadyKnown")
	}
	if second.WorkID != first.WorkID {
		t.Fatalf("second import work %q != first %q", second.WorkID, first.WorkID)
	}
	if len(content.parts) != 1 {
		t.Fatalf("idempotent import created %d parts, want the first import's 1", len(content.parts))
	}
}

func TestImportRejectsEmptyRef(t *testing.T) {
	cap := stremio.New(nil)
	// An addon is configured so the ref is what's rejected, not the settings.
	_, err := cap.Import(context.Background(), newFakeContent(), v1.ImportRequest{
		Caller: v1.CallerFromSession("s-1"), Ref: v1.ContentRef{NativeType: "movie"}, Settings: addonSettings("http://unused.example"),
	})
	if err == nil {
		t.Fatal("a ref without a native id must be rejected")
	}
}

func TestImportRejectsWhenNoAddonAndDefaultDisabled(t *testing.T) {
	cap := stremio.New(nil)
	// With the bundled default (Cinemeta) there is always an addon to source
	// from, so the only "no addon" state left is opting the default out and
	// configuring nothing — which must still be rejected. (Empty settings would
	// use Cinemeta over the network; this opt-out path stays hermetic.)
	_, err := cap.Import(context.Background(), newFakeContent(), v1.ImportRequest{
		Caller: v1.CallerFromSession("s-1"), Ref: movieRef("tt1254207"),
		Settings: []byte(`{"disableDefaultAddons":true}`),
	})
	if err == nil {
		t.Fatal("an import with the default disabled and no addons configured must be rejected")
	}
}

// addonSettings builds a module-settings document naming the given addon URLs.
// It opts out of the bundled default (Cinemeta) so these tests stay hermetic
// against their fake addon and never reach the network.
func addonSettings(urls ...string) []byte {
	b, _ := json.Marshal(map[string]any{"addons": urls, "disableDefaultAddons": true})
	return b
}

// movieRef and seriesRef build the ContentRef the Platform hands Import for a
// chosen result — the shape a search or catalog browse produced.
func movieRef(id string) v1.ContentRef {
	return v1.ContentRef{Provider: "stremio", NativeID: id, NativeType: "movie", MediaType: v1.MediaMovie, ExternalScheme: "imdb", ExternalID: id}
}

func seriesRef(id string) v1.ContentRef {
	return v1.ContentRef{Provider: "stremio", NativeID: id, NativeType: "series", MediaType: v1.MediaTVSeries, ExternalScheme: "imdb", ExternalID: id}
}

// ---- fake Stremio addon ----

type addonMode int

const (
	withStreams addonMode = iota
	metaOnly
)

// fakeAddon serves a canned manifest, meta and (optionally) stream over HTTP.
func fakeAddon(mode addonMode) *httptest.Server {
	resources := []string{"meta", "stream", "subtitles"}
	if mode == metaOnly {
		resources = []string{"meta"}
	}
	manifest := map[string]interface{}{
		"id":            "org.fake.addon",
		"name":          "Fake Addon",
		"version":       "1.0.0",
		"description":   "A fake addon for tests.",
		"logo":          "http://fake/logo.png",
		"behaviorHints": map[string]interface{}{"configurable": true},
		"resources":     append(append([]string{}, resources...), "catalog"),
		"types":         []string{"movie", "series"},
		// Two catalogs, both search-capable, so search and browse have something
		// to hit. A search-capable catalog declares "search" in its extra.
		"catalogs": []map[string]interface{}{
			{"type": "movie", "id": "top", "name": "Popular Movies", "extra": []map[string]interface{}{{"name": "search"}}},
			{"type": "series", "id": "top", "name": "Popular Series", "extra": []map[string]interface{}{{"name": "search"}}},
		},
	}

	movieMeta := map[string]interface{}{"id": "tt1254207", "type": "movie", "name": "Blade Runner 2049", "poster": "http://img/br", "releaseInfo": "2017"}
	seriesMeta := map[string]interface{}{"id": "tt0903747", "type": "series", "name": "Breaking Bad", "poster": "http://img/bb", "releaseInfo": "2008"}

	handler := func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/manifest.json":
			writeJSON(w, manifest)
		// Catalog listing and search share a prefix; both return meta previews
		// of the type in the path, so browse and search exercise both types.
		case strings.HasPrefix(path, "/catalog/movie/"):
			writeJSON(w, map[string]interface{}{"metas": []map[string]interface{}{movieMeta}})
		case strings.HasPrefix(path, "/catalog/series/"):
			writeJSON(w, map[string]interface{}{"metas": []map[string]interface{}{seriesMeta}})
		case strings.HasPrefix(path, "/meta/movie/"):
			writeJSON(w, map[string]interface{}{"meta": map[string]interface{}{
				"id": "tt1254207", "type": "movie", "name": "Blade Runner 2049",
				"releaseInfo": "2017", "description": "A young blade runner's discovery.",
			}})
		case strings.HasPrefix(path, "/meta/series/"):
			writeJSON(w, map[string]interface{}{"meta": map[string]interface{}{
				"id": "tt0903747", "type": "series", "name": "Breaking Bad",
				"videos": []map[string]interface{}{
					{"id": "tt0903747:1:1", "title": "Pilot", "season": 1, "episode": 1},
					{"id": "tt0903747:1:2", "title": "Cat's in the Bag...", "season": 1, "episode": 2},
				},
			}})
		case strings.HasPrefix(path, "/stream/"):
			// One direct-play stream for whatever id was asked, with the release
			// detail packed into the title the way real addons do (ADR 0037).
			writeJSON(w, map[string]interface{}{"streams": []map[string]interface{}{
				{
					"name":  "Fake",
					"title": "Movie.Name.2017.1080p.BluRay.x264\n👤 45 💾 2.3 GB ⚙️ Source",
					"url":   "http://cdn.example/" + strings.TrimSuffix(path[len("/stream/"):], ".json"),
				},
			}})
		case strings.HasPrefix(path, "/subtitles/"):
			writeJSON(w, map[string]interface{}{"subtitles": []map[string]interface{}{
				{"id": "en-1", "lang": "eng", "url": "http://cdn.example/subs/en.srt"},
				{"id": "es-1", "lang": "spa", "url": "http://cdn.example/subs/es.srt"},
			}})
		default:
			http.NotFound(w, r)
		}
	}
	return httptest.NewServer(http.HandlerFunc(handler))
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ---- in-memory ContentService ----

// fakeContent is a minimal, faithful v1.ContentService: it assigns ids, keeps
// nodes and parts in memory, inherits a child's work id and media type from
// its parent (as the real service does), and resolves FindContentByExternalID
// against stored works — enough to exercise the capability including dedup.
type fakeContent struct {
	seq   int
	nodes map[v1.NodeID]v1.Node
	parts []v1.Part
	binds []v1.BindContentSourceCommand
}

func newFakeContent() *fakeContent {
	return &fakeContent{nodes: make(map[v1.NodeID]v1.Node)}
}

func (f *fakeContent) nextID(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s-%d", prefix, f.seq)
}

func (f *fakeContent) AddContentWork(_ context.Context, cmd v1.AddContentWorkCommand) (v1.AddContentWorkResult, error) {
	id := v1.NodeID(f.nextID("work"))
	n := v1.Node{
		ID: id, WorkID: id, Kind: v1.NodeWork,
		MediaType: cmd.MediaType, Title: cmd.Title, Status: v1.NodeActive,
		ExternalIDs: cmd.ExternalIDs,
	}
	f.nodes[id] = n
	return v1.AddContentWorkResult{Work: n}, nil
}

func (f *fakeContent) AddContentChild(_ context.Context, cmd v1.AddContentChildCommand) (v1.AddContentChildResult, error) {
	parent := f.nodes[cmd.ParentID]
	id := v1.NodeID(f.nextID("node"))
	parentID := cmd.ParentID
	n := v1.Node{
		ID: id, WorkID: parent.WorkID, ParentID: &parentID,
		Kind: cmd.Kind, MediaType: parent.MediaType,
		ContainerType: cmd.ContainerType, ItemType: cmd.ItemType,
		Title: cmd.Title, NaturalOrder: cmd.NaturalOrder, Status: v1.NodeActive,
	}
	f.nodes[id] = n
	return v1.AddContentChildResult{Node: n}, nil
}

func (f *fakeContent) AttachContentPart(_ context.Context, cmd v1.AttachContentPartCommand) (v1.AttachContentPartResult, error) {
	p := v1.Part{
		ID: v1.PartID(f.nextID("part")), NodeID: cmd.NodeID,
		Role: cmd.Role, Location: cmd.Location,
	}
	f.parts = append(f.parts, p)
	return v1.AttachContentPartResult{Part: p}, nil
}

func (f *fakeContent) BindContentSource(_ context.Context, cmd v1.BindContentSourceCommand) (v1.BindContentSourceResult, error) {
	f.binds = append(f.binds, cmd)
	b := v1.SourceBinding{
		ID: v1.SourceBindingID(f.nextID("bind")), NodeID: cmd.NodeID,
		SourceProvider: cmd.SourceProvider, SourceRef: cmd.SourceRef, Status: cmd.Status,
	}
	return v1.BindContentSourceResult{Binding: b}, nil
}

func (f *fakeContent) FindContentByExternalID(_ context.Context, q v1.FindContentByExternalIDQuery) (v1.FindContentByExternalIDResult, error) {
	var out []v1.Node
	for _, n := range f.nodes {
		if !n.IsRoot() || len(n.ExternalIDs) == 0 {
			continue
		}
		ids := map[string]string{}
		if err := json.Unmarshal(n.ExternalIDs, &ids); err != nil {
			continue
		}
		if ids[q.Scheme] == q.Value {
			out = append(out, n)
		}
	}
	return v1.FindContentByExternalIDResult{Nodes: out}, nil
}

// The remaining ContentService methods are not exercised by the capability;
// they are stubbed to satisfy the interface.
func (f *fakeContent) SearchContent(context.Context, v1.SearchContentQuery) (v1.SearchContentResult, error) {
	return v1.SearchContentResult{}, nil
}
func (f *fakeContent) GetContentNode(context.Context, v1.GetContentNodeQuery) (v1.GetContentNodeResult, error) {
	return v1.GetContentNodeResult{}, nil
}
func (f *fakeContent) RelateContent(context.Context, v1.RelateContentCommand) (v1.RelateContentResult, error) {
	return v1.RelateContentResult{}, nil
}
func (f *fakeContent) ResolveContentBinding(context.Context, v1.ResolveContentBindingCommand) (v1.ResolveContentBindingResult, error) {
	return v1.ResolveContentBindingResult{}, nil
}

var _ v1.ContentService = (*fakeContent)(nil)
