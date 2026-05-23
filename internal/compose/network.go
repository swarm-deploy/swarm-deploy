package compose

type Network struct {
	Name     string `yaml:"name" json:"name"`
	External bool   `yaml:"external" json:"external"`
	Internal bool   `yaml:"internal" json:"internal"`
}
