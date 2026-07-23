# module-stremio-addons

Stremio addon-source module for the [Mosaic](https://github.com/mosaic-media) platform — a Go client of the Stremio addon protocol, built against the Mosaic SDK.

It is an **optional Mosaic module**: its own Go module that imports only
[`sdk`](https://github.com/mosaic-media/sdk) and the standard
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

**This module bundles no addon of its own.** It used to include Cinemeta so that
a fresh install had metadata; that guarantee now belongs to
[`module-cinemeta`](https://github.com/mosaic-media/module-cinemeta), a core
module that cannot be switched off or misconfigured
([ADR 0072](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0072-the-guaranteed-metadata-provider-needs-no-credential.md)).
With nothing configured here, this module simply contributes nothing — Mosaic
still has metadata and search. A settings document written before the change may
still carry `disableDefaultAddons`; the key is ignored, so nothing needs
migrating.

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
`stream` resource and adds `RemoteLocation` Parts. With only a metadata addon,
an import is complete but has no streams — that is the meta-only case working as
intended, not a failure.

**A stream addon on its own does not yet make a searchable library playable.**
Torrentio declares no catalog, so it produces no search results; the results come
from the core metadata module, and an import routes to whichever provider
produced the ref — so it materialises metadata with no Parts. Configuring a
metadata addon *here* as well is what makes this module produce refs its own
stream addons can attach to. Composing one provider's metadata with another's
streams is an open Platform question, not something this module can answer.

## Build and test

**Everything runs in a container; nothing is built or tested on the host.** The
gate — gofmt, `go build`, `go vet`, `go test` — is one command:

```bash
docker compose -f docker-compose.test.yml run --rm test
```

Append `bash` for a shell in the same environment. The SDK is a **published**
dependency resolved from the module proxy (`go.mod` requires it at a tagged
version, no `replace`), so no sibling checkout is needed to build. The container
resolves it exactly as a third party would — which is the point: this module
compiles against the published SDK and the standard library and nothing else,
and `boundary_test.go` enforces that by parsing every import. It also reaches
real addons over TLS, so the container has network and certificates; if those
tests start failing, check that before suspecting the addons.

## A note on Stremio

This is an **unofficial** module. It is not affiliated with, sponsored by, or
endorsed by Stremio or SmartCode OOD. "Stremio" is used only nominatively, to
describe the addon protocol this module is compatible with. The module contains
no Stremio source code; it is an independent implementation of the publicly
documented [addon protocol](https://stremio.github.io/stremio-addon-sdk/), which
is itself published under the MIT License.

## License

MIT (see [`LICENSE`](LICENSE)). This module depends only on the Apache-2.0
[Mosaic SDK](https://github.com/mosaic-media/sdk); it may be compiled into
a Mosaic Platform binary under the Platform's Module Linking Exception.
