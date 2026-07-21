package stremio_test

import (
	"context"
	"testing"

	stremio "github.com/mosaic-media/mosaic-module-stremio"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// These tests exercise the read provider roles (ADR 0027) against the hermetic
// fake addon: search, catalog browse, metadata enrichment and stream
// resolution. None touches a ContentService — reads do not write (ADR 0028).

func TestSearchReturnsVirtualCandidates(t *testing.T) {
	server := fakeAddon(withStreams)
	defer server.Close()
	cap := stremio.New(server.Client())

	resp, err := cap.Search(context.Background(), v1.SearchRequest{
		Caller: v1.CallerFromSession("s-1"), Settings: addonSettings(server.URL), Text: "blade",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Both search-capable catalogs answer, so a movie and a series come back.
	if len(resp.Results) != 2 {
		t.Fatalf("results = %d, want 2 (one per searchable catalog)", len(resp.Results))
	}
	var sawMovie bool
	for _, r := range resp.Results {
		if r.Ref.Provider != "stremio" || r.Ref.ExternalScheme != "imdb" || r.Ref.ExternalID == "" {
			t.Fatalf("result ref not materialisable: %+v", r.Ref)
		}
		if r.Ref.NativeType == "movie" {
			sawMovie = true
			if r.Title != "Blade Runner 2049" || r.Year != 2017 {
				t.Fatalf("movie result = %q (%d), want Blade Runner 2049 (2017)", r.Title, r.Year)
			}
		}
	}
	if !sawMovie {
		t.Fatal("search did not include the movie result")
	}
}

func TestSearchMediaTypeFilter(t *testing.T) {
	server := fakeAddon(withStreams)
	defer server.Close()
	cap := stremio.New(server.Client())

	resp, err := cap.Search(context.Background(), v1.SearchRequest{
		Caller: v1.CallerFromSession("s-1"), Settings: addonSettings(server.URL),
		Text: "blade", MediaType: v1.MediaMovie,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Ref.MediaType != v1.MediaMovie {
		t.Fatalf("filtered results = %+v, want one movie", resp.Results)
	}
}

func TestCatalogsAndItems(t *testing.T) {
	server := fakeAddon(withStreams)
	defer server.Close()
	cap := stremio.New(server.Client())
	ctx := context.Background()
	settings := addonSettings(server.URL)

	cats, err := cap.Catalogs(ctx, v1.CatalogsRequest{Caller: v1.CallerFromSession("s-1"), Settings: settings})
	if err != nil {
		t.Fatalf("Catalogs: %v", err)
	}
	if len(cats.Catalogs) != 2 {
		t.Fatalf("catalogs = %d, want 2", len(cats.Catalogs))
	}
	if cats.Catalogs[0].Name != "Popular Movies" || cats.Catalogs[0].ID != "top" {
		t.Fatalf("first catalog = %+v, want Popular Movies/top", cats.Catalogs[0])
	}

	items, err := cap.CatalogItems(ctx, v1.CatalogItemsRequest{
		Caller: v1.CallerFromSession("s-1"), Settings: settings, CatalogID: "top", NativeType: "movie",
	})
	if err != nil {
		t.Fatalf("CatalogItems: %v", err)
	}
	if len(items.Items) != 1 || items.Items[0].Ref.NativeID != "tt1254207" {
		t.Fatalf("catalog items = %+v, want the movie preview", items.Items)
	}
}

func TestMetadataEnriches(t *testing.T) {
	server := fakeAddon(withStreams)
	defer server.Close()
	cap := stremio.New(server.Client())

	meta, err := cap.Metadata(context.Background(), v1.MetadataRequest{
		Caller: v1.CallerFromSession("s-1"), Settings: addonSettings(server.URL), Ref: movieRef("tt1254207"),
	})
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if meta.Title != "Blade Runner 2049" || meta.Year != 2017 || meta.Overview == "" {
		t.Fatalf("metadata = %+v, want title/year/overview populated", meta)
	}
}

func TestStreamsResolve(t *testing.T) {
	server := fakeAddon(withStreams)
	defer server.Close()
	cap := stremio.New(server.Client())

	resp, err := cap.Streams(context.Background(), v1.StreamRequest{
		Caller: v1.CallerFromSession("s-1"), Settings: addonSettings(server.URL), Ref: movieRef("tt1254207"),
	})
	if err != nil {
		t.Fatalf("Streams: %v", err)
	}
	if len(resp.Streams) != 1 || resp.Streams[0].Location.Scheme != v1.RemoteLocation {
		t.Fatalf("streams = %+v, want one remote location", resp.Streams)
	}
}

func TestStreamsEmptyForMetaOnlyAddon(t *testing.T) {
	server := fakeAddon(metaOnly)
	defer server.Close()
	cap := stremio.New(server.Client())

	resp, err := cap.Streams(context.Background(), v1.StreamRequest{
		Caller: v1.CallerFromSession("s-1"), Settings: addonSettings(server.URL), Ref: movieRef("tt1254207"),
	})
	if err != nil {
		t.Fatalf("Streams: %v", err)
	}
	if len(resp.Streams) != 0 {
		t.Fatalf("streams = %d, want none from a meta-only addon", len(resp.Streams))
	}
}

func TestStreamsCarryReleaseDetail(t *testing.T) {
	server := fakeAddon(withStreams)
	defer server.Close()
	cap := stremio.New(server.Client())

	resp, err := cap.Streams(context.Background(), v1.StreamRequest{
		Caller: v1.CallerFromSession("s-1"), Settings: addonSettings(server.URL), Ref: movieRef("tt1254207"),
	})
	if err != nil {
		t.Fatalf("Streams: %v", err)
	}
	if len(resp.Streams) != 1 {
		t.Fatalf("streams = %d, want 1", len(resp.Streams))
	}
	s := resp.Streams[0]
	// The quality, seeders and size are parsed out of the addon's title (ADR 0037).
	if s.Quality != "1080p" {
		t.Errorf("quality = %q, want 1080p", s.Quality)
	}
	if s.Seeders != 45 {
		t.Errorf("seeders = %d, want 45", s.Seeders)
	}
	if s.SizeBytes != 2_300_000_000 {
		t.Errorf("sizeBytes = %d, want 2.3e9", s.SizeBytes)
	}
}

func TestSubtitlesResolve(t *testing.T) {
	server := fakeAddon(withStreams)
	defer server.Close()
	cap := stremio.New(server.Client())

	resp, err := cap.Subtitles(context.Background(), v1.SubtitlesRequest{
		Caller: v1.CallerFromSession("s-1"), Settings: addonSettings(server.URL), Ref: movieRef("tt1254207"),
	})
	if err != nil {
		t.Fatalf("Subtitles: %v", err)
	}
	if len(resp.Subtitles) != 2 {
		t.Fatalf("subtitles = %d, want 2", len(resp.Subtitles))
	}
	if resp.Subtitles[0].Language != "eng" || resp.Subtitles[0].URL == "" {
		t.Fatalf("first subtitle = %+v, want eng with a url", resp.Subtitles[0])
	}
}

func TestSubtitlesEmptyForMetaOnlyAddon(t *testing.T) {
	server := fakeAddon(metaOnly)
	defer server.Close()
	cap := stremio.New(server.Client())

	resp, err := cap.Subtitles(context.Background(), v1.SubtitlesRequest{
		Caller: v1.CallerFromSession("s-1"), Settings: addonSettings(server.URL), Ref: movieRef("tt1254207"),
	})
	if err != nil {
		t.Fatalf("Subtitles: %v", err)
	}
	if len(resp.Subtitles) != 0 {
		t.Fatalf("subtitles = %d, want none from a meta-only addon", len(resp.Subtitles))
	}
}
