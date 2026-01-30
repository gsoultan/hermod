package engine

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

func TestSinkWriter_CircuitBreaker(t *testing.T) {
	sink := &mockSink{received: make(chan hermod.Message, 10)}
	eng := &Engine{logger: NewDefaultLogger()}
	sw := &sinkWriter{
		engine: eng,
		sink:   sink,
		sinkID: "sink1",
		config: SinkConfig{
			CircuitBreakerThreshold: 2,
			CircuitBreakerInterval:  1 * time.Minute,
			CircuitBreakerCoolDown:  100 * time.Millisecond,
			BatchSize:               1,
			BatchTimeout:            10 * time.Millisecond,
		},
		ch: make(chan *pendingMessage, 10),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sw.run(ctx)

	// Trigger failure 1
	pm1 := acquirePendingMessage(message.AcquireMessage())
	sink.fail = 1
	sw.ch <- pm1
	<-pm1.done
	if sw.cbStatus != "closed" {
		t.Errorf("expected closed, got %s", sw.cbStatus)
	}

	// Trigger failure 2 -> Open circuit
	pm2 := acquirePendingMessage(message.AcquireMessage())
	sink.fail = 1
	sw.ch <- pm2
	<-pm2.done
	if sw.cbStatus != "open" {
		t.Errorf("expected open, got %s", sw.cbStatus)
	}

	// Send message while open -> should fail immediately without sink write
	pm3 := acquirePendingMessage(message.AcquireMessage())
	sw.ch <- pm3
	err := <-pm3.done
	if err == nil {
		t.Errorf("expected circuit breaker error, got nil")
	}

	// Wait for cool down
	time.Sleep(150 * time.Millisecond)

	// Send message -> should be half-open and success -> close
	pm4 := acquirePendingMessage(message.AcquireMessage())
	sw.ch <- pm4
	<-pm4.done
	if sw.cbStatus != "closed" {
		t.Errorf("expected closed after success in half-open, got %s", sw.cbStatus)
	}
}
