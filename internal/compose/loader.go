package compose

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Loader struct {
}

func NewLoader() *Loader {
	return &Loader{}
}

func (l *Loader) Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read compose file %s: %w", path, err)
	}

	root := map[string]any{}
	err = yaml.Unmarshal(raw, &root)
	if err != nil {
		return nil, fmt.Errorf("decode compose yaml: %w", err)
	}

	schema := Compose{}
	err = yaml.Unmarshal(raw, &schema)
	if err != nil {
		return nil, fmt.Errorf("decode compose schema: %w", err)
	}

	err = l.linkServices(&schema)
	if err != nil {
		return nil, fmt.Errorf("link: %w", err)
	}

	return &File{
		RawBytes: raw,
		RawMap:   root,
		Compose:  schema,
	}, nil
}

func (*Loader) linkServices(compose *Compose) error {
	for name, service := range compose.Services {
		service.Networks = resolveNetworkAliases(service.Networks, compose.Networks)

		initJobs := normalizeInitJobs(service.InitJobs, compose.Networks)
		service.InitJobs = initJobs

		compose.Services[name] = service
	}

	return nil
}
