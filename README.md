# mosaic-module-stremio

Stremio addon-source module for the [Mosaic](https://github.com/mosaic-media) platform — a Go client of the Stremio addon protocol, built against the Mosaic SDK.

It is an **optional Mosaic module**: its own Go module that imports only
[`mosaic-sdk`](https://github.com/mosaic-media/mosaic-sdk) and the standard
library, compiled into a Mosaic Platform binary and invoked through the
Platform's capability registry. It owns no schema; everything it does goes
through the published `ContentService`.

## What it does

Given one or more Stremio addon base URLs (configured at runtime as module
settings), it consumes the addon protocol as a **client** and reflects content
into a Mosaic library:

- **Metadata** (the addon `meta` resource) creates the Work and its
  season/episode tree with an external-id source binding.
- **Streams** (the addon `stream` resource) attach a `RemoteLocation` Part.

The two are independent and driven by what each addon's manifest declares: a
metadata-only addon yields a library with no stream Parts, so it can enrich
local media without any remote streaming. Movies and TV series are both
supported.

## Configuration

Addons are user-managed settings, set through the Platform at runtime — a JSON
document naming the addons. Either an addon's base URL or the `…/manifest.json`
URL you copy from Stremio works (the `stremio://` install scheme is accepted
too); they are normalised to the same base.

```json
{
  "addons": [
    "https://v3-cinemeta.strem.io/manifest.json",
    "https://torrentio.strem.fun/manifest.json"
  ]
}
```

**Metadata and streams come from different addons.** A metadata addon (e.g.
Cinemeta) serves the `meta` resource and builds the library — Works, seasons,
episodes — but no playable Parts. A stream addon (e.g. Torrentio) serves the
`stream` resource and adds `RemoteLocation` Parts. Configure a metadata addon
to see a library at all, and add a stream addon as well if you want stream
references. With only a metadata addon, an import is complete but has no
streams — that is the meta-only case working as intended, not a failure.

## Build

Requires a sibling checkout of `mosaic-sdk` (a `replace` directive in `go.mod`
points at `../mosaic-sdk`) until the SDK is published.

```
go build ./...
go test ./...
```

## A note on Stremio

This is an **unofficial** module. It is not affiliated with, sponsored by, or
endorsed by Stremio or SmartCode OOD. "Stremio" is used only nominatively, to
describe the addon protocol this module is compatible with. The module contains
no Stremio source code; it is an independent implementation of the publicly
documented [addon protocol](https://stremio.github.io/stremio-addon-sdk/), which
is itself published under the MIT License.

## License

MIT (see [`LICENSE`](LICENSE)). This module depends only on the Apache-2.0
[Mosaic SDK](https://github.com/mosaic-media/mosaic-sdk); it may be compiled into
a Mosaic Platform binary under the Platform's Module Linking Exception.
