// Package domain defines dependency-free business contracts.
package domain

type (
	BoardID       string
	ThreadID      string
	PostID        string
	AttachmentID  string
	SessionID     string
	EventID       int64
	RequestID     string
	CorrelationID string
)

func (id BoardID) Valid() bool       { return id != "" }
func (id ThreadID) Valid() bool      { return id != "" }
func (id PostID) Valid() bool        { return id != "" }
func (id AttachmentID) Valid() bool  { return id != "" }
func (id SessionID) Valid() bool     { return id != "" }
func (id RequestID) Valid() bool     { return id != "" }
func (id CorrelationID) Valid() bool { return id != "" }
