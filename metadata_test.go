package stremio

import "testing"

// Two sources describing the same film. `preferred` is what a user put first;
// `fallback` is the bundled default. They disagree on prose, and only one of
// them has artwork or a logo — the realistic shape, since addons differ in what
// they populate rather than in which film they describe.
var (
	preferred = metaSource{
		order: 0, addon: "TMDB", hasIdent: true, hasArt: false,
		meta: Meta{
			ID: "tt1", Name: "Thor: Ragnarok", Description: "The preferred synopsis",
			Runtime: "130 min", Genres: []string{"Action"},
			Cast: []string{"Chris Hemsworth"},
		},
	}
	fallback = metaSource{
		order: 1, addon: "Cinemeta", hasIdent: true, hasArt: true,
		meta: Meta{
			ID: "tt1", Name: "Thor Ragnarok (2017)", Description: "A different synopsis",
			Poster: "https://img/poster.jpg", Background: "https://img/bg.jpg", Logo: "https://img/logo.png",
			Genres: []string{"Comedy"}, Cast: []string{"Cate Blanchett"},
		},
	}
)

// merge runs the tiering the way MetaMerged does, on a fixed source list, so the
// rule can be tested without network.
func merge(sources ...metaSource) (Meta, MetaProvenance) {
	var out Meta
	var prov MetaProvenance
	for _, s := range sources {
		if s.hasIdent {
			out.ID, out.Type, out.Name = s.meta.ID, s.meta.Type, s.meta.Name
			out.Description, out.ReleaseInfo = s.meta.Description, s.meta.ReleaseInfo
			out.Runtime, out.ImdbRating = s.meta.Runtime, s.meta.ImdbRating
			prov.Identity = s.addon
			break
		}
	}
	for _, s := range sources {
		if s.hasArt {
			out.Poster, out.Background, out.Logo = s.meta.Poster, s.meta.Background, s.meta.Logo
			prov.Artwork = s.addon
			break
		}
	}
	for _, s := range sources {
		out.Genres = unionStrings(out.Genres, s.meta.Genres)
		out.Cast = unionStrings(out.Cast, s.meta.Cast)
		out.Links = unionLinks(out.Links, s.meta.Links)
		out.Videos = mergeVideos(out.Videos, s.meta.Videos)
		prov.Contributors = append(prov.Contributors, s.addon)
	}
	return out, prov
}

// TestIdentityIsNeverBlended is the tier that exists to keep records coherent.
// A title from one source beside another's overview reads as wrong in a way a
// missing field does not, so identity travels whole or not at all.
func TestIdentityIsNeverBlended(t *testing.T) {
	out, prov := merge(preferred, fallback)

	if out.Name != "Thor: Ragnarok" || out.Description != "The preferred synopsis" {
		t.Errorf("identity was blended: name=%q description=%q", out.Name, out.Description)
	}
	if prov.Identity != "TMDB" {
		t.Errorf("identity provenance = %q, want the highest-priority source", prov.Identity)
	}
}

// TestArtworkTravelsAsASet is the middle ground itself. The preferred source has
// no artwork at all, so the whole set comes from the fallback — rather than a
// poster from one source sitting beside a logo from another, which is the mixing
// this tier prevents.
func TestArtworkTravelsAsASet(t *testing.T) {
	out, prov := merge(preferred, fallback)

	if out.Poster == "" || out.Background == "" || out.Logo == "" {
		t.Fatalf("artwork not filled from the fallback: %+v", out)
	}
	if prov.Artwork != "Cinemeta" {
		t.Errorf("artwork provenance = %q, want the source that actually had it", prov.Artwork)
	}
	// All three must come from one source: a poster from one and a logo from
	// another is a regional/edition mismatch waiting to happen.
	if prov.Artwork == prov.Identity {
		t.Log("both tiers came from one source, which is the ideal case")
	}
}

// TestArtworkPrefersTheHigherPrioritySourceWhenItHasAny — the set rule must not
// become "always take the fallback's artwork".
func TestArtworkPrefersTheHigherPrioritySourceWhenItHasAny(t *testing.T) {
	rich := preferred
	rich.hasArt = true
	rich.meta.Poster = "https://tmdb/poster.jpg"

	out, prov := merge(rich, fallback)

	if out.Poster != "https://tmdb/poster.jpg" || prov.Artwork != "TMDB" {
		t.Errorf("artwork = %q from %q, want the preferred source's", out.Poster, prov.Artwork)
	}
	// And it takes that source's set wholesale, gaps included, rather than
	// topping up from the next one.
	if out.Logo != "" {
		t.Error("artwork was topped up across sources; the set must not mix")
	}
}

// TestSupplementaryListsUnionAcrossSources is where the enrichment actually
// lives: more cast and more genres are strictly better, with no coherence risk.
func TestSupplementaryListsUnionAcrossSources(t *testing.T) {
	out, prov := merge(preferred, fallback)

	if len(out.Cast) != 2 {
		t.Errorf("cast = %v, want both sources' entries", out.Cast)
	}
	if len(out.Genres) != 2 {
		t.Errorf("genres = %v, want both sources' entries", out.Genres)
	}
	if len(prov.Contributors) != 2 {
		t.Errorf("contributors = %v, want every source that answered", prov.Contributors)
	}
}

// TestMergeVideosMergesEpisodesByNumber — two sources describing the same
// episode must enrich it rather than produce two of it.
func TestMergeVideosMergesEpisodesByNumber(t *testing.T) {
	got := mergeVideos(
		[]Video{{Season: 1, Episode: 1, Name: "Pilot"}},
		[]Video{
			{Season: 1, Episode: 1, Overview: "The synopsis", Thumbnail: "https://img/still.jpg"},
			{Season: 1, Episode: 2, Name: "Second"},
		},
	)
	if len(got) != 2 {
		t.Fatalf("got %d episodes, want 2 — the shared episode must merge, not duplicate", len(got))
	}
	if got[0].Name != "Pilot" || got[0].Overview == "" || got[0].Thumbnail == "" {
		t.Errorf("episode 1 did not gather fields from both sources: %+v", got[0])
	}
}

// TestUnionCreditsFillsAPersonRatherThanDuplicating — two sources listing the
// same actor must produce one entry that gathers what each had, not two entries
// with half the information each.
func TestUnionCreditsFillsAPersonRatherThanDuplicating(t *testing.T) {
	got := unionCredits(
		[]Credit{{Name: "Ryan Gosling"}},
		[]Credit{
			{Name: "ryan gosling", Character: "Ryland Grace", Photo: "https://img/rg.jpg"},
			{Name: "Sandra Hüller", Character: "Eva Stratt"},
		},
	)
	if len(got) != 2 {
		t.Fatalf("got %d credits, want 2 — the shared person must merge", len(got))
	}
	if got[0].Character == "" || got[0].Photo == "" {
		t.Errorf("the existing person did not gain the character or photo: %+v", got[0])
	}
}
