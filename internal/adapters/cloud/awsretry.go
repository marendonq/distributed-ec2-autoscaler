package cloud

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/smithy-go"
)

const defaultExpiredTokenRetryDelay = 30 * time.Second

// expiredTokenRetryDelay is the wait between retries (overridable in tests).
var expiredTokenRetryDelay = defaultExpiredTokenRetryDelay

func isExpiredToken(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "ExpiredToken" || code == "ExpiredTokenException"
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "expiredtoken")
}

// WithExpiredTokenRetry runs fn and retries after a delay when AWS credentials expired.
func WithExpiredTokenRetry(ctx context.Context, fn func(context.Context) error) error {
	for {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if !isExpiredToken(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(expiredTokenRetryDelay):
		}
	}
}
