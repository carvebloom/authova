package domain

// RateLimitError carries a bounded retry hint without exposing counter keys.
type RateLimitError struct{ RetryAfterSeconds int }

func (*RateLimitError) Error() string { return "rate limit" }
func (*RateLimitError) Unwrap() error { return ErrRateLimit }
