// Package stremio is the Stremio addon-source module: the first official
// optional Module, built exactly as a third party would build one. It is its
// own Go module (github.com/mosaic-media/mosaic-module-stremio) importing only
// the published SDK (contracts/platform/v1) and the standard library, and it
// is compiled into the Platform binary and invoked through the capability
// registry (ADR 0007, ADR 0008).
//
// It consumes the Stremio addon protocol as a client: it points at one or more
// addon HTTP endpoints and, guided by each addon's manifest, uses whatever
// resources that addon declares. Metadata (the meta resource) creates the Work
// and its season/episode tree with an external-id source binding; streams (the
// stream resource) attach a RemoteLocation Part. The two are independent — a
// meta-only addon yields metadata with no Parts, so a user can enrich local
// media through Stremio addons without adopting remote streaming. Streams are
// opt-in by which addons are configured, not by the module.
//
// It owns no schema (ADR 0012): everything it does to the graph goes through
// ContentService, acting as the Caller the Platform hands it (ADR 0017). Stream
// locations are snapshotted at import; resolving or transcoding them at play
// time is a separate, future concern (the Remote Media module), deliberately
// not here.
package stremio
