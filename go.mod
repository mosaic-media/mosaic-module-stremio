module github.com/mosaic-media/mosaic-module-stremio

go 1.25.0

require github.com/mosaic-media/mosaic-sdk v0.2.0

// Local development against the unreleased SDK surface. Removed once the SDK
// is tagged v0.2.0 and this requires it through the module proxy.
replace github.com/mosaic-media/mosaic-sdk => ../../../mosaic-sdk
