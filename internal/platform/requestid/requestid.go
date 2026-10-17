// Package requestid defines transport-safe request and correlation identifiers.
package requestid

import (
	"errors"
	"strings"
)

type RequestID string
type CorrelationID string

func ParseRequest(value string) (RequestID, error) {
	if !valid(value) {
		return "", errors.New("invalid request ID")
	}
	return RequestID(value), nil
}
func ParseCorrelation(value string) (CorrelationID, error) {
	if !valid(value) {
		return "", errors.New("invalid correlation ID")
	}
	return CorrelationID(value), nil
}
func valid(value string) bool {
	return len(value) >= 8 && len(value) <= 128 && !strings.ContainsAny(value, "\r\n\t ")
}
