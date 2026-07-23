# Claude Instructions — module-stremio-addons

This repository is the **first optional Mosaic module**: a Go client of the
Stremio addon protocol, built exactly as a third party's module would be — its
own Go module importing only the published contracts, compiled into a Platform
binary and invoked through the capability registry
([ADR 0019](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0019-module-capability-and-invocation.md),
[ADR 0020](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0020-optional-module-composition.md)).
"Official" describes only its authorship, not its shape; the discipline is the
point.

## The boundary is the point

- **Import only [`sdk`](https://github.com/mosaic-media/sdk),
  [`sdui`](https://github.com/mosaic-media/sdui) and the standard library.**
  `boundary_test.go` parses every import and fails on anything else. `sdui` is
  allowed because this module authors its own settings screen
  ([ADR 0038](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0038-module-contributed-settings-ui.md))
  — it declares a *form*, not a screen, which is what keeps ADR 0008's "modules
  contribute data, not screens" intact.
- **This module is an anti-corruption layer, and that is its job**
  ([ADR 0051](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0051-modules-as-anti-corruption-layers.md)).
  Upstream dialects are translated *here*, at the boundary, into the SDK's typed
  fields — a dialect table keyed on addon manifest id, an explicit list of
  sources actually tested, and a generic fallback for the rest. The Platform must
  never learn a provider's quirks. A community member adding a dialect should be
  a version bump touching nothing else.
- **Fill the typed fields rather than leaving them empty.** `Part.Container`,
  `VideoCodec` and `AudioCodec` are what selection ranks on. An empty field is
  not neutral: it was left empty once and the Platform would have relayed ten
  gigabytes of Matroska to a browser.
- **Resource-aware, so streams do not gate metadata.** Use whatever resources
  each configured addon declares. A meta-only addon must yield metadata with no
  Parts, so a user can enrich local media without adopting remote streaming.
- **Settings are user-managed opaque JSON** handed in by the Platform
  ([ADR 0021](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0021-module-settings.md)),
  not env vars and not Platform config. The module owns their meaning.
- **MIT-licensed**, the author's choice, unlike the Platform's AGPL
  ([ADR 0022](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0022-licensing.md)).

## Modules are the forcing function for the SDK

This module exists to find the SDK's gaps by using it. When something cannot be
expressed, that is a finding, not an obstacle to work around — user-managed
settings (ADR 0021) and module-declared cron/jobs were both found this way. Take
it to the SDK as an additive `v0.x` bump, or record it in the roadmap as an open
gap. **Do not simulate the missing surface locally.**

## Versioning and release

The Platform requires this at a **tagged version with no `replace`** — a
`replace` must never land in a commit. A change is a minor bump, tagged and
pushed, then the Platform's `go.mod` require is bumped to match.

```bash
git tag v0.18.0 && git push origin main && git push origin v0.18.0
```

The module reports the version that was **actually linked**, via
`v1.ModuleVersion` reading the build graph — not a hand-maintained constant,
which nothing forces to agree with anything.

## Workflow

- Commit and push this repository **separately** from `platform`.
- **Commit author identity** must be `AdamNi-7080 <anicholls41@gmail.com>`.
- `gofmt`, `go build ./...`, `go vet ./...` and `go test ./...` before pushing.
- Observability goes through the SDK's ambient `v1.Telemetry`
  ([ADR 0059](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0059-modules-observe-through-the-sdk.md)),
  reached as `TelemetryFrom(ctx)`. Do not print, and do not configure an
  exporter, a sink or retention — the Platform owns the observability plane.
- Real addons are the test fixture that matters. Two blocking bugs here —
  manifest-URL normalisation and a missing User-Agent, without which
  Cloudflare-fronted addons returned 403 — were invisible against fakes.

## The roadmap and the decision records

These rules are identical in every Mosaic repository. They exist because the
state of the build and the reasons behind it are the two things that rot fastest
and report nothing when they do — no build fails, no test goes red.

### The roadmap is maintained, not consulted

**`docs/roadmap.md` in [`architecture`](https://github.com/mosaic-media/architecture)
is the single record of where the build is.** Read it before starting work, and
**update it in the same session as the change that dates it** — not in a
follow-up, which does not happen.

- **A slice that lands is marked landed, with what was left out.** "Built" with
  no qualifier is a claim that the whole slice shipped; if part of it did not,
  say which part and why in the same sentence.
- **Implementation that departs from the plan is recorded where it departed.**
  The roadmap is derived from the code, not from the intention that preceded it,
  and the surprises are the most valuable thing in it.
- **Do not restate the roadmap here.** A second copy of "what is built" in a
  `CLAUDE.md` is how the first copy goes stale unnoticed. This file carries how
  to work in *this* repository; the roadmap carries what has been done across all
  of them.
- **A capability with no client path is not done — it is
  [owed](https://github.com/mosaic-media/architecture/blob/main/docs/unreachable-capability.md).**
  If you delete or fail to build a client path to a working service, add its row
  to that register in the same change.

### Decision records are append-only

An ADR is an account of what was decided and why, at a time. It is evidence, not
documentation, and its value is that it was not edited afterwards.

- **Never rewrite a record's body to match what was built.** Not to correct it,
  not to annotate it, not to add "as built, this differs". That pattern turns a
  record into a running commentary and destroys the thing it is for.
- **State changes in the `**Status:**` line, and nowhere else.** That is where a
  record says it is built, built in part (naming the part), or superseded —
  wholly ("Superseded by ADR N") or partly ("Partly superseded: X was reversed by
  ADR N; the rest stands").
- **A changed decision needs a new record that supersedes it.** If the code
  deliberately does something a record decided against, that is a decision and it
  is written down as one, with its own Context / Decision / Alternatives /
  Consequences. Both records then stand: the old one keeps its reasoning, the new
  one carries the change.
- **An unbuilt decision is not a superseded one.** "We have not done this yet"
  belongs in the Status line and the roadmap. Only a genuine reversal earns a new
  record.
- **Records live only in `architecture/docs/adr/`**, numbered sequentially in
  kebab-case. Adding one means adding it to `nav:` in `mkdocs.yml`, and
  `mkdocs build --strict` must pass.

**If the code and a record disagree, say so rather than quietly picking one.** An
honest "this is unresolved" is worth more than a plausible reconciliation that
reads as settled.
