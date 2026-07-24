package batcher

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBatcher(t *testing.T) {
	var callCount int32
	batchFn := func(ctx context.Context, keys []string) (map[string]int, error) {
		atomic.AddInt32(&callCount, 1)
		results := make(map[string]int)
		for _, k := range keys {
			results[k] = len(k)
		}
		return results, nil
	}

	b := NewBatcher(5, 50*time.Millisecond, batchFn)
	defer b.Close()

	var wg sync.WaitGroup
	numRequests := 10
	results := make([]int, numRequests)
	errs := make([]error, numRequests)

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", i)
			val, err := b.Execute(context.Background(), key)
			results[i] = val
			errs[i] = err
		}(i)
		// Small delay to staggered starts but still within batch window
		time.Sleep(2 * time.Millisecond)
	}

	wg.Wait()

	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("Expected 2 batch calls (10 requests, batch size 5), got %d", callCount)
	}

	for i := 0; i < numRequests; i++ {
		if errs[i] != nil {
			t.Errorf("Request %d failed: %v", i, errs[i])
		}
		expected := len(fmt.Sprintf("key-%d", i))
		if results[i] != expected {
			t.Errorf("Request %d: expected %d, got %d", i, expected, results[i])
		}
	}
}

func TestBatcherTimeout(t *testing.T) {
	var callCount int32
	batchFn := func(ctx context.Context, keys []string) (map[string]int, error) {
		atomic.AddInt32(&callCount, 1)
		results := make(map[string]int)
		for _, k := range keys {
			results[k] = 1
		}
		return results, nil
	}

	b := NewBatcher(100, 20*time.Millisecond, batchFn)
	defer b.Close()

	val, err := b.Execute(context.Background(), "test")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if val != 1 {
		t.Errorf("Expected 1, got %d", val)
	}

	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("Expected 1 batch call, got %d", callCount)
	}
}

func TestBatcherCancellation(t *testing.T) {
	batchFn := func(ctx context.Context, keys []string) (map[string]int, error) {
		time.Sleep(100 * time.Millisecond)
		return nil, nil
	}

	b := NewBatcher(10, 100*time.Millisecond, batchFn)
	defer b.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := b.Execute(ctx, "test")
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}
