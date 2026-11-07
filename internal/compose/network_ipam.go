package compose

// NetworkIPAM configures the network address allocator.
type NetworkIPAM struct {
	// Driver selects the allocator plugin.
	Driver string `yaml:"driver,omitempty" json:"driver,omitempty"`
	// Options configures the allocator driver.
	Options map[string]string `yaml:"options,omitempty" json:"options,omitempty"`
	// Config contains address pools.
	Config []NetworkIPAMPool `yaml:"config,omitempty" json:"config,omitempty"`
}

// NetworkIPAMPool describes a network address pool.
type NetworkIPAMPool struct {
	// Subnet is the pool's CIDR subnet.
	Subnet string `yaml:"subnet,omitempty" json:"subnet,omitempty"`
	// IPRange restricts dynamic address allocation.
	IPRange string `yaml:"ip_range,omitempty" json:"ip_range,omitempty"`
	// Gateway is the subnet gateway.
	Gateway string `yaml:"gateway,omitempty" json:"gateway,omitempty"`
	// AuxAddresses reserves named addresses in the pool.
	AuxAddresses map[string]string `yaml:"aux_addresses,omitempty" json:"aux_addresses,omitempty"`
}
