package stremio

import (
	"context"
	"encoding/json"

	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
	sdui "github.com/mosaic-media/mosaic-sdui/sdui"
)

// SettingsUI renders the module's own settings screen as SDUI (RoleSettingsUI,
// ADR 0038): add an addon by manifest URL, view the installed addons as cards
// (name, logo, description) with a way to configure or remove them, toggle the
// bundled Cinemeta default, and browse a grid of installable addons (the
// addon_catalog resource) to add without a URL. Every mutating control is an
// Invoke of the Platform's configureModule command with the complete new
// settings document, so the Platform stays the one that persists them. The
// screen is returned as serialised UINode JSON — the SDK stays SDUI-agnostic.
func (c *Capability) SettingsUI(ctx context.Context, req v1.SettingsUIRequest) (v1.SettingsUIResponse, error) {
	addons, disableDefaults, err := addonsFrom(req.Settings)
	if err != nil {
		return v1.SettingsUIResponse{}, err
	}

	// One client over the effective addon set, so the manifest fetches the cards
	// need are made and cached once.
	client, err := c.clientFrom(req.Settings)
	if err != nil {
		return v1.SettingsUIResponse{}, err
	}

	body := []sdui.Node{
		addAddonSection(addons, disableDefaults),
		installedSection(ctx, client, addons, disableDefaults),
		browseSection(ctx, client, addons, disableDefaults),
	}
	screen := sdui.Screen(sdui.Prop("title", "Stremio addons"), sdui.Child(body...))

	ui, err := json.Marshal(screen)
	if err != nil {
		return v1.SettingsUIResponse{}, err
	}
	return v1.SettingsUIResponse{UI: ui}, nil
}

// configureInput builds the configureModule invoke input for a given user-addon
// list and default flag — the complete settings document the Platform persists.
// The bundled default is never stored in the list; it is module-owned.
func configureInput(addons []string, disableDefaults bool) map[string]any {
	settings := map[string]any{"addons": addons}
	if disableDefaults {
		settings["disableDefaultAddons"] = true
	}
	return map[string]any{"moduleId": CapabilityID, "settings": settings}
}

// addAddonSection is the add-by-URL form: a SubmitField whose action carries the
// existing addons plus the "$value" placeholder the runtime fills with the typed
// manifest URL (ADR 0038).
func addAddonSection(addons []string, disableDefaults bool) sdui.Node {
	withNew := append(append([]string{}, addons...), "$value")
	field := sdui.Component("SubmitField",
		sdui.Prop("placeholder", "Paste an addon manifest URL…"),
		sdui.Prop("submitLabel", "Add"),
		sdui.Act(sdui.Invoke("configureModule", configureInput(withNew, disableDefaults))),
	)
	return sdui.Section("Add an addon", sdui.Child(field))
}

// installedSection renders the effective addon set as a grid of cards. The
// bundled Cinemeta default is shown once, deduped against a user addon for the
// same URL: as the default (a Disable that also strips any duplicate from the
// user list) when enabled, else as a plain user addon. A configurable addon
// carries a Configure control that opens its own configuration page.
func installedSection(ctx context.Context, client *Client, userAddons []string, disableDefaults bool) sdui.Node {
	defaultBase := normaliseAddonURL(defaultAddon)
	userByBase := make(map[string]string, len(userAddons))
	for _, u := range userAddons {
		userByBase[normaliseAddonURL(u)] = u
	}

	cards := make([]sdui.Node, 0)
	for _, info := range client.InstalledAddons(ctx) {
		var controls []sdui.Node
		if info.Base == defaultBase && !disableDefaults {
			// The bundled default. Disabling it also removes any duplicate the
			// user added explicitly, so Cinemeta is fully gone.
			controls = append(controls,
				sdui.Badge("Default", sdui.ToneNeutral),
				sdui.Button("Disable", "ghost", sdui.Invoke("configureModule", configureInput(withoutBase(userAddons, defaultBase), true))))
		} else {
			if info.Configurable {
				controls = append(controls, sdui.Button("Configure", "secondary", sdui.OpenURL(info.Base+"/configure")))
			}
			orig := userByBase[info.Base]
			controls = append(controls, sdui.Button("Remove", "danger", sdui.Invoke("configureModule", configureInput(without(userAddons, orig), disableDefaults))))
		}
		cards = append(cards, addonCard(info.Name, info.Logo, info.Description, controls...))
	}

	// When the default is disabled and Cinemeta is not otherwise present, offer
	// to re-enable it, so the toggle stays reachable.
	if disableDefaults && userByBase[defaultBase] == "" {
		cards = append(cards, addonCard("Cinemeta", "", "The bundled default metadata addon — currently disabled.",
			sdui.Badge("Disabled", sdui.ToneNeutral),
			sdui.Button("Enable", "secondary", sdui.Invoke("configureModule", configureInput(userAddons, false)))))
	}

	return sdui.Section("Installed addons", sdui.Child(sdui.Grid(sdui.Child(cards...))))
}

// browseSection renders installable addons from the addon_catalog resource as a
// grid of cards, each with its name/logo from the catalog's inline manifest.
// Best-effort: with no addon-catalog source it shows an empty state.
func browseSection(ctx context.Context, client *Client, userAddons []string, disableDefaults bool) sdui.Node {
	entries, err := client.AddonCatalog(ctx)
	if err != nil || len(entries) == 0 {
		return sdui.Section("Browse addons",
			sdui.Child(sdui.EmptyState("collections", "No addon catalog available — configure an addon that provides one to browse installable addons here")))
	}

	installed := make(map[string]bool)
	if !disableDefaults {
		installed[normaliseAddonURL(defaultAddon)] = true
	}
	for _, u := range userAddons {
		installed[normaliseAddonURL(u)] = true
	}

	cards := make([]sdui.Node, 0, len(entries))
	for _, e := range entries {
		name := e.Manifest.Name
		if name == "" {
			name = e.TransportURL
		}
		if installed[normaliseAddonURL(e.TransportURL)] {
			cards = append(cards, addonCard(name, e.Manifest.Logo, e.Manifest.Description, sdui.Badge("Installed", sdui.ToneSuccess)))
			continue
		}
		withNew := append(append([]string{}, userAddons...), e.TransportURL)
		cards = append(cards, addonCard(name, e.Manifest.Logo, e.Manifest.Description,
			sdui.Button("Install", "primary", sdui.Invoke("configureModule", configureInput(withNew, disableDefaults)))))
	}
	return sdui.Section("Browse addons", sdui.Child(sdui.Grid(sdui.Child(cards...))))
}

// addonCard is one addon tile: a logo + name header, a clamped description, and
// a trailing control row, laid out for a responsive grid.
func addonCard(name, logo, description string, controls ...sdui.Node) sdui.Node {
	header := make([]sdui.Node, 0, 2)
	if logo != "" {
		header = append(header, sdui.Component("Box",
			sdui.Prop("style", map[string]any{"width": 40, "height": 40, "radius": "md", "overflow": "hidden", "bg": "surface-overlay", "shrink": false}),
			sdui.Child(sdui.Component("Image", sdui.Prop("src", logo), sdui.Prop("fit", "contain"),
				sdui.Prop("placeholder", " "), sdui.Prop("style", map[string]any{"width": "full", "height": "full"})))))
	}
	header = append(header, sdui.Component("Text", sdui.Prop("text", name),
		sdui.Prop("style", map[string]any{"weight": "medium", "lineClamp": 1})))

	children := []sdui.Node{
		sdui.Component("Box", sdui.Prop("style", map[string]any{"direction": "row", "align": "center", "gap": 3}), sdui.Child(header...)),
	}
	if description != "" {
		children = append(children, sdui.Component("Text", sdui.Prop("text", description),
			sdui.Prop("style", map[string]any{"variant": "sm", "color": "text-muted", "lineClamp": 2})))
	}
	children = append(children, sdui.Component("Box",
		sdui.Prop("style", map[string]any{"direction": "row", "gap": 2, "wrap": true, "mt": "auto", "pt": 2}),
		sdui.Child(controls...)))

	return sdui.Component("Box",
		sdui.Prop("style", map[string]any{"direction": "column", "gap": 2, "p": 4, "radius": "lg", "bg": "surface-raised", "border": true, "minHeight": 132}),
		sdui.Child(children...))
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

// withoutBase returns addons with every entry normalising to base removed.
func withoutBase(addons []string, base string) []string {
	out := make([]string, 0, len(addons))
	for _, a := range addons {
		if normaliseAddonURL(a) != base {
			out = append(out, a)
		}
	}
	return out
}
