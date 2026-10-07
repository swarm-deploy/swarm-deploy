package metrics

import "github.com/prometheus/client_golang/prometheus"

type BuildInfo interface {
	subsystem

	Set(version, buildDate string)
}

type prometheusBuildInfo struct {
	info *prometheus.GaugeVec
}

func newPrometheusBuildInfo(namespace string) *prometheusBuildInfo {
	return &prometheusBuildInfo{
		info: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "build_info",
			Help:      "Build info of swarm deploy",
		}, []string{"version", "build_date"}),
	}
}

func (i *prometheusBuildInfo) Set(version, buildDate string) {
	i.info.WithLabelValues(version, buildDate).Set(1)
}

func (i *prometheusBuildInfo) collectors() []prometheus.Collector {
	return []prometheus.Collector{i.info}
}
