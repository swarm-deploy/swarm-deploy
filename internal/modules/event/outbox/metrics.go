package outbox

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

var deliveriesDesc = prometheus.NewDesc(
	"swarm_deploy_outbox_deliveries", "Persisted outbox deliveries by status and subscription.",
	[]string{"status", "subscription_id"}, nil,
)

// Describe implements prometheus.Collector.
func (b *Bus) Describe(ch chan<- *prometheus.Desc) { ch <- deliveriesDesc }

// Collect reports persisted queue state, including terminal failures after restart.
func (b *Bus) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), databaseOperationTimeout)
	defer cancel()
	rows, err := b.db.Get(ctx).QueryContext(ctx, `SELECT status,subscription_id,count(*)
		FROM outbox_deliveries GROUP BY status,subscription_id`)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(deliveriesDesc, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var status, id string
		var count float64
		if err = rows.Scan(&status, &id, &count); err != nil {
			ch <- prometheus.NewInvalidMetric(deliveriesDesc, err)
			return
		}
		ch <- prometheus.MustNewConstMetric(deliveriesDesc, prometheus.GaugeValue, count, status, id)
	}
	if err = rows.Err(); err != nil {
		ch <- prometheus.NewInvalidMetric(deliveriesDesc, err)
	}
}
