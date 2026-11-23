package tools

import (
	"fmt"
)

func convertRequestPayload[T any](payload any) (T, error) {
	var decoded T
	if payload == nil {
		return decoded, nil
	}

	typed, ok := payload.(T)
	if ok {
		return typed, nil
	}

	return decoded, fmt.Errorf("request payload has unexpected type %T", payload)
}
