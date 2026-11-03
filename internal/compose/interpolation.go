package compose

import "go.yaml.in/yaml/v3"

// interpolateSyntax preserves repository-controlled Compose values literally.
// No process environment values may be used during source loading.
func interpolateSyntax(_ *yaml.Node) (bool, error) {
	return false, nil
}

// interpolationSafeError retains diagnostics when interpolation is disabled.
func interpolationSafeError(err error, _ bool, _ string) error {
	return err
}
