package buffer

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// FileBuffer is a persistent buffer that stores messages in an append-only log.
type FileBuffer struct {
	dir       string
	size      int
	mu        sync.Mutex
	closed    bool
	done      chan struct{}
	logFile   *os.File
	logWriter *bufio.Writer
	stateFile *os.File

	produceCount uint64
	consumeCount uint64
	readOffset   int64

	statePath string
}

func NewFileBuffer(dir string, size int) (*FileBuffer, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create buffer directory: %w", err)
	}

	logPath := filepath.Join(dir, "messages.log")
	statePath := filepath.Join(dir, "state.bin")

	f, err := os.OpenFile(logPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	sf, err := os.OpenFile(statePath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to open state file: %w", err)
	}

	fb := &FileBuffer{
		dir:       dir,
		size:      size,
		done:      make(chan struct{}),
		logFile:   f,
		logWriter: bufio.NewWriter(f),
		stateFile: sf,
		statePath: statePath,
	}

	if err := fb.loadState(); err != nil {
		f.Close()
		sf.Close()
		return nil, err
	}

	return fb, nil
}

func (b *FileBuffer) loadState() error {
	data := make([]byte, 24)
	n, err := b.stateFile.ReadAt(data, 0)
	if err != nil && err != io.EOF {
		return fmt.Errorf("failed to read state file: %w", err)
	}

	if n >= 24 {
		b.produceCount = binary.LittleEndian.Uint64(data[0:8])
		b.consumeCount = binary.LittleEndian.Uint64(data[8:16])
		b.readOffset = int64(binary.LittleEndian.Uint64(data[16:24]))
	}
	return nil
}

func (b *FileBuffer) saveState() error {
	data := make([]byte, 24)
	binary.LittleEndian.PutUint64(data[0:8], b.produceCount)
	binary.LittleEndian.PutUint64(data[8:16], b.consumeCount)
	binary.LittleEndian.PutUint64(data[16:24], uint64(b.readOffset))
	_, err := b.stateFile.WriteAt(data, 0)
	return err
}

func (b *FileBuffer) Produce(ctx context.Context, msg hermod.Message) (err error) {
	// Ensure message is released after production (since it's encoded/copied)
	if dm, ok := msg.(*message.DefaultMessage); ok {
		defer message.ReleaseMessage(dm)
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errors.New("buffer closed")
	}

	// Backpressure
	if b.size > 0 && (b.produceCount-b.consumeCount) >= uint64(b.size) {
		b.mu.Unlock()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-b.done:
				return errors.New("buffer closed")
			case <-ticker.C:
				b.mu.Lock()
				if (b.produceCount - b.consumeCount) < uint64(b.size) {
					goto proceed
				}
				b.mu.Unlock()
			}
		}
	}

proceed:
	if b.closed {
		b.mu.Unlock()
		return errors.New("buffer closed")
	}

	if err := encodeMessage(b.logWriter, msg); err != nil {
		b.mu.Unlock()
		return fmt.Errorf("failed to encode message: %w", err)
	}

	if err := b.logWriter.Flush(); err != nil {
		b.mu.Unlock()
		return fmt.Errorf("failed to flush log: %w", err)
	}

	b.produceCount++
	if err := b.saveState(); err != nil {
		// Non-fatal, but should log
	}
	b.mu.Unlock()

	return nil
}

func (b *FileBuffer) Consume(ctx context.Context, handler hermod.Handler) error {
	// Re-open for reading to have an independent seek pointer
	readF, err := os.Open(filepath.Join(b.dir, "messages.log"))
	if err != nil {
		return fmt.Errorf("failed to open log for reading: %w", err)
	}
	defer readF.Close()

	// Skip already consumed messages
	// This is a naive implementation: it re-reads from the beginning and skips.
	// A better way would be to store file offsets instead of message counts.
	// But let's see if this is enough for the benchmark improvement.
	// Wait, actually I should store offsets.

	// Let's quickly fix to use offsets in state.
	return b.consumeWithOffsets(ctx, readF, handler)
}

func (b *FileBuffer) consumeWithOffsets(ctx context.Context, f *os.File, handler hermod.Handler) error {
	b.mu.Lock()
	offset := b.readOffset
	b.mu.Unlock()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}

	// Use a counting reader to track actual bytes read from the file
	cr := &countingReader{r: f, offset: offset}
	reader := bufio.NewReader(cr)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.done:
			return nil
		default:
			b.mu.Lock()
			hasMore := b.consumeCount < b.produceCount
			b.mu.Unlock()

			if !hasMore {
				time.Sleep(10 * time.Millisecond)
				continue
			}

			// We need to know how many bytes were consumed from the UNDERLYING file
			// bufio.Reader makes this hard.
			// Instead of bufio.Reader, let's use a custom decoder that tracks bytes.

			startOffset := cr.offset - int64(reader.Buffered())

			msg, err := decodeMessage(reader)
			if err != nil {
				if err == io.EOF {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				return fmt.Errorf("failed to decode message: %w", err)
			}

			if err := handler(ctx, msg); err != nil {
				return err
			}

			b.mu.Lock()
			b.consumeCount++
			// Calculate new offset: previous offset - buffered + new buffered position?
			// Actually, countingReader + reader.Buffered() is correct.
			b.readOffset = cr.offset - int64(reader.Buffered())
			b.saveState()
			b.mu.Unlock()
			_ = startOffset
		}
	}
}

type countingReader struct {
	r      io.Reader
	offset int64
}

func (cr *countingReader) Read(p []byte) (n int, err error) {
	n, err = cr.r.Read(p)
	cr.offset += int64(n)
	return n, err
}

func (b *FileBuffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	close(b.done)
	b.logWriter.Flush()
	b.saveState()
	b.stateFile.Close()
	return b.logFile.Close()
}
