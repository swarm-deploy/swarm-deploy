package config

import "github.com/artarts36/specw"

type WebSpec struct {
	// Address is an HTTP listen address for UI and API server.
	Address string `yaml:"address"`
	// Security contains UI and API access settings.
	Security SecuritySpec `yaml:"security"`
}

type SecuritySpec struct {
	// Authentication contains web authentication strategy settings.
	Authentication AuthenticationSpec `yaml:"authentication"`
}

type AuthenticationSpec struct {
	// Basic contains HTTP Basic authentication settings.
	Basic BasicAuthenticationSpec `yaml:"basic"`
	// AuthProxy contains authentication settings for a trusted reverse proxy.
	AuthProxy AuthProxyAuthenticationSpec `yaml:"authProxy"`
}

type BasicAuthenticationSpec struct {
	// HTPasswdFile is a path to htpasswd file with user credentials.
	HTPasswdFile specw.File `yaml:"htpasswdFile"`
}

type AuthProxyAuthenticationSpec struct {
	// LoginHeader is the request header containing the authenticated user login.
	LoginHeader string `yaml:"loginHeader"`
}
