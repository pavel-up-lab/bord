package terminalui

import (
	"crypto/rand"
	"encoding/binary"
	"io"
	"sync"
)

type NativeProtocol uint8

const (
	NativeNone NativeProtocol = iota
	NativeKitty
	NativeSixel
)

type ImageHandle struct {
	ID         uint32
	Generation uint64
	Cleanup    string
	Changed    bool
}

// ImageSession owns terminal-native resources for one Bubble Tea program.
// It is safe to share between the public and administrative models.
type ImageSession struct {
	mu         sync.Mutex
	output     io.Writer
	nextID     uint32
	generation uint64
	active     imageResource
	closed     bool
}

type imageResource struct {
	protocol      NativeProtocol
	key           string
	columns, rows int
	id            uint32
}

func NewImageSession(output io.Writer) *ImageSession {
	var seed [4]byte
	_, _ = rand.Read(seed[:])
	// Kitty placeholder IDs are encoded in a true-color foreground, giving us
	// 24 usable bits. Reserve zero as "no image".
	next := binary.BigEndian.Uint32(seed[:]) & 0x00ffffff
	if next == 0 {
		next = 1
	}
	return &ImageSession{output: output, nextID: next}
}

func (s *ImageSession) Activate(protocol NativeProtocol, key string, columns, rows int) ImageHandle {
	if s == nil || protocol == NativeNone || key == "" || columns <= 0 || rows <= 0 {
		return ImageHandle{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ImageHandle{}
	}
	if s.active.protocol == protocol && s.active.key == key && s.active.columns == columns && s.active.rows == rows {
		return ImageHandle{ID: s.active.id, Generation: s.generation}
	}
	cleanup := resourceCleanup(s.active)
	s.generation++
	s.nextID = (s.nextID + 1) & 0x00ffffff
	if s.nextID == 0 {
		s.nextID = 1
	}
	s.active = imageResource{protocol: protocol, key: key, columns: columns, rows: rows, id: s.nextID}
	return ImageHandle{ID: s.active.id, Generation: s.generation, Cleanup: cleanup, Changed: true}
}

func (s *ImageSession) Clear() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cleanup := resourceCleanup(s.active)
	s.active = imageResource{}
	return cleanup
}

func (s *ImageSession) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	cleanup := resourceCleanup(s.active)
	s.active = imageResource{}
	s.closed = true
	output := s.output
	s.mu.Unlock()
	if cleanup == "" || output == nil {
		return nil
	}
	_, err := io.WriteString(output, cleanup)
	return err
}

func resourceCleanup(resource imageResource) string {
	if resource.protocol != NativeKitty || resource.id == 0 {
		return ""
	}
	return KittyDeleteImage(resource.id)
}
