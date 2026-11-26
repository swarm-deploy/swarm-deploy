// Package pwa prepares the localized web application manifest.
package pwa

import (
	"embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	sharedi18n "github.com/swarm-deploy/swarm-deploy/internal/shared/i18n"
)

// ManifestFile is the public path of the web application manifest.
const ManifestFile = "manifest.webmanifest"

//go:embed messages.en.json messages.ru.json
var catalogs embed.FS

// Manifest describes the web application manifest served by swarm-deploy.
type Manifest struct {
	// Name is the full application name.
	Name string `json:"name"`
	// ShortName is the application name used when space is limited.
	ShortName string `json:"short_name"`
	// Description describes the application purpose.
	Description string `json:"description"`
	// StartURL is the URL opened when the installed application starts.
	StartURL string `json:"start_url"`
	// Scope limits the URLs handled by the installed application.
	Scope string `json:"scope"`
	// Display defines the preferred application display mode.
	Display string `json:"display"`
	// ThemeColor defines the browser UI color associated with the application.
	ThemeColor string `json:"theme_color"`
	// BackgroundColor defines the launch background color.
	BackgroundColor string `json:"background_color"`
	// Icons contains installable application icons.
	Icons []Icon `json:"icons"`
	// Shortcuts contains quick actions exposed by the installed application.
	Shortcuts []Shortcut `json:"shortcuts,omitempty"`
}

// Shortcut describes a quick action in the installed application menu.
type Shortcut struct {
	// Name is the localized action label.
	Name string `json:"name"`
	// URL is the destination relative to the manifest URL.
	URL string `json:"url"`
	// Icons contains images representing the shortcut in the application menu.
	Icons []Icon `json:"icons,omitempty"`
}

// Icon describes an image referenced by a web application manifest.
type Icon struct {
	// Src is the icon URL.
	Src string `json:"src"`
	// Sizes lists the icon dimensions.
	Sizes string `json:"sizes"`
	// Type is the icon media type.
	Type string `json:"type"`
	// Purpose describes how the icon may be used.
	Purpose string `json:"purpose"`
}

// Prepare reads the base manifest, applies configuration and adds localized shortcuts.
func Prepare(uiFS fs.FS, locale string, pwaConfig config.WebPWASpec) ([]byte, error) {
	manifestBytes, err := fs.ReadFile(uiFS, ManifestFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read embedded PWA manifest: %w", err)
	}

	var manifest Manifest
	if err = json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("decode embedded PWA manifest: %w", err)
	}
	if pwaConfig.Name != "" {
		manifest.Name = pwaConfig.Name
	}
	if pwaConfig.ShortName != "" {
		manifest.ShortName = pwaConfig.ShortName
	}

	localizer, err := sharedi18n.New(locale, catalogs, "messages.en.json", "messages.ru.json")
	if err != nil {
		return nil, fmt.Errorf("load PWA texts: %w", err)
	}
	manifest.Shortcuts = []Shortcut{
		{
			Name: localizer.Text("shortcut.ask_assistant"),
			URL:  "./overview?assistant=new",
			Icons: []Icon{
				{Src: "icons/icon-192.png", Sizes: "192x192", Type: "image/png", Purpose: "any"},
			},
		},
		{
			Name: localizer.Text("shortcut.events_alerts"),
			URL:  "./events",
			Icons: []Icon{
				{Src: "icons/icon-192.png", Sizes: "192x192", Type: "image/png", Purpose: "any"},
			},
		},
	}

	manifestBytes, err = json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode PWA manifest: %w", err)
	}

	return manifestBytes, nil
}
