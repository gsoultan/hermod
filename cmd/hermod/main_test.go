package main

import (
	"context"
	"github.com/user/hermod/internal/storage"
	"testing"
)

type fakeUserLister struct {
	users []storage.User
	err   error
}

func (f *fakeUserLister) ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	// ignore filter for simplicity
	return f.users, len(f.users), nil
}

func TestComputeSetupStatus(t *testing.T) {
	ctx := t.Context()

	cases := []struct {
		name       string
		configured bool
		store      userLister
		wantCfg    bool
		wantUsers  bool
	}{
		{name: "not configured, nil store", configured: false, store: nil, wantCfg: false, wantUsers: false},
		{name: "configured, nil store", configured: true, store: nil, wantCfg: true, wantUsers: false},
		{name: "configured, no users", configured: true, store: &fakeUserLister{users: nil}, wantCfg: true, wantUsers: false},
		{name: "configured, with users", configured: true, store: &fakeUserLister{users: []storage.User{{ID: "u1", Username: "admin"}}}, wantCfg: true, wantUsers: true},
		{name: "configured, list error", configured: true, store: &fakeUserLister{err: context.DeadlineExceeded}, wantCfg: true, wantUsers: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCfg, gotUsers := computeSetupStatus(ctx, tc.store, tc.configured)
			if gotCfg != tc.wantCfg || gotUsers != tc.wantUsers {
				t.Fatalf("computeSetupStatus() = (%v,%v), want (%v,%v)", gotCfg, gotUsers, tc.wantCfg, tc.wantUsers)
			}
		})
	}
}
