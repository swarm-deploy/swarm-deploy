package pipe

import "context"

type Step[pt any] struct {
	Name string
	When func(payload pt) bool
	Run  func(ctx context.Context, payload pt) error
}

func always[pt any]() func(payload pt) bool {
	return func(pt) bool {
		return true
	}
}
