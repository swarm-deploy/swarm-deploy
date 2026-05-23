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

	schema := File{}
	err = yaml.Unmarshal(raw, &schema)
	if err != nil {
		return nil, fmt.Errorf("decode compose schema: %w", err)
	}

	err = l.linkServices(&schema)
	if err != nil {
		return nil, fmt.Errorf("link: %w", err)
	}

	schema.RawBytes = raw
	schema.RawMap = root

	return &schema, nil
}

func (*Loader) linkServices(file *File) error {
	for name, service := range file.Services {
		service.Networks = resolveNetworkAliases(service.Networks, file.Networks)

		initJobs := normalizeInitJobs(service.InitJobs, file.Networks)
		service.InitJobs = initJobs

		file.Services[name] = service
	}

	return nil
}
