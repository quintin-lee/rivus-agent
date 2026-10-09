package model

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type RetryError struct {
	Status    int
	Body      string
	Retryable bool
}

func (e *RetryError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("model http %d: %s", e.Status, e.Body)
	}
	return "model request failed: " + e.Body
}

func Classify(err error) error {
	var nerr net.Error
	if errors.As(err, &nerr) && (nerr.Timeout() || true) {
		return &RetryError{Body: err.Error(), Retryable: true}
	}
	return &RetryError{Body: err.Error(), Retryable: false}
}

func IsRetryable(err error) bool {
	var re *RetryError
	if errors.As(err, &re) {
		return re.Retryable
	}
	return false
}

func GenerateWithRetry(ctx context.Context, m model.BaseChatModel, input []*schema.Message, maxRetries int) (*schema.Message, error) {
	var err error
	var out *schema.Message
	backoff := 500 * time.Millisecond
	for attempt := 0; attempt <= maxRetries; attempt++ {
		out, err = m.Generate(ctx, input)
		if err == nil {
			return out, nil
		}
		if !IsRetryable(err) || attempt == maxRetries {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
			if backoff > 8*time.Second {
				backoff = 8 * time.Second
			}
		}
	}
	return nil, err
}
