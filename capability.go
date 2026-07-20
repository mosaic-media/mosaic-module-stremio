package stremio

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

const (
	// CapabilityID is the id the Platform registers this module under and a
	// caller names to invoke it.
	CapabilityID = "stremio"
	// moduleVersion is this module's own version, reported in its Manifest.
	moduleVersion = "0.1.0"
	// providerScheme is the external-id scheme and source-binding provider the
	// module keys content under: Stremio content is identified by IMDB id.
	providerScheme = "imdb"
	// streamProvider names the resolving service recorded on a RemoteLocation
	// Part. The bytes are resolved later (the future Remote Media module); the
	// binding only records where the reference came from.
	streamProvider = "stremio"
)

// Capability satisfies the SDK's capability contract. The assertion fails to
// compile if the module drifts from what the Platform invokes.
var _ v1.Capability = (*Capability)(nil)

// Capability is the Stremio addon-source module (ADR 0008's capability
// surface, first populated). It holds only its protocol client and drives
// ContentService; it owns no schema and imports no Platform internals.
type Capability struct {
	client *Client
}

// New builds the capability over a configured protocol client.
func New(client *Client) *Capability {
	return &Capability{client: client}
}

// Manifest is the module's self-declaration.
func (c *Capability) Manifest() v1.Manifest {
	return v1.Manifest{ID: CapabilityID, Version: moduleVersion, Name: "Stremio addon source"}
}

// Import sources the content named by query — "movie/<imdb-id>" or
// "series/<imdb-id>" — from the configured addons and reflects it into the
// Platform. It fetches metadata (required), searches to avoid duplicating,
// creates the Work with an external-id binding, builds the tree, and attaches
// a RemoteLocation Part wherever a stream addon serves one. Metadata alone is
// a complete import; streams are additive.
func (c *Capability) Import(ctx context.Context, svc v1.ContentService, caller v1.Caller, query string) (v1.ImportResult, error) {
	typ, id, err := parseQuery(query)
	if err != nil {
		return v1.ImportResult{}, err
	}

	meta, ok, err := c.client.Meta(ctx, typ, id)
	if err != nil {
		return v1.ImportResult{}, fmt.Errorf("fetch metadata: %w", err)
	}
	if !ok {
		return v1.ImportResult{}, fmt.Errorf("no configured addon served metadata for %s/%s", typ, id)
	}

	// Search existing content: if this id already resolves to a work, return
	// it rather than creating a second copy.
	if existing, ok, err := c.find(ctx, svc, caller, id); err != nil {
		return v1.ImportResult{}, err
	} else if ok {
		return v1.ImportResult{WorkID: existing, AlreadyKnown: true}, nil
	}

	title := meta.Name
	if title == "" {
		title = id
	}
	work, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
		Caller: caller, MediaType: mediaTypeFor(typ), Title: title,
		ExternalIDs: externalIDs(id),
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
		err = c.importMovie(ctx, svc, caller, work.Work.ID, id, &result)
	case "series":
		err = c.importSeries(ctx, svc, caller, work.Work.ID, id, meta, &result)
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
func (c *Capability) importMovie(ctx context.Context, svc v1.ContentService, caller v1.Caller, workID v1.NodeID, id string, result *v1.ImportResult) error {
	item, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
		Caller: caller, ParentID: workID,
		Kind: v1.NodeItem, ItemType: v1.ItemFeature,
		Title: "Feature", NaturalOrder: 1,
	})
	if err != nil {
		return fmt.Errorf("create feature item: %w", err)
	}
	result.Items++
	return c.attachStream(ctx, svc, caller, item.Node.ID, "movie", id, result)
}

// importSeries builds a series as Work -> season container -> episode item,
// grouping the meta's flat video list by season, and attaching each episode's
// stream to its item.
func (c *Capability) importSeries(ctx context.Context, svc v1.ContentService, caller v1.Caller, workID v1.NodeID, id string, meta Meta, result *v1.ImportResult) error {
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
			if err := c.attachStream(ctx, svc, caller, item.Node.ID, "series", episodeID, result); err != nil {
				return err
			}
		}
	}
	return nil
}

// attachStream fetches a stream for the content id and, if a stream addon
// served one, attaches it as a RemoteLocation Part. No stream is not an error:
// a meta-only import creates the tree without Parts.
func (c *Capability) attachStream(ctx context.Context, svc v1.ContentService, caller v1.Caller, itemID v1.NodeID, typ, id string, result *v1.ImportResult) error {
	stream, ok, err := c.client.Stream(ctx, typ, id)
	if err != nil {
		return fmt.Errorf("fetch stream for %s: %w", id, err)
	}
	if !ok {
		return nil
	}
	if _, err := svc.AttachContentPart(ctx, v1.AttachContentPartCommand{
		Caller: caller, NodeID: itemID, Role: v1.PartEdition,
		Location: v1.MediaLocation{Scheme: v1.RemoteLocation, Provider: streamProvider, Ref: stream.Ref()},
	}); err != nil {
		return fmt.Errorf("attach stream part for %s: %w", id, err)
	}
	result.Parts++
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

// parseQuery splits a "type/id" query. The type is the Stremio content type
// (movie, series); the id is the addon's content id, an IMDB id in practice.
func parseQuery(query string) (typ, id string, err error) {
	typ, id, found := strings.Cut(strings.TrimSpace(query), "/")
	if !found || typ == "" || id == "" {
		return "", "", fmt.Errorf("query %q must be of the form type/id, e.g. movie/tt1254207", query)
	}
	return typ, id, nil
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
