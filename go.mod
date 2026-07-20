module github.com/mosaic-media/mosaic-module-stremio

go 1.25.0

require github.com/mosaic-media/mosaic-sdk v0.3.0

// Local development against the unreleased SDK surface. Dropped once the SDK is
// tagged v0.3.0 and this requires it through the module proxy. The path is a
// sibling working tree of this repository.
replace github.com/mosaic-media/mosaic-sdk => ../mosaic-sdk
