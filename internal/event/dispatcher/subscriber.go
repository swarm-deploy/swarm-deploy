package dispatcher

import "context"

type Subscriber interface {
	Handle(ctx context.Context, event Event) error
}
