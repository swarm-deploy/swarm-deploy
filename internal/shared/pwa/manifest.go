package pwa

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
