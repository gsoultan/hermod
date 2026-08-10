package source

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
)

// ConnectGocql establishes a gocql session without blocking past the caller's
// context.
//
// gocql exposes no context-aware CreateSession: it blocks for its own
// ConnectTimeout (and retries across the host list) regardless of what the
// caller asked for. Against an unreachable cluster that is tens of seconds. A
// readiness probe with a two-second budget therefore does not time out — it
// hangs, holding a goroutine, and reports nothing either way.
//
// This runs the connect off the caller's goroutine and returns as soon as
// either the session is ready or ctx is done.
//
// The spawned goroutine is not leaked. The result channel is buffered, so a
// late CreateSession still completes rather than blocking forever on send, and
// the abandoned session is closed so an established connection is not stranded.
func ConnectGocql(ctx context.Context, cluster *gocql.ClusterConfig) (*gocql.Session, error) {
	type result struct {
		session *gocql.Session
		err     error
	}

	resultCh := make(chan result, 1)
	go func() {
		s, err := cluster.CreateSession()
		resultCh <- result{session: s, err: err}
	}()

	select {
	case res := <-resultCh:
		if res.err != nil {
			return nil, res.err
		}
		return res.session, nil

	case <-ctx.Done():
		// Hand the cleanup to another goroutine: CreateSession is still in
		// flight and may yet return a live session that would otherwise leak.
		go func() {
			if res := <-resultCh; res.session != nil {
				res.session.Close()
			}
		}()
		return nil, fmt.Errorf("gocql connect cancelled: %w", ctx.Err())
	}
}
