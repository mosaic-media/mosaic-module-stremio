// Command module-stremio-addons runs this module as its own process, for a
// Platform that hosts it out of process (ADR 0064, ADR 0077).
//
// The whole of it is one line, and that is the point rather than a
// simplification. ADR 0064 is arranged around the property that crossing the
// process boundary must not change what a module author writes: the Capability
// below is the same plain Go value the Platform used to link into its own
// binary, its provider roles are the same methods, and its tests still run with
// no transport at all. What changes is that something now serves it.
//
// **This module builds and behaves identically whether or not this file is
// used.** Nothing else here imports it, and `stremio.New` remains what a
// statically-composed Platform calls. That is deliberate for the move: the
// binary exists before anything depends on it, so the change that switches the
// Platform over is a composition change rather than a module change.
//
// Two things a module author inherits by using host.Serve, both easy to trip
// over and neither obvious from this file:
//
//   - **Nothing may be written to stdout.** go-plugin writes its handshake
//     there, and anything else corrupts it. Use the Telemetry reached from the
//     invocation's context (ADR 0059) — it goes to the Platform's observability
//     plane rather than a stream nobody reads.
//   - **The Caller is a handle, not a session.** It is minted per invocation and
//     stops resolving when that invocation returns, so it cannot usefully be
//     stored. Module code never has to know: it forwards what it was given,
//     exactly as ADR 0017 already required.
package main

import (
	"github.com/mosaic-media/sdk/host"

	stremio "github.com/mosaic-media/module-stremio-addons"
)

func main() {
	// nil takes the module's default HTTP client. In process the Platform hands
	// one in so outbound calls route through its dial guard and carry trace
	// context (ADR 0055, seam 9); out of process it cannot, because an
	// *http.Client does not cross a process boundary.
	//
	// That is not a regression waiting to happen, it is the seam ADR 0064 moves:
	// egress for an out-of-process module is contained by a forward proxy the
	// Platform operates, which sees every host whether the module cooperates or
	// not. **That proxy is not built yet**, so until it is, this process's
	// outbound calls are unguarded in a way the in-process path is not — which
	// is exactly why the Platform still composes this module statically today.
	host.Serve(stremio.New(nil))
}
