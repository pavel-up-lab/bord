package api

import "time"

// RetryError decorates a public domain error with HTTP Retry-After information.
type RetryError struct {
	Err   error
	After time.Duration
}

func (e *RetryError) Error() string             { return e.Err.Error() }
func (e *RetryError) Unwrap() error             { return e.Err }
func (e *RetryError) RetryAfter() time.Duration { return e.After }
