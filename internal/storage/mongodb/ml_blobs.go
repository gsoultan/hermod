// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package mongodb

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/gsoultan/hermod/internal/storage"
)

const mlBlobsCollection = "ml_blobs"

// A blob is a head document, _id vhost + "/" + key, holding the first chunk,
// and one document per further chunk, _id vhost + "/" + key + "#" + gen +
// "#" + chunk. gen names one write. MongoDB without a replica set has no
// transactions, so a write puts its further chunks first under a new gen,
// then swaps the head to it, then removes the old gen's chunks: a reader that
// follows the head finds every chunk of one write, and one that loses a race
// with a removal reads the head again.
type mlBlobDoc struct {
	ID        string    `bson:"_id"`
	VHost     string    `bson:"vhost"`
	Key       string    `bson:"key"`
	Head      bool      `bson:"head"`
	Gen       string    `bson:"gen"`
	Chunk     int       `bson:"chunk"`
	Chunks    int       `bson:"chunks"`
	Size      int64     `bson:"size"`
	Data      []byte    `bson:"data"`
	UpdatedAt time.Time `bson:"updated_at"`
}

func mlBlobHeadID(vhost, key string) string { return vhost + "/" + key }

func mlBlobChunkID(vhost, key, gen string, chunk int) string {
	return vhost + "/" + key + "#" + gen + "#" + strconv.Itoa(chunk)
}

func newMLBlobGen() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// mlBlobPrefix matches the keys starting with prefix.
func mlBlobPrefix(vhost, prefix string) bson.M {
	return bson.M{"vhost": vhost, "key": bson.M{"$regex": "^" + regexp.QuoteMeta(prefix)}}
}

// writeMLBlob writes data under key; with create, only if the key is free.
func (s *mongoStorage) writeMLBlob(ctx context.Context, vhost, key string, data []byte, create bool) error {
	if err := storage.ValidateMLBlob(vhost, key, len(data)); err != nil {
		return err
	}
	coll := s.db.Collection(mlBlobsCollection)
	if create {
		if n, err := coll.CountDocuments(ctx, bson.M{"_id": mlBlobHeadID(vhost, key)}); err != nil {
			return fmt.Errorf("saving ML blob %q of vhost %q: %w", key, vhost, err)
		} else if n > 0 {
			return storage.ErrMLBlobExists
		}
	}
	chunks, gen, now := storage.MLBlobChunks(data), newMLBlobGen(), time.Now().UTC()
	doc := func(i int) mlBlobDoc {
		return mlBlobDoc{ID: mlBlobChunkID(vhost, key, gen, i), VHost: vhost, Key: key, Gen: gen, Chunk: i,
			Chunks: len(chunks), Size: int64(len(data)), Data: chunks[i], UpdatedAt: now}
	}
	for i := 1; i < len(chunks); i++ {
		if _, err := coll.InsertOne(ctx, doc(i)); err != nil {
			s.dropMLBlobGen(ctx, vhost, key, gen)
			return fmt.Errorf("saving ML blob %q of vhost %q: %w", key, vhost, err)
		}
	}
	head := doc(0)
	head.ID, head.Head = mlBlobHeadID(vhost, key), true
	var err error
	if create {
		_, err = coll.InsertOne(ctx, head)
		if mongo.IsDuplicateKeyError(err) {
			s.dropMLBlobGen(ctx, vhost, key, gen)
			return storage.ErrMLBlobExists
		}
	} else {
		_, err = coll.ReplaceOne(ctx, bson.M{"_id": head.ID}, head, options.Replace().SetUpsert(true))
	}
	if err != nil {
		s.dropMLBlobGen(ctx, vhost, key, gen)
		return fmt.Errorf("saving ML blob %q of vhost %q: %w", key, vhost, err)
	}
	// The old write's chunks; the head no longer leads to them.
	_, _ = coll.DeleteMany(ctx, bson.M{"vhost": vhost, "key": key, "head": false, "gen": bson.M{"$ne": gen}})
	return nil
}

func (s *mongoStorage) dropMLBlobGen(ctx context.Context, vhost, key, gen string) {
	_, _ = s.db.Collection(mlBlobsCollection).DeleteMany(ctx, bson.M{"vhost": vhost, "key": key, "head": false, "gen": gen})
}

func (s *mongoStorage) PutMLBlob(ctx context.Context, vhost, key string, data []byte) error {
	return s.writeMLBlob(ctx, vhost, key, data, false)
}

func (s *mongoStorage) CreateMLBlob(ctx context.Context, vhost, key string, data []byte) error {
	return s.writeMLBlob(ctx, vhost, key, data, true)
}

// errMLBlobMoved is a read that lost a race with a newer write.
var errMLBlobMoved = errors.New("the ML blob changed while it was read")

// assemble joins a head's chunks; errMLBlobMoved when its gen is gone.
func (s *mongoStorage) assemble(ctx context.Context, head mlBlobDoc) ([]byte, error) {
	if head.Chunks <= 1 {
		return head.Data, nil
	}
	cur, err := s.db.Collection(mlBlobsCollection).Find(ctx,
		bson.M{"vhost": head.VHost, "key": head.Key, "head": false, "gen": head.Gen},
		options.Find().SetSort(bson.D{{Key: "chunk", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var rest []mlBlobDoc
	if err := cur.All(ctx, &rest); err != nil {
		return nil, err
	}
	if len(rest) != head.Chunks-1 {
		return nil, errMLBlobMoved
	}
	parts := [][]byte{head.Data}
	for _, d := range rest {
		parts = append(parts, d.Data)
	}
	return bytes.Join(parts, nil), nil
}

func (s *mongoStorage) GetMLBlob(ctx context.Context, vhost, key string) ([]byte, error) {
	if err := storage.ValidateMLBlob(vhost, key, 0); err != nil {
		return nil, err
	}
	var err error
	for range 3 {
		var head mlBlobDoc
		err = s.db.Collection(mlBlobsCollection).FindOne(ctx, bson.M{"_id": mlBlobHeadID(vhost, key)}).Decode(&head)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, storage.ErrNotFound
		}
		if err != nil {
			break
		}
		var data []byte
		if data, err = s.assemble(ctx, head); err == nil {
			return data, nil
		}
		if !errors.Is(err, errMLBlobMoved) {
			break
		}
	}
	return nil, fmt.Errorf("reading ML blob %q of vhost %q: %w", key, vhost, err)
}

func (s *mongoStorage) ListMLBlobs(ctx context.Context, vhost, prefix string, withData bool) ([]storage.MLBlob, error) {
	if err := storage.ValidateMLBlobPrefix(vhost, prefix); err != nil {
		return nil, err
	}
	filter := mlBlobPrefix(vhost, prefix)
	filter["head"] = true
	opts := options.Find()
	if !withData {
		opts.SetProjection(bson.M{"data": 0})
	}
	cur, err := s.db.Collection(mlBlobsCollection).Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("listing ML blobs %q of vhost %q: %w", prefix, vhost, err)
	}
	var heads []mlBlobDoc
	if err := cur.All(ctx, &heads); err != nil {
		return nil, fmt.Errorf("listing ML blobs %q of vhost %q: %w", prefix, vhost, err)
	}
	out := make([]storage.MLBlob, 0, len(heads))
	for _, h := range heads {
		b := storage.MLBlob{Key: h.Key, Size: h.Size, UpdatedAt: h.UpdatedAt}
		if withData {
			data, err := s.assemble(ctx, h)
			if errors.Is(err, errMLBlobMoved) {
				data, err = s.GetMLBlob(ctx, vhost, h.Key)
			}
			if err != nil {
				return nil, fmt.Errorf("listing ML blobs %q of vhost %q: %w", prefix, vhost, err)
			}
			b.Data = data
		}
		out = append(out, b)
	}
	slices.SortFunc(out, func(a, b storage.MLBlob) int { return strings.Compare(a.Key, b.Key) })
	return out, nil
}

func (s *mongoStorage) DeleteMLBlobs(ctx context.Context, vhost, prefix string) (int, error) {
	if err := storage.ValidateMLBlobPrefix(vhost, prefix); err != nil {
		return 0, err
	}
	coll := s.db.Collection(mlBlobsCollection)
	heads := mlBlobPrefix(vhost, prefix)
	heads["head"] = true
	n, err := coll.CountDocuments(ctx, heads)
	if err != nil {
		return 0, fmt.Errorf("deleting ML blobs %q of vhost %q: %w", prefix, vhost, err)
	}
	if _, err := coll.DeleteMany(ctx, mlBlobPrefix(vhost, prefix)); err != nil {
		return 0, fmt.Errorf("deleting ML blobs %q of vhost %q: %w", prefix, vhost, err)
	}
	return int(n), nil
}
