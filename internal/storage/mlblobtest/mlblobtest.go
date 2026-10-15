// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package mlblobtest is the contract every storage.MLBlobStore keeps, run
// against each backend by its own tests.
package mlblobtest

import (
	"bytes"
	"errors"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// Run checks st against the MLBlobStore contract. vhost names a vhost the
// test owns; a second one, vhost+"-other", must hold nothing at first.
func Run(t *testing.T, st storage.MLBlobStore, vhost string) {
	t.Helper()
	other := vhost + "-other"
	t.Run("put and get", func(t *testing.T) { putAndGet(t, st, vhost) })
	t.Run("replace never leaves old chunks", func(t *testing.T) { replace(t, st, vhost) })
	t.Run("create claims a key once", func(t *testing.T) { create(t, st, vhost) })
	t.Run("list by prefix", func(t *testing.T) { list(t, st, vhost, other) })
	t.Run("delete by prefix", func(t *testing.T) { deleteByPrefix(t, st, vhost, other) })
	t.Run("refuses bad keys", func(t *testing.T) { refuses(t, st, vhost) })
}

// big is a blob of n bytes that is not all one value, so a chunk out of
// place shows.
func big(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/storage.MLBlobChunkBytes)
	}
	return b
}

func putAndGet(t *testing.T, st storage.MLBlobStore, vhost string) {
	ctx := t.Context()
	if _, err := st.GetMLBlob(ctx, vhost, "a/missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("GetMLBlob of a missing key = %v, want ErrNotFound", err)
	}
	for _, tc := range []struct {
		key  string
		data []byte
	}{
		{"a/small", []byte(`{"rows":3}`)},
		{"a/empty", []byte{}},
		{"a/large", big(2*storage.MLBlobChunkBytes + 5)},
		{"a/exact", big(storage.MLBlobChunkBytes)},
	} {
		if err := st.PutMLBlob(ctx, vhost, tc.key, tc.data); err != nil {
			t.Fatalf("PutMLBlob(%s): %v", tc.key, err)
		}
		got, err := st.GetMLBlob(ctx, vhost, tc.key)
		if err != nil || !bytes.Equal(got, tc.data) {
			t.Errorf("GetMLBlob(%s) = %d bytes, %v; want the %d bytes put", tc.key, len(got), err, len(tc.data))
		}
	}
	if _, err := st.DeleteMLBlobs(ctx, vhost, "a/"); err != nil {
		t.Fatal(err)
	}
}

func replace(t *testing.T, st storage.MLBlobStore, vhost string) {
	ctx := t.Context()
	if err := st.PutMLBlob(ctx, vhost, "r/x", big(storage.MLBlobChunkBytes+10)); err != nil {
		t.Fatal(err)
	}
	if err := st.PutMLBlob(ctx, vhost, "r/x", []byte("short")); err != nil {
		t.Fatal(err)
	}
	if got, err := st.GetMLBlob(ctx, vhost, "r/x"); err != nil || string(got) != "short" {
		t.Errorf("after replacing, GetMLBlob = %q, %v; want \"short\"", got, err)
	}
	list, err := st.ListMLBlobs(ctx, vhost, "r/", true)
	if err != nil || len(list) != 1 || list[0].Size != 5 || string(list[0].Data) != "short" {
		t.Errorf("after replacing, ListMLBlobs = %+v, %v; want one blob of 5 bytes", list, err)
	}
	if n, err := st.DeleteMLBlobs(ctx, vhost, "r/"); err != nil || n != 1 {
		t.Errorf("DeleteMLBlobs = %d, %v; want 1 blob", n, err)
	}
}

func create(t *testing.T, st storage.MLBlobStore, vhost string) {
	ctx := t.Context()
	if err := st.CreateMLBlob(ctx, vhost, "c/v1", []byte("first")); err != nil {
		t.Fatalf("CreateMLBlob: %v", err)
	}
	if err := st.CreateMLBlob(ctx, vhost, "c/v1", []byte("second")); !errors.Is(err, storage.ErrMLBlobExists) {
		t.Errorf("CreateMLBlob of a taken key = %v, want ErrMLBlobExists", err)
	}
	if got, _ := st.GetMLBlob(ctx, vhost, "c/v1"); string(got) != "first" {
		t.Errorf("the losing create changed the blob to %q", got)
	}
	if _, err := st.DeleteMLBlobs(ctx, vhost, "c/"); err != nil {
		t.Fatal(err)
	}
}

func list(t *testing.T, st storage.MLBlobStore, vhost, other string) {
	ctx := t.Context()
	put := func(vh, key, data string) {
		t.Helper()
		if err := st.PutMLBlob(ctx, vh, key, []byte(data)); err != nil {
			t.Fatalf("PutMLBlob(%s): %v", key, err)
		}
	}
	// Written out of order; a prefix must not match a longer name, and LIKE
	// wildcards in a key are plain characters.
	put(vhost, "l/ab/2", "bb")
	put(vhost, "l/ab/1", "a")
	put(vhost, "l/abc/1", "ccc")
	put(vhost, "l/x_y/1", "u")
	put(vhost, "l/xzy/1", "z")
	put(vhost, "l/50%/1", "p")
	put(vhost, "l/500/1", "q")
	put(other, "l/ab/9", "other vhost")

	got, err := st.ListMLBlobs(ctx, vhost, "l/ab/", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "l/ab/1" || got[1].Key != "l/ab/2" || got[1].Size != 2 || got[0].Data != nil ||
		got[0].UpdatedAt.IsZero() {
		t.Errorf("ListMLBlobs(l/ab/) = %+v; want l/ab/1 and l/ab/2 in order, sized, without data", got)
	}
	withData, _ := st.ListMLBlobs(ctx, vhost, "l/ab/", true)
	if len(withData) != 2 || string(withData[1].Data) != "bb" {
		t.Errorf("ListMLBlobs with data = %+v", withData)
	}
	listLiterally(t, st, vhost)
	if _, err := st.DeleteMLBlobs(ctx, vhost, "l/"); err != nil {
		t.Fatal(err)
	}
}

// listLiterally checks that LIKE or regex wildcards in a prefix match only
// themselves, and that "" lists the whole vhost.
func listLiterally(t *testing.T, st storage.MLBlobStore, vhost string) {
	ctx := t.Context()
	for prefix, want := range map[string]string{"l/x_y/": "l/x_y/1", "l/50%/": "l/50%/1"} {
		if got, _ := st.ListMLBlobs(ctx, vhost, prefix, false); len(got) != 1 || got[0].Key != want {
			t.Errorf("ListMLBlobs(%s) = %+v; want only %s", prefix, got, want)
		}
	}
	if all, _ := st.ListMLBlobs(ctx, vhost, "", false); len(all) != 7 {
		t.Errorf("ListMLBlobs of the whole vhost = %d blobs, want 7", len(all))
	}
}

func deleteByPrefix(t *testing.T, st storage.MLBlobStore, vhost, other string) {
	ctx := t.Context()
	for _, key := range []string{"d/m/1", "d/m/2", "d/mm/1"} {
		if err := st.PutMLBlob(ctx, vhost, key, big(10)); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := st.DeleteMLBlobs(ctx, vhost, "d/m/"); err != nil || n != 2 {
		t.Errorf("DeleteMLBlobs(d/m/) = %d, %v; want 2", n, err)
	}
	if _, err := st.GetMLBlob(ctx, vhost, "d/mm/1"); err != nil {
		t.Errorf("a blob under a longer name went too: %v", err)
	}
	if n, err := st.DeleteMLBlobs(ctx, vhost, ""); err != nil || n != 1 {
		t.Errorf("DeleteMLBlobs of the whole vhost = %d, %v; want 1", n, err)
	}
	if got, _ := st.ListMLBlobs(ctx, other, "", false); len(got) != 1 {
		t.Errorf("deleting one vhost's blobs touched another's: %+v", got)
	}
	if _, err := st.DeleteMLBlobs(ctx, other, ""); err != nil {
		t.Fatal(err)
	}
}

func refuses(t *testing.T, st storage.MLBlobStore, vhost string) {
	ctx := t.Context()
	for _, key := range []string{"", "a//b", "../x", "a/./b", "a/\x00"} {
		if err := st.PutMLBlob(ctx, vhost, key, []byte("x")); err == nil {
			t.Errorf("PutMLBlob accepted the key %q", key)
		}
	}
	if err := st.PutMLBlob(ctx, "", "a/b", []byte("x")); err == nil {
		t.Error("PutMLBlob accepted an empty vhost")
	}
	if _, err := st.ListMLBlobs(ctx, vhost, "a", false); err == nil {
		t.Error("ListMLBlobs accepted a prefix not ending in '/'")
	}
}
