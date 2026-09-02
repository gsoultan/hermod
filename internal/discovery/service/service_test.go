package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/factory"
)

// The discovery service is what the UI talks to when an operator points Hermod
// at a database: test the connection, list the tables, sample a row. Everything
// it does goes through ComponentCreator, so all of it is reachable with fakes
// and none of it needs a server.
//
// What these tests are about is what an operator is told when discovery fails.
// A wrong answer here is expensive in a way a wrong answer elsewhere is not:
// discovery is the screen where somebody is deciding whether their credentials
// are right, and it is the one place where "this is not supported" and "this
// failed" look identical from the outside.

type fakeCreator struct {
	src    hermod.Source
	snk    hermod.Sink
	srcErr error
	snkErr error
}

func (f *fakeCreator) CreateSource(context.Context, factory.SourceConfig) (hermod.Source, error) {
	return f.src, f.srcErr
}

func (f *fakeCreator) CreateSink(context.Context, factory.SinkConfig) (hermod.Sink, error) {
	return f.snk, f.snkErr
}

func (f *fakeCreator) GetSourceFactoryConfig(context.Context, string) (factory.SourceConfig, error) {
	return factory.SourceConfig{}, errors.New("not used in these tests")
}

func (f *fakeCreator) GetDB(context.Context, string, map[string]string) (*sql.DB, error) {
	return nil, errors.New("not used in these tests")
}

// stubSink satisfies hermod.Sink and, optionally, Browser.
type stubSink struct {
	browse func(ctx context.Context, table string, limit int) ([]hermod.Message, error)
}

func (s *stubSink) Write(context.Context, hermod.Message) error { return nil }
func (s *stubSink) Ping(context.Context) error                  { return nil }
func (s *stubSink) Close() error                                { return nil }

type browsableSink struct{ *stubSink }

func (b *browsableSink) Browse(ctx context.Context, table string, limit int) ([]hermod.Message, error) {
	return b.browse(ctx, table, limit)
}

type stubSource struct {
	sample func(ctx context.Context, table string) (hermod.Message, error)
}

func (s *stubSource) Read(context.Context) (hermod.Message, error) { return nil, nil }
func (s *stubSource) Ack(context.Context, hermod.Message) error    { return nil }
func (s *stubSource) Ping(context.Context) error                   { return nil }
func (s *stubSource) Close() error                                 { return nil }
func (s *stubSource) Sample(ctx context.Context, t string) (hermod.Message, error) {
	return s.sample(ctx, t)
}

// A sink that can browse but whose browse failed must say so. Reporting "does
// not support browsing" sends the operator looking for a missing feature when
// what actually happened is that their table name was wrong, or their grants
// were, and the real message said which.
func TestABrowseFailureIsNotReportedAsAMissingFeature(t *testing.T) {
	boom := errors.New("permission denied for table orders")
	svc := NewDiscoveryService(&fakeCreator{
		snk: &browsableSink{&stubSink{
			browse: func(context.Context, string, int) ([]hermod.Message, error) {
				return nil, boom
			},
		}},
	})

	// Limit 2 so the Sampler fallback (which only applies at limit 1) is not in
	// the way: this sink browses, and browsing is what failed.
	_, err := svc.BrowseSinkTable(context.Background(), factory.SinkConfig{Type: "postgres"}, "orders", 2)
	if err == nil {
		t.Fatal("browsing a sink whose Browse returned an error reported success")
	}
	if strings.Contains(err.Error(), "does not support browsing") {
		t.Errorf("a sink that does support browsing, and whose browse failed with %q, "+
			"was reported as not supporting browsing: %v\n"+
			"the real failure is discarded, so the operator is told to look for a "+
			"missing feature instead of at the error the database returned", boom, err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("the underlying browse error was lost; got %v, want it to wrap %v", err, boom)
	}
}

// A sampler that finds nothing and says so without an error must not take the
// request down with it. The result is asserted to hermod.Message, and a nil
// interface boxed in `any` fails that assertion — outside the recover() that
// guards the rest of this package.
func TestSamplingAnEmptyTableDoesNotPanic(t *testing.T) {
	svc := NewDiscoveryService(&fakeCreator{
		src: &stubSource{
			sample: func(context.Context, string) (hermod.Message, error) {
				return nil, nil // no row, no error: the table is empty
			},
		},
	})

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("sampling a table that returned no row panicked: %v\n"+
				"the type assertion on the discovery result is unchecked, and it is "+
				"outside the recover() that protects the rest of this package, so "+
				"this reaches the HTTP handler", rec)
		}
	}()

	msg, err := svc.SampleTable(context.Background(), factory.SourceConfig{Type: "postgres"}, "orders")
	if err == nil && msg == nil {
		t.Error("sampling an empty table reported neither a row nor a reason")
	}
	if err != nil && !strings.Contains(err.Error(), "orders") {
		t.Errorf("sampling an empty table did not name the table: %v", err)
	}
}

// TestSource refuses a factory that hands back a nil source with a clear
// message. TestSink is the same call on the other side of the pipeline and had
// no such check, so it dereferenced the nil instead.
//
// This asserts on the refusal rather than on the dereference, deliberately. The
// unguarded version panics on Linux and is recovered into ErrOperationPanicked,
// but hangs until the 30s work deadline in a sandbox that does not deliver
// SIGSEGV — so a test written against the panic reports two different things in
// two places, and the slower one looks like a pass. What matters either way is
// that the nil is named before anything touches it, promptly.
func TestTestingANilSinkIsRefusedNotDereferenced(t *testing.T) {
	svc := NewDiscoveryService(&fakeCreator{snk: nil, snkErr: nil})

	done := make(chan error, 1)
	go func() {
		done <- svc.TestSink(context.Background(), factory.SinkConfig{Type: "postgres"})
	}()

	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("testing a sink that was never built did not answer within 5s; " +
			"the nil reached the connection attempt instead of being refused up front")
	}

	if err == nil {
		t.Fatal("testing a sink that was never built reported a healthy connection")
	}
	if errors.Is(err, ErrOperationPanicked) {
		t.Errorf("testing a nil sink panicked instead of being refused: %v", err)
	}
	if !strings.Contains(err.Error(), "nil sink") {
		t.Errorf("testing a nil sink did not say so; got %v\n"+
			"TestSource explains this case in a sentence, and TestSink is the same "+
			"call on the other side of the same pipeline", err)
	}
}
