package outbox

import (
	"context"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/prometheus/client_golang/prometheus"
)

var deliveriesDesc = prometheus.NewDesc(
	"swarm_deploy_outbox_deliveries", "Persisted outbox deliveries by status and subscription.",
	[]string{"status", "subscription_id"}, nil,
)

// Describe implements prometheus.Collector.
func (b *Bus) Describe(ch chan<- *prometheus.Desc) { ch <- deliveriesDesc }

type deliveryCount struct {
	Status         string  `db:"status"`
	SubscriptionID string  `db:"subscription_id"`
	Count          float64 `db:"count"`
}

// Collect reports persisted queue state, including terminal failures after restart.
func (b *Bus) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), databaseOperationTimeout)
	defer cancel()
	query, args, err := goqu.Dialect("sqlite3").From("outbox_deliveries").Prepared(true).
		Select("status", "subscription_id", goqu.COUNT("*").As("count")).
		GroupBy("status", "subscription_id").
		ToSQL()
	if err != nil {
		ch <- prometheus.NewInvalidMetric(deliveriesDesc, err)
		return
	}
	var counts []deliveryCount
	if err = b.db.Select(ctx, &counts, query, args...); err != nil {
		ch <- prometheus.NewInvalidMetric(deliveriesDesc, err)
		return
	}
	for _, c := range counts {
		ch <- prometheus.MustNewConstMetric(deliveriesDesc, prometheus.GaugeValue, c.Count, c.Status, c.SubscriptionID)
	}
}
