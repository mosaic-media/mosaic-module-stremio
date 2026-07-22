package stremio

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// Unioning metadata across addons.
//
// The module used to take the first addon that answered, which made the result
// an accident of list order — and with the bundled Cinemeta prepended, it always
// won and every richer source a user had deliberately installed was never even
// asked. Every configured addon is now queried concurrently.
//
// **How the answers combine is a tiered rule, not a flat merge**, and the tiers
// are drawn around what has to stay internally consistent:
//
//   - **Identity** — title, year, overview, runtime, rating — is never blended.
//     It comes whole from the highest-priority source that answered. A record
//     assembled from two sources' prose reads as wrong in a way a gap does not,
//     and this is the behaviour Stremio itself has.
//   - **Artwork** travels as a *set*. Poster, backdrop and logo come from the
//     first source that supplies any of them, so a regional poster never ends up
//     beside another source's English logo — while a source carrying no artwork
//     at all still costs you none.
//   - **Supplementary lists** — cast, genres, episodes — union freely. They are
//     additive by nature: more cast is strictly better, and two sources
//     describing one episode enrich it rather than conflict. This is where the
//     actual enrichment lives.
//
// The risk this guards against is narrower than it first looks: every addon is
// asked about the same IMDB id, so they describe the same title. What differs is
// edition, region and language variants — which is exactly what the artwork tier
// keeps from mixing.

// metaSource pairs one addon's answer with its configured rank.
type metaSource struct {
	order    int
	addon    string
	meta     Meta
	hasArt   bool
	hasIdent bool
}

// MetaMerged asks every addon that serves `meta` and combines their answers by
// the tiered rule above. It also reports which addon supplied each tier, so an
// odd-looking detail screen is an answerable question rather than a guess.
func (c *Client) MetaMerged(ctx context.Context, typ, id string) (Meta, MetaProvenance, bool, error) {
	var (
		mu      sync.Mutex
		sources []metaSource
		wg      sync.WaitGroup
	)

	for _, a := range c.addons {
		// The manifest is fetched inside the goroutine too, so a slow manifest
		// costs the same as a slow meta call rather than serialising ahead of it.
		wg.Add(1)
		go func(a *resolvedAddon) {
			defer wg.Done()
			if err := c.ensureManifest(ctx, a); err != nil {
				return
			}
			if !supports(a.manifest, "meta", typ, id) {
				return
			}
			var resp struct {
				Meta Meta `json:"meta"`
			}
			if err := c.getJSON(ctx, a.baseURL+"/meta/"+typ+"/"+id+".json", &resp); err != nil {
				return
			}
			m := resp.Meta
			if m.ID == "" && m.Name == "" {
				return
			}
			mu.Lock()
			sources = append(sources, metaSource{
				order: a.order, addon: addonLabel(a),
				meta:     m,
				hasArt:   m.Poster != "" || m.Background != "" || m.Logo != "",
				hasIdent: strings.TrimSpace(m.Name) != "",
			})
			mu.Unlock()
		}(a)
	}
	wg.Wait()

	if len(sources) == 0 {
		return Meta{}, MetaProvenance{}, false, nil
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].order < sources[j].order })

	var (
		out  Meta
		prov MetaProvenance
	)

	// Identity: whole, from the first source that has one. Not field by field —
	// a title from one source and an overview from another is the blend this
	// tier exists to prevent.
	for _, s := range sources {
		if s.hasIdent {
			out.ID, out.Type, out.Name = s.meta.ID, s.meta.Type, s.meta.Name
			out.Description, out.ReleaseInfo = s.meta.Description, s.meta.ReleaseInfo
			out.Runtime, out.ImdbRating = s.meta.Runtime, s.meta.ImdbRating
			prov.Identity = s.addon
			break
		}
	}

	// Artwork: as a set, from the first source that has any of it.
	for _, s := range sources {
		if s.hasArt {
			out.Poster, out.Background, out.Logo = s.meta.Poster, s.meta.Background, s.meta.Logo
			prov.Artwork = s.addon
			break
		}
	}

	// Supplementary lists: union across everything, in priority order so the
	// preferred source's entries lead.
	for _, s := range sources {
		out.Genres = unionStrings(out.Genres, s.meta.Genres)
		out.Cast = unionStrings(out.Cast, s.meta.Cast)
		out.Links = unionLinks(out.Links, s.meta.Links)
		out.Videos = mergeVideos(out.Videos, s.meta.Videos)
		// Credits union like any other supplementary list. They are also the one
		// place a photo can come from, so a source that has them contributes
		// even when a higher-priority source supplied the identity.
		out.AppExtras.Cast = unionCredits(out.AppExtras.Cast, s.meta.AppExtras.Cast)
		out.AppExtras.Directors = unionCredits(out.AppExtras.Directors, s.meta.AppExtras.Directors)
		out.AppExtras.Writers = unionCredits(out.AppExtras.Writers, s.meta.AppExtras.Writers)
		prov.Contributors = append(prov.Contributors, s.addon)
	}

	return out, prov, true, nil
}

// MetaProvenance records which addon supplied each tier of a merged record.
//
// It costs almost nothing and turns "why does this look odd" into something
// answerable. Metadata assembled from several sources is exactly the kind of
// thing that is hard to debug after the fact without it.
type MetaProvenance struct {
	Identity     string
	Artwork      string
	Contributors []string
}

// addonLabel names an addon for provenance — its manifest name where it has
// one, otherwise its host, which is enough to tell two sources apart.
func addonLabel(a *resolvedAddon) string {
	if a.manifest.Name != "" {
		return a.manifest.Name
	}
	base := strings.TrimPrefix(strings.TrimPrefix(a.baseURL, "https://"), "http://")
	if i := strings.IndexByte(base, '/'); i > 0 {
		base = base[:i]
	}
	return base
}

// unionStrings appends what is new, case-insensitively, preserving order.
func unionStrings(dst, src []string) []string {
	seen := make(map[string]bool, len(dst))
	for _, s := range dst {
		seen[strings.ToLower(strings.TrimSpace(s))] = true
	}
	for _, s := range src {
		k := strings.ToLower(strings.TrimSpace(s))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		dst = append(dst, s)
	}
	return dst
}

// unionLinks merges the links array, which is where cast and crew live. Deduped
// on category plus name, so the same actor from two sources appears once.
func unionLinks(dst, src []Link) []Link {
	key := func(l Link) string { return strings.ToLower(l.Category + "|" + l.Name) }
	seen := make(map[string]bool, len(dst))
	for _, l := range dst {
		seen[key(l)] = true
	}
	for _, l := range src {
		if l.Name == "" || seen[key(l)] {
			continue
		}
		seen[key(l)] = true
		dst = append(dst, l)
	}
	return dst
}

// mergeVideos merges episode lists by season and episode number rather than
// appending, so two sources describing the same episode enrich it instead of
// duplicating it — a still from one and a synopsis from the other.
func mergeVideos(dst, src []Video) []Video {
	index := make(map[[2]int]int, len(dst))
	for i, v := range dst {
		index[[2]int{v.Season, v.Episode}] = i
	}
	for _, v := range src {
		k := [2]int{v.Season, v.Episode}
		if i, ok := index[k]; ok {
			coalesce(&dst[i].Name, v.Name)
			coalesce(&dst[i].Title, v.Title)
			coalesce(&dst[i].Overview, v.Overview)
			coalesce(&dst[i].Thumbnail, v.Thumbnail)
			coalesce(&dst[i].Released, v.Released)
			continue
		}
		index[k] = len(dst)
		dst = append(dst, v)
	}
	return dst
}

// coalesce fills a string only when it is empty. Used within an episode, where
// the fields are independent facts about one thing rather than a coherent set.
func coalesce(dst *string, src string) {
	if strings.TrimSpace(*dst) == "" {
		*dst = src
	}
}

// unionCredits merges credit lists by person, filling a missing photo or
// character from a later source rather than duplicating the person.
func unionCredits(dst, src []Credit) []Credit {
	index := make(map[string]int, len(dst))
	for i, c := range dst {
		index[strings.ToLower(strings.TrimSpace(c.Name))] = i
	}
	for _, c := range src {
		k := strings.ToLower(strings.TrimSpace(c.Name))
		if k == "" {
			continue
		}
		if i, ok := index[k]; ok {
			coalesce(&dst[i].Character, c.Character)
			coalesce(&dst[i].Photo, c.Photo)
			continue
		}
		index[k] = len(dst)
		dst = append(dst, c)
	}
	return dst
}
