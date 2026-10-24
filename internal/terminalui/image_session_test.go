package terminalui

import (
	"bytes"
	"strings"
	"testing"
)

func TestImageSessionGenerationReplacementAndIdempotentClose(t *testing.T) {
	var output bytes.Buffer
	session := NewImageSession(&output)
	first := session.Activate(NativeKitty, "attachment-a", 20, 8)
	if !first.Changed || first.ID == 0 || first.Generation != 1 || first.Cleanup != "" {
		t.Fatalf("first=%+v", first)
	}
	same := session.Activate(NativeKitty, "attachment-a", 20, 8)
	if same.Changed || same.ID != first.ID || same.Generation != first.Generation {
		t.Fatalf("same=%+v", same)
	}
	resized := session.Activate(NativeKitty, "attachment-a", 21, 8)
	if !resized.Changed || resized.ID == first.ID || resized.Generation != 2 || !strings.Contains(resized.Cleanup, "i="+itoa(first.ID)) {
		t.Fatalf("resized=%+v", resized)
	}
	replaced := session.Activate(NativeSixel, "attachment-b", 21, 8)
	if !replaced.Changed || !strings.Contains(replaced.Cleanup, "i="+itoa(resized.ID)) {
		t.Fatalf("replaced=%+v", replaced)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil || output.Len() != 0 {
		t.Fatalf("second close err=%v output=%q", err, output.String())
	}
}

func TestImageSessionClearAndCloseDeleteKittyOnce(t *testing.T) {
	var output bytes.Buffer
	session := NewImageSession(&output)
	handle := session.Activate(NativeKitty, "attachment", 10, 5)
	cleanup := session.Clear()
	if !strings.Contains(cleanup, "i="+itoa(handle.ID)) || session.Clear() != "" {
		t.Fatalf("cleanup=%q", cleanup)
	}
	session.Activate(NativeKitty, "attachment", 10, 5)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "a=d") != 1 {
		t.Fatalf("output=%q", output.String())
	}
}

func itoa(value uint32) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buffer [10]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = digits[value%10]
		value /= 10
	}
	return string(buffer[index:])
}
