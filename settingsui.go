package stremio

import (
	"context"

	"github.com/mosaic-media/contracts/ui"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// SettingsUI renders the module's own settings screen as SDUI (RoleSettingsUI,
// ADR 0038): add an addon by manifest URL, view the installed addons as cards
// (name, logo, description) with a way to configure or remove them, and browse a
// grid of installable addons (the addon_catalog resource) to add without a URL.
// Every mutating control is an Invoke of the Platform's configureModule command
// with the complete new settings document, so the Platform stays the one that
// persists them. The screen is returned as serialised UINode JSON — the SDK
// stays SDUI-agnostic.
//
// It builds its clients directly rather than through clientFrom, which refuses
// an empty addon list. That refusal is right for a provider role and wrong here:
// with no bundled default, no addons configured is what a fresh install looks
// like, and this screen is the only way out of it.
func (c *Capability) SettingsUI(ctx context.Context, req v1.SettingsUIRequest) (v1.SettingsUIResponse, error) {
	addons, err := addonsFrom(req.Settings)
	if err != nil {
		return v1.SettingsUIResponse{}, err
	}

	// One client over the configured addons, so the manifest fetches the cards
	// need are made and cached once.
	client := NewClient(c.httpClient, addons...)
	// A second over the configured addons *plus* Stremio's official directory,
	// which is what makes the browse grid non-empty on a fresh install. It is a
	// discovery source only: nothing it returns is sourced from unless a user
	// installs it.
	catalogClient := NewClient(c.httpClient, append(append([]string{}, addons...), addonCatalogSource)...)

	body := []ui.El{
		addAddonSection(addons),
		installedSection(ctx, client, addons),
		browseSection(ctx, catalogClient, addons),
	}
	screen := ui.Screen(ui.Title("Stremio addons"), ui.Group(body...))

	data, err := screen.BuildJSON()
	if err != nil {
		return v1.SettingsUIResponse{}, err
	}
	return v1.SettingsUIResponse{UI: data}, nil
}

// configureInput builds the configureModule invoke input for a given addon list
// — the complete settings document the Platform persists.
func configureInput(addons []string) map[string]any {
	return map[string]any{"moduleId": CapabilityID, "settings": map[string]any{"addons": addons}}
}

// addAddonSection is the add-by-URL form: a SubmitField whose action carries the
// existing addons plus the "$value" placeholder the runtime fills with the typed
// manifest URL (ADR 0038).
func addAddonSection(addons []string) *ui.Element {
	withNew := append(append([]string{}, addons...), "$value")
	field := ui.Component("SubmitField",
		ui.Prop("placeholder", "Paste an addon manifest URL…"),
		ui.Prop("submitLabel", "Add"),
		ui.OnTap(ui.Invoke("configureModule", configureInput(withNew))),
	)
	return ui.Section("Add an addon", field)
}

// installedSection renders the configured addons as a grid of cards, each with a
// Remove control and, where the addon exposes one, a Configure control that
// opens its own configuration page.
//
// With nothing configured it is an empty state rather than an empty grid. That
// is now a state a real install starts in — there is no bundled addon any more —
// so it has to say what to do next rather than look broken.
func installedSection(ctx context.Context, client *Client, userAddons []string) *ui.Element {
	if len(userAddons) == 0 {
		return ui.Section("Installed addons",
			ui.EmptyState("collections", "No Stremio addons yet — paste a manifest URL above, or install one from the list below. Mosaic already has metadata without them; addons are what add streams and other sources."))
	}

	userByBase := make(map[string]string, len(userAddons))
	for _, u := range userAddons {
		userByBase[normaliseAddonURL(u)] = u
	}

	cards := make([]ui.El, 0, len(userAddons))
	for _, info := range client.InstalledAddons(ctx) {
		var controls []ui.El
		if info.Configurable {
			controls = append(controls, ui.Button("Configure", "secondary", ui.OnTap(ui.OpenURL(info.Base+"/configure"))))
		}
		orig := userByBase[info.Base]
		controls = append(controls, ui.Button("Remove", "danger", ui.OnTap(ui.Invoke("configureModule", configureInput(without(userAddons, orig))))))
		cards = append(cards, addonCard(info.Name, info.Logo, info.Description, controls...))
	}

	return ui.Section("Installed addons", ui.Grid(cards...))
}

// browseSection renders installable addons from the addon_catalog resource as a
// grid of cards, each with its name/logo from the catalog's inline manifest.
// Best-effort: with no addon-catalog source it shows an empty state.
func browseSection(ctx context.Context, client *Client, userAddons []string) *ui.Element {
	entries, err := client.AddonCatalog(ctx)
	if err != nil || len(entries) == 0 {
		return ui.Section("Browse addons",
			ui.EmptyState("collections", "No addon catalog available — configure an addon that provides one to browse installable addons here"))
	}

	installed := make(map[string]bool)
	for _, u := range userAddons {
		installed[normaliseAddonURL(u)] = true
	}

	cards := make([]ui.El, 0, len(entries))
	for _, e := range entries {
		// Only offer addons Mosaic can actually use — those that fill one of the
		// provider roles it sources (metadata, catalog/search, stream, subtitles).
		// This hides addon-catalog-only, UI-overlay and behaviour-only addons that
		// would install but contribute nothing (ADR 0038).
		if !usefulToMosaic(e.Manifest) {
			continue
		}
		name := e.Manifest.Name
		if name == "" {
			name = e.TransportURL
		}
		if installed[normaliseAddonURL(e.TransportURL)] {
			cards = append(cards, addonCard(name, e.Manifest.Logo, e.Manifest.Description, ui.Badge("Installed", ui.ToneSuccess)))
			continue
		}
		withNew := append(append([]string{}, userAddons...), e.TransportURL)
		cards = append(cards, addonCard(name, e.Manifest.Logo, e.Manifest.Description,
			ui.Button("Install", "primary", ui.OnTap(ui.Invoke("configureModule", configureInput(withNew))))))
	}
	disclaimer := ui.Banner("Mosaic doesn't support every Stremio addon. This list is filtered to likely-compatible ones, but addons are community-made — add them at your own risk.", ui.ToneWarning)
	if len(cards) == 0 {
		return ui.Section("Browse addons",
			disclaimer, ui.EmptyState("collections", "No compatible addons to browse right now"))
	}
	return ui.Section("Browse addons", disclaimer, ui.Grid(cards...))
}

// addonCard is one addon tile: a logo + name header, a clamped description, and
// a trailing control row, laid out for a responsive grid.
func addonCard(name, logo, description string, controls ...ui.El) *ui.Element {
	header := make([]ui.El, 0, 2)
	if logo != "" {
		header = append(header, ui.Component("Box",
			ui.Prop("style", map[string]any{"width": 40, "height": 40, "radius": "md", "overflow": "hidden", "bg": "surface-overlay", "shrink": false}),
			ui.Component("Image", ui.Prop("src", logo), ui.Prop("fit", "contain"),
				ui.Prop("placeholder", " "), ui.Prop("style", map[string]any{"width": "full", "height": "full"}))))
	}
	header = append(header, ui.Component("Text", ui.Prop("text", name),
		ui.Prop("style", map[string]any{"weight": "medium", "lineClamp": 1})))

	children := []ui.El{
		ui.Component("Box", ui.Prop("style", map[string]any{"direction": "row", "align": "center", "gap": 3}), ui.Group(header...)),
	}
	if description != "" {
		children = append(children, ui.Component("Text", ui.Prop("text", description),
			ui.Prop("style", map[string]any{"variant": "sm", "color": "text-muted", "lineClamp": 2})))
	}
	children = append(children, ui.Component("Box",
		ui.Prop("style", map[string]any{"direction": "row", "gap": 2, "wrap": true, "mt": "auto", "pt": 2}),
		ui.Group(controls...)))

	return ui.Component("Box",
		ui.Prop("style", map[string]any{"direction": "column", "gap": 2, "p": 4, "radius": "lg", "bg": "surface-raised", "border": true, "minHeight": 132}),
		ui.Group(children...))
}

// deniedAddonIDs is a curated deny-list of community addons that are not content
// sources — mid-credits/jump-scare/clock overlays, debrid/VPN status panels,
// watch-party, rich-presence and companion addons. They inject non-content
// through the stream/meta/subtitles resources, so they are indistinguishable
// from real sources by resource type (ADR 0038) and must be named to hide. It is
// deliberately non-exhaustive — the browse disclaimer covers what it misses.
var deniedAddonIDs = map[string]bool{
	"com.almosteffective.aftercredits": true, // AfterCredits
	"org.community.cast-search":        true, // Cast Search
	"org.stremio.deepdivecompanion":    true, // Content Deep Dive Companion
	"com.discussio":                    true, // Discussio
	"imdb.ratings.local":               true, // IMDb Ratings (overlay)
	"community.peario":                 true, // Peario (watch party)
	"org.stinger.pro":                  true, // Stremio Stinger Pro
	"community.watch.next":             true, // Watch Next
	"org.stremio.doesTheDogDie":        true, // DoesTheDogDie
	"org.stremio.wheresthejump":        true, // Where's The Jump
	"community.aiostatus":              true, // AIOStatus
	"a1337user.statusio.tv.compatible": true, // Statusio
	"org.efnikolas.debridstatus":       true, // Debrid Status
	"org.stremio.discordpresence":      true, // Discord Rich Presence
	"org.vpn.iptest":                   true, // EfNikolas IP Test
	"com.kepners.flashclock":           true, // Clockrr
}

// usefulToMosaic reports whether an addon is worth offering in browse: it fills a
// provider role the Platform sources (metadata, catalog/search, stream,
// subtitles — ADR 0027/0037) and is not on the curated deny-list of non-content
// overlays/status addons. Compatibility is best-effort: Stremio has no field
// separating a content source from an enhancement addon, so this is a heuristic
// plus a named deny-list, with a disclaimer covering the rest (ADR 0038).
func usefulToMosaic(m Manifest) bool {
	if deniedAddonIDs[m.ID] {
		return false
	}
	for _, r := range m.Resources {
		switch r.Name {
		case "catalog", "meta", "stream", "subtitles":
			return true
		}
	}
	return false
}

// without returns addons with the first occurrence of target removed. An empty
// target (a default-only entry with no user duplicate) leaves the list unchanged.
func without(addons []string, target string) []string {
	out := make([]string, 0, len(addons))
	removed := false
	for _, a := range addons {
		if !removed && target != "" && a == target {
			removed = true
			continue
		}
		out = append(out, a)
	}
	return out
}
