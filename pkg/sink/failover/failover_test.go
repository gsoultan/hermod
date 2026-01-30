package failover

import (
	"context"
	"errors"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type mockSink struct {
	writeCalled bool
	fail        bool
	closeCalled bool
}

func (m *mockSink) Write(ctx context.Context, msg hermod.Message) error {
	m.writeCalled = true
	if m.fail {
		return errors.New("write failed")
	}
	return nil
}

func (m *mockSink) Ping(ctx context.Context) error { return nil }
func (m *mockSink) Close() error {
	m.closeCalled = true
	return nil
}

func TestFailoverSink_Write(t *testing.T) {
	primary := &mockSink{fail: true}
	fallback := &mockSink{fail: false}

	s := NewFailoverSink(primary, []hermod.Sink{fallback})
	msg := message.AcquireMessage()

	err := s.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !primary.writeCalled {
		t.Error("primary write should have been called")
	}
	if !fallback.writeCalled {
		t.Error("fallback write should have been called")
	}
}

func TestFailoverSink_Write_PrimarySuccess(t *testing.T) {
	primary := &mockSink{fail: false}
	fallback := &mockSink{fail: false}

	s := NewFailoverSink(primary, []hermod.Sink{fallback})
	msg := message.AcquireMessage()

	err := s.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !primary.writeCalled {
		t.Error("primary write should have been called")
	}
	if fallback.writeCalled {
		t.Error("fallback write should NOT have been called")
	}
}

func TestFailoverSink_Write_AllFail(t *testing.T) {
	primary := &mockSink{fail: true}
	fallback := &mockSink{fail: true}

	s := NewFailoverSink(primary, []hermod.Sink{fallback})
	msg := message.AcquireMessage()

	err := s.Write(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFailoverSink_Close(t *testing.T) {
	primary := &mockSink{}
	fallback := &mockSink{}

	s := NewFailoverSink(primary, []hermod.Sink{fallback})
	s.Close()

	if !primary.closeCalled {
		t.Error("primary close should have been called")
	}
	if !fallback.closeCalled {
		t.Error("fallback close should have been called")
	}
}
