package evaluator

import (
	"context"
	"github.com/user/hermod"
)

type OutboxTransactionalSource struct {
	hermod.Source
}

func (s *OutboxTransactionalSource) Begin(ctx context.Context) error {
	if t, ok := s.Source.(hermod.Transactional); ok {
		return t.Begin(ctx)
	}
	return nil
}

func (s *OutboxTransactionalSource) Commit(ctx context.Context) error {
	if t, ok := s.Source.(hermod.Transactional); ok {
		return t.Commit(ctx)
	}
	return nil
}

func (s *OutboxTransactionalSource) Rollback(ctx context.Context) error {
	if t, ok := s.Source.(hermod.Transactional); ok {
		return t.Rollback(ctx)
	}
	return nil
}
