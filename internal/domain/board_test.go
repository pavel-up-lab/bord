package domain

import (
	"testing"
	"time"
)

func TestPostRequiresContent(t *testing.T) {
	_, err := NewPost("1", "1", "", "author", "  ", "", time.Now())
	if ErrorCode(err) != CodeValidation {
		t.Fatalf("code = %q", ErrorCode(err))
	}
}

func TestClosedThreadRejectsReply(t *testing.T) {
	thread, _ := NewThread("1", "1", time.Now())
	thread.Status = ThreadClosed
	if ErrorCode(thread.RequireOpen()) != CodeThreadClosed {
		t.Fatal("closed thread accepted a reply")
	}
}

func TestCrossThreadReplyRejected(t *testing.T) {
	now := time.Now()
	reply, _ := NewPost("2", "thread-a", "1", "author", "reply", "", now)
	target, _ := NewPost("1", "thread-b", "", "other", "target", "", now)
	if ErrorCode(reply.ValidateReplyTarget(target)) != CodeValidation {
		t.Fatal("cross-thread target accepted")
	}
}

func TestTombstoneRemovesContent(t *testing.T) {
	now := time.Now()
	post, _ := NewPost("1", "1", "", "author", "secret", "attachment", now)
	tombstone, err := post.Tombstone(now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if tombstone.Body != "" || tombstone.AttachmentID.Valid() || tombstone.DeletedAt == nil || tombstone.Status != PostDeleted {
		t.Fatalf("invalid tombstone: %+v", tombstone)
	}
}
