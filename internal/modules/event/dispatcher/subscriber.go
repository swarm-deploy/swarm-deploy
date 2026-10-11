package dispatcher

import "github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"

// Subscriber is a consumer of the single durable Outbox.
type Subscriber = outbox.Subscriber
