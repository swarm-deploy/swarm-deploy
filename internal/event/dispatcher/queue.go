package dispatcher

import "github.com/swarm-deploy/swarm-deploy/internal/event/events"

const queueSize = 200

type queueTask struct {
	Event      events.Event
	Subscriber Subscriber
}

type queue struct {
	queue chan *queueTask
}

func newQueue() *queue {
	q := &queue{
		queue: make(chan *queueTask, queueSize),
	}

	go func() {
		q.runWorker()
	}()

	return q
}

func (q *queue) Dispatch(task *queueTask) {
	q.queue <- task
}

func (q *queue) Close() {
	close(q.queue)
}

func (q *queue) runWorker() {
	for task := range q.queue {
		handleSubscriber(task.Subscriber, task.Event)
	}
}
