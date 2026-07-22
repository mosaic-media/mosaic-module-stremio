package stremio

import "testing"

// TestCandidateKeyIsStableAcrossRefetches is the assertion the whole refresh
// rests on. A debrid link is minted per request, so the same release comes back
// with a different URL every time; keying on the location would match nothing
// already stored and re-attach the entire listing on each refresh.
func TestCandidateKeyIsStableAcrossRefetches(t *testing.T) {
	const release = "Thor.Ragnarok.2017.1080p.x264.AAC.mkv"

	first := candidateKey(release, "https://cdn.example/dl/AAAA1111/file.mkv?exp=1")
	second := candidateKey(release, "https://cdn.example/dl/BBBB2222/file.mkv?exp=9")

	if first != second {
		t.Fatalf("same release keyed differently across fetches:\n  %q\n  %q", first, second)
	}
}

// TestCandidateKeyFallsBackToInfoHash covers the source that gives no filename.
// A magnet's info hash identifies the file where the surrounding trackers and
// parameters do not, so it survives a re-listing that reorders them.
func TestCandidateKeyFallsBackToInfoHash(t *testing.T) {
	a := candidateKey("", "magnet:?xt=urn:btih:0123456789ABCDEF&dn=Thor&tr=udp://one")
	b := candidateKey("", "magnet:?xt=urn:btih:0123456789abcdef&tr=udp://two&dn=Thor")

	if a != b || a == "" {
		t.Fatalf("info-hash key not stable: %q vs %q", a, b)
	}
}

// TestCandidateKeyDistinguishesDifferentReleases guards the opposite failure: a
// key so loose that two releases collide would silently hide one of them, which
// is worse than a duplicate because it cannot be seen.
func TestCandidateKeyDistinguishesDifferentReleases(t *testing.T) {
	a := candidateKey("Thor.Ragnarok.2017.1080p.x264.AAC.mkv", "https://cdn.example/a")
	b := candidateKey("Thor.Ragnarok.2017.2160p.HEVC.EAC3.mkv", "https://cdn.example/b")

	if a == b {
		t.Fatal("two different releases produced the same key")
	}
}

// TestCandidateKeyIgnoresLabelCasingAndPadding — the same release arriving with
// different whitespace or casing must not read as new.
func TestCandidateKeyIgnoresLabelCasingAndPadding(t *testing.T) {
	if candidateKey("  Thor.Ragnarok.MKV ", "x") != candidateKey("thor.ragnarok.mkv", "y") {
		t.Error("keying is sensitive to casing or padding it should normalise away")
	}
}

func TestInfoHashExtraction(t *testing.T) {
	cases := map[string]string{
		"magnet:?xt=urn:btih:ABC123&dn=x": "abc123",
		"magnet:?dn=x&xt=urn:btih:abc123": "abc123",
		"https://cdn.example/file.mkv":    "",
		"":                                "",
	}
	for ref, want := range cases {
		if got := infoHash(ref); got != want {
			t.Errorf("infoHash(%q) = %q, want %q", ref, got, want)
		}
	}
}

// TestSelectCandidatesSpansResolutions is the bug that cost the most time,
// pinned. A flat head-of-list cap took the first N of a listing an aggregator
// had ranked by quality descending — so it kept only the largest, least playable
// releases and threw away every smaller one. Selection then had nothing it could
// play, and the symptom reported was a browser rendering Dolby Vision as purple
// and green.
func TestSelectCandidatesSpansResolutions(t *testing.T) {
	var listing []Stream
	// The real shape: 4K first and plentiful, everything else below it.
	for i := 0; i < 99; i++ {
		listing = append(listing, Stream{Title: "Film 2160p HDR DV release " + string(rune('a'+i%26))})
	}
	for i := 0; i < 141; i++ {
		listing = append(listing, Stream{Title: "Film 1080p x264 AAC release " + string(rune('a'+i%26))})
	}
	for i := 0; i < 38; i++ {
		listing = append(listing, Stream{Title: "Film 720p x264 AAC release " + string(rune('a'+i%26))})
	}

	got := selectCandidates(listing)

	counts := map[string]int{}
	for _, s := range got {
		counts[parseStreamMeta(s).quality]++
	}
	for _, q := range []string{"2160p", "1080p", "720p"} {
		if counts[q] == 0 {
			t.Errorf("no %s candidates kept: %v — a head-of-list cap is what produced this", q, counts)
		}
		if counts[q] > perQualityCandidates {
			t.Errorf("%s kept %d, more than the per-quality cap %d", q, counts[q], perQualityCandidates)
		}
	}
}

// TestSelectCandidatesKeepsUnparsedReleases — the parse is best-effort, and a
// release whose resolution it cannot read is not thereby a bad release.
// Dropping those would repeat the same mistake on a different axis.
func TestSelectCandidatesKeepsUnparsedReleases(t *testing.T) {
	got := selectCandidates([]Stream{
		{Title: "Film 1080p x264"},
		{Title: "Some release with no resolution in its name"},
	})
	if len(got) != 2 {
		t.Fatalf("kept %d of 2; an unparsed release must not be discarded", len(got))
	}
}

// TestSelectCandidatesPreservesSourceOrderWithinAQuality — within one
// resolution the source knows best, so its ranking must survive the sampling.
func TestSelectCandidatesPreservesSourceOrderWithinAQuality(t *testing.T) {
	got := selectCandidates([]Stream{
		{Title: "first 1080p", BehaviorHints: behaviorHints{Filename: "a.mkv"}},
		{Title: "second 1080p", BehaviorHints: behaviorHints{Filename: "b.mkv"}},
	})
	if len(got) != 2 || got[0].BehaviorHints.Filename != "a.mkv" {
		t.Errorf("source order within a quality was not preserved: %+v", got)
	}
}

func TestDedupeSubtitlesCollapsesRepeatedTracks(t *testing.T) {
	got := dedupeSubtitles([]Subtitle{
		{Lang: "eng", URL: "https://s/1.srt"},
		{Lang: "ENG", URL: "https://s/1.srt"},
		{Lang: "eng", URL: "https://s/2.srt"},
		{Lang: "fre", URL: ""},
	})
	if len(got) != 2 {
		t.Errorf("got %d tracks, want 2 (one duplicate and one URL-less entry dropped): %+v", len(got), got)
	}
}
