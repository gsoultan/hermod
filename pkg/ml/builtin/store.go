package builtin

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/builtin/table"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// The engine's blobs (Blobs), per vhost:
//
//	dataset-heads/{name}/head          {"gen": G}: the dataset's current generation
//	datasets/{name}/{G}/data/{part}    a part's values, gzipped JSON (partFile)
//	datasets/{name}/{G}/info/{part}    the part's rows and column kinds (partInfo)
//	models/{name}/{version}/meta       a version and where its artifact is (versionMeta)
//	artifacts/{name}/{id}/artifact     a version's model.json.gz or model.rec
//	counters/{name}                    the highest version number the model was given
//
// Nothing here relies on a lock or a transaction, so several Hermod servers
// can share one store. A part is written once under a fresh id, its data
// before its info, so whoever lists a part can read it. Replacing a dataset
// writes a new generation and then points the head at it. A version number is
// claimed with Create, so two servers training one model at once get two
// numbers; the counter keeps a deleted model's numbers from being given
// again, so a version id always means one model.

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func checkName(kind, name string) error {
	if !validName.MatchString(name) || name == "." || name == ".." {
		return badRequest("%s name %q may hold only letters, digits, '_', '.' or '-'", kind, name)
	}
	return nil
}

func tempName(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// newID is a fresh id that sorts by when it was made, then at random.
func newID() string {
	return fmt.Sprintf("%020d", time.Now().UnixNano()) + tempName("-")
}

// writeFileAtomic writes data to name through a temporary file beside it.
func writeFileAtomic(root *os.Root, name string, data []byte) error {
	tmp := path.Join(path.Dir(name), tempName(".tmp-"))
	if err := root.WriteFile(tmp, data, 0o600); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

// exists reports whether name is in root.
func exists(root *os.Root, name string) bool {
	_, err := root.Stat(name)
	return err == nil
}

// removeDir takes a directory out of sight by renaming it, then deletes it.
func removeDir(root *os.Root, dir string) error {
	trash := path.Join(path.Dir(dir), tempName(".trash-"))
	if err := root.Rename(dir, trash); err != nil {
		return err
	}
	return root.RemoveAll(trash)
}

func gzipped(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gunzipped(raw []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	return io.ReadAll(zr)
}

// -- datasets ----------------------------------------------------------------

// datasetStore keeps datasets as parts, one per append.
type datasetStore struct {
	blobs Blobs
}

// datasetHead points at the dataset's current generation of parts.
type datasetHead struct {
	Gen string `json:"gen"`
}

type partInfo struct {
	ID      string         `json:"id"`
	Rows    int            `json:"rows"`
	Columns []partColumnKV `json:"columns"`
}

// partColumnKV is a part's column and its kind; Kind is "" when the part
// holds no value in it, so it does not vote on the dataset's kind.
type partColumnKV struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// partFile is a part as it is written: its columns' values, a missing one
// as null.
type partFile struct {
	Rows    int          `json:"rows"`
	Columns []partValues `json:"columns"`
}

type partValues struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Values []any  `json:"values"`
}

func checkDatasetName(vhost, name string) error {
	if err := checkName("vhost", vhost); err != nil {
		return err
	}
	return checkName("dataset", name)
}

func headKey(name string) string { return "dataset-heads/" + name + "/head" }

func genPrefix(name, gen string) string { return "datasets/" + name + "/" + gen + "/" }

func notFoundDataset(vhost, name string) error {
	return notFound("Dataset %q does not exist in vhost %q.", name, vhost)
}

// head is the dataset's generation, "" when there is no such dataset.
func (s *datasetStore) head(ctx context.Context, vhost, name string) (string, error) {
	raw, err := s.blobs.Get(ctx, vhost, headKey(name))
	if errors.Is(err, ErrBlobNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var h datasetHead
	if err := json.Unmarshal(raw, &h); err != nil {
		return "", fmt.Errorf("dataset %q has an unreadable head: %w", name, err)
	}
	if h.Gen == "" {
		return "", fmt.Errorf("dataset %q has an empty head", name)
	}
	return h.Gen, nil
}

// ensureHead is the dataset's generation, made if the dataset is new. Two
// servers making it at once agree on the one that was created first.
func (s *datasetStore) ensureHead(ctx context.Context, vhost, name string) (string, error) {
	if gen, err := s.head(ctx, vhost, name); err != nil || gen != "" {
		return gen, err
	}
	raw, _ := json.Marshal(datasetHead{Gen: newID()})
	if err := s.blobs.Create(ctx, vhost, headKey(name), raw); err != nil && !errors.Is(err, ErrBlobExists) {
		return "", err
	}
	return s.head(ctx, vhost, name)
}

func encodePart(t *table.Table) (partFile, partInfo) {
	pf := partFile{Rows: t.Rows}
	info := partInfo{Rows: t.Rows}
	for _, c := range t.Columns {
		vals := make([]any, t.Rows)
		present := false
		for i := range vals {
			vals[i] = c.Value(i)
			present = present || vals[i] != nil
		}
		kind := c.Kind
		if !present {
			kind = ""
		}
		pf.Columns = append(pf.Columns, partValues{Name: c.Name, Kind: kind, Values: vals})
		info.Columns = append(info.Columns, partColumnKV{Name: c.Name, Kind: kind})
	}
	return pf, info
}

// writePart writes t as a new part of generation gen: its data, then its
// info, which is what lists it.
func (s *datasetStore) writePart(ctx context.Context, vhost, name, gen string, t *table.Table) error {
	pf, info := encodePart(t)
	info.ID = newID()
	raw, err := json.Marshal(pf)
	if err != nil {
		return err
	}
	if raw, err = gzipped(raw); err != nil {
		return err
	}
	if err := s.blobs.Put(ctx, vhost, genPrefix(name, gen)+"data/"+info.ID, raw); err != nil {
		return err
	}
	meta, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return s.blobs.Put(ctx, vhost, genPrefix(name, gen)+"info/"+info.ID, meta)
}

// append adds t as a new part, or replaces the dataset with it, and returns
// how many rows the dataset holds now.
func (s *datasetStore) append(ctx context.Context, vhost, name string, t *table.Table, replace bool) (int, error) {
	if err := checkDatasetName(vhost, name); err != nil {
		return 0, err
	}
	if replace {
		return t.Rows, s.replace(ctx, vhost, name, t)
	}
	gen, err := s.ensureHead(ctx, vhost, name)
	if err != nil {
		return 0, err
	}
	if t.Rows > 0 {
		if err := s.writePart(ctx, vhost, name, gen, t); err != nil {
			return 0, err
		}
	}
	parts, _, err := s.parts(ctx, vhost, name, gen)
	if err != nil {
		return 0, err
	}
	return rowsOf(parts), nil
}

func rowsOf(parts []partInfo) int {
	n := 0
	for _, p := range parts {
		n += p.Rows
	}
	return n
}

// replace swaps the dataset for one holding only t: a new generation, then
// the head moved to it, then the old one removed.
func (s *datasetStore) replace(ctx context.Context, vhost, name string, t *table.Table) error {
	if err := checkDatasetName(vhost, name); err != nil {
		return err
	}
	old, err := s.head(ctx, vhost, name)
	if err != nil {
		return err
	}
	gen := newID()
	if t.Rows > 0 || len(t.Columns) > 0 {
		if err := s.writePart(ctx, vhost, name, gen, t); err != nil {
			return err
		}
	}
	raw, _ := json.Marshal(datasetHead{Gen: gen})
	if err := s.blobs.Put(ctx, vhost, headKey(name), raw); err != nil {
		return err
	}
	if old != "" {
		_, _ = s.blobs.Delete(ctx, vhost, genPrefix(name, old))
	}
	return nil
}

func (s *datasetStore) delete(ctx context.Context, vhost, name string) error {
	if err := checkDatasetName(vhost, name); err != nil {
		return err
	}
	n, err := s.blobs.Delete(ctx, vhost, "dataset-heads/"+name+"/")
	if err != nil {
		return err
	}
	if _, err := s.blobs.Delete(ctx, vhost, "datasets/"+name+"/"); err != nil {
		return err
	}
	if n == 0 {
		return notFoundDataset(vhost, name)
	}
	return nil
}

// parts are the infos of generation gen, oldest first, and when the latest
// was written.
func (s *datasetStore) parts(ctx context.Context, vhost, name, gen string) ([]partInfo, time.Time, error) {
	list, err := s.blobs.List(ctx, vhost, genPrefix(name, gen)+"info/", true)
	if err != nil {
		return nil, time.Time{}, err
	}
	out := make([]partInfo, 0, len(list))
	var latest time.Time
	for _, b := range list {
		var p partInfo
		if err := json.Unmarshal(b.Data, &p); err != nil {
			return nil, time.Time{}, fmt.Errorf("dataset %q part %s is unreadable: %w", name, b.Key, err)
		}
		out = append(out, p)
		if b.UpdatedAt.After(latest) {
			latest = b.UpdatedAt
		}
	}
	return out, latest, nil
}

// schemaOf is the dataset's columns: in first-seen order, each the kind every
// part holding a value in it agrees on, else string.
func schemaOf(parts []partInfo) []worker.Column {
	var order []string
	votes := map[string]map[string]bool{}
	for _, p := range parts {
		for _, c := range p.Columns {
			if _, ok := votes[c.Name]; !ok {
				votes[c.Name] = map[string]bool{}
				order = append(order, c.Name)
			}
			if c.Kind != "" {
				votes[c.Name][c.Kind] = true
			}
		}
	}
	out := make([]worker.Column, 0, len(order))
	for _, name := range order {
		kind := table.Number
		switch len(votes[name]) {
		case 0:
		case 1:
			for k := range votes[name] {
				kind = k
			}
		default:
			kind = table.String
		}
		out = append(out, worker.Column{Name: name, Type: kind})
	}
	return out
}

func (s *datasetStore) info(ctx context.Context, vhost, name string) (worker.DatasetInfo, error) {
	if err := checkDatasetName(vhost, name); err != nil {
		return worker.DatasetInfo{}, err
	}
	gen, err := s.head(ctx, vhost, name)
	if err != nil {
		return worker.DatasetInfo{}, err
	}
	if gen == "" {
		return worker.DatasetInfo{}, notFoundDataset(vhost, name)
	}
	return s.infoOf(ctx, vhost, name, gen, time.Time{})
}

func (s *datasetStore) infoOf(ctx context.Context, vhost, name, gen string, since time.Time) (worker.DatasetInfo, error) {
	parts, latest, err := s.parts(ctx, vhost, name, gen)
	if err != nil {
		return worker.DatasetInfo{}, err
	}
	if latest.Before(since) {
		latest = since
	}
	return worker.DatasetInfo{Name: name, Rows: rowsOf(parts), Columns: schemaOf(parts),
		UpdatedAt: latest.UTC().Truncate(time.Second), Engine: worker.EngineBuiltin}, nil
}

func (s *datasetStore) list(ctx context.Context, vhost string) ([]worker.DatasetInfo, error) {
	if err := checkName("vhost", vhost); err != nil {
		return nil, err
	}
	heads, err := s.blobs.List(ctx, vhost, "dataset-heads/", true)
	if err != nil {
		return nil, err
	}
	out := []worker.DatasetInfo{}
	for _, b := range heads {
		name := strings.TrimSuffix(strings.TrimPrefix(b.Key, "dataset-heads/"), "/head")
		var h datasetHead
		if !validName.MatchString(name) || json.Unmarshal(b.Data, &h) != nil || h.Gen == "" {
			continue
		}
		info, err := s.infoOf(ctx, vhost, name, h.Gen, b.UpdatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

// has reports whether the dataset exists.
func (s *datasetStore) has(ctx context.Context, vhost, name string) (bool, error) {
	if err := checkDatasetName(vhost, name); err != nil {
		return false, err
	}
	gen, err := s.head(ctx, vhost, name)
	return gen != "", err
}

// load reads the dataset into one table, at most limit rows (0 for all),
// with the dataset's column kinds.
func (s *datasetStore) load(ctx context.Context, vhost, name string, limit int) (*table.Table, error) {
	if err := checkDatasetName(vhost, name); err != nil {
		return nil, err
	}
	gen, err := s.head(ctx, vhost, name)
	if err != nil {
		return nil, err
	}
	if gen == "" {
		return nil, notFoundDataset(vhost, name)
	}
	parts, _, err := s.parts(ctx, vhost, name, gen)
	if err != nil {
		return nil, err
	}
	total := rowsOf(parts)
	if limit > 0 {
		total = min(total, limit)
	}
	out := &table.Table{Rows: total}
	cols := map[string]*table.Column{}
	for _, c := range schemaOf(parts) {
		col := emptyColumn(c.Name, c.Type, total)
		cols[c.Name] = col
		out.Columns = append(out.Columns, col)
	}
	at := 0
	for _, p := range parts {
		if at >= total {
			break
		}
		n, err := s.readPart(ctx, vhost, genPrefix(name, gen)+"data/"+p.ID, cols, at, total-at)
		if err != nil {
			return nil, fmt.Errorf("dataset %q part %s is unreadable: %w", name, p.ID, err)
		}
		at += n
	}
	return out, nil
}

// readPart reads a part's values into cols from row at on, at most room
// rows, and returns how many it read.
func (s *datasetStore) readPart(ctx context.Context, vhost, key string, cols map[string]*table.Column, at, room int) (int, error) {
	raw, err := s.blobs.Get(ctx, vhost, key)
	if err != nil {
		return 0, err
	}
	if raw, err = gunzipped(raw); err != nil {
		return 0, err
	}
	var pf partFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		return 0, err
	}
	n := min(pf.Rows, room)
	for _, pc := range pf.Columns {
		col := cols[pc.Name]
		for i := 0; i < n && i < len(pc.Values); i++ {
			setValue(col, at+i, pc.Values[i])
		}
	}
	return n, nil
}

func emptyColumn(name, kind string, n int) *table.Column {
	c := &table.Column{Name: name, Kind: kind}
	if kind == table.String {
		c.Str, c.Null = make([]string, n), make([]bool, n)
		for i := range c.Null {
			c.Null[i] = true
		}
		return c
	}
	c.Num = make([]float64, n)
	for i := range c.Num {
		c.Num[i] = math.NaN()
	}
	return c
}

// setValue stores a part's value in a column of the dataset's kind, which
// may be string where the part's was not.
func setValue(c *table.Column, i int, v any) {
	if v == nil {
		return
	}
	switch c.Kind {
	case table.String:
		if s, ok := v.(string); ok {
			c.Str[i] = s
		} else {
			c.Str[i] = table.ToText(v)
		}
		c.Null[i] = false
	case table.Bool:
		if b, ok := v.(bool); ok {
			c.Num[i] = 0
			if b {
				c.Num[i] = 1
			}
		}
	default:
		if f, ok := v.(float64); ok {
			c.Num[i] = f
		}
	}
}

// -- models ------------------------------------------------------------------

// The kinds of a version's artifact.
const (
	modelFile       = "model.json.gz"
	recommenderFile = "model.rec"
)

// maxVersionClaims bounds how many numbers a save tries when other servers
// keep taking them.
const maxVersionClaims = 50

// modelStore keeps each model's versions, b1, b2, ….
type modelStore struct {
	blobs Blobs
}

// versionMeta is a version as stored: what the worker API answers, and which
// artifact holds it.
type versionMeta struct {
	Version  worker.Version `json:"version"`
	File     string         `json:"file"`
	Artifact string         `json:"artifact"`
}

func checkModel(vhost, name string) error {
	if err := checkName("vhost", vhost); err != nil {
		return err
	}
	return checkName("model", name)
}

func metaKey(name, version string) string { return "models/" + name + "/" + version + "/meta" }

// highest is the highest version number the model was ever given.
func (s *modelStore) highest(ctx context.Context, vhost, name string) (int, error) {
	top := 0
	if raw, err := s.blobs.Get(ctx, vhost, "counters/"+name); err == nil {
		top, _ = strconv.Atoi(string(raw))
	} else if !errors.Is(err, ErrBlobNotFound) {
		return 0, err
	}
	list, err := s.blobs.List(ctx, vhost, "models/"+name+"/", false)
	if err != nil {
		return 0, err
	}
	for _, b := range list {
		if n, ok := worker.ParseBuiltinVersion(path.Base(path.Dir(b.Key))); ok {
			top = max(top, n)
		}
	}
	return top, nil
}

// save writes a new version, numbered after the highest the model was ever
// given, and returns it with its version filled in.
func (s *modelStore) save(ctx context.Context, vhost, name string, v worker.Version, file string, data []byte) (worker.Version, error) {
	if err := checkModel(vhost, name); err != nil {
		return v, err
	}
	// Under a prefix of its own, so a save that fails can take it back.
	artifactDir := "artifacts/" + name + "/" + newID() + "/"
	artifact := artifactDir + "artifact"
	if err := s.blobs.Put(ctx, vhost, artifact, data); err != nil {
		return v, err
	}
	for range maxVersionClaims {
		top, err := s.highest(ctx, vhost, name)
		if err != nil {
			break
		}
		v.Model, v.Version, v.Engine = name, worker.BuiltinVersion(top+1), worker.EngineBuiltin
		meta, err := json.Marshal(versionMeta{Version: v, File: file, Artifact: artifact})
		if err != nil {
			return v, fmt.Errorf("encoding the version: %w", err)
		}
		err = s.blobs.Create(ctx, vhost, metaKey(name, v.Version), meta)
		if errors.Is(err, ErrBlobExists) {
			continue
		}
		if err != nil {
			break
		}
		_ = s.blobs.Put(ctx, vhost, "counters/"+name, []byte(strconv.Itoa(top+1)))
		return v, nil
	}
	_, _ = s.blobs.Delete(ctx, vhost, artifactDir)
	return v, fmt.Errorf("could not give model %q a version number", name)
}

func (s *modelStore) versions(ctx context.Context, vhost, name string) ([]worker.Version, error) {
	if err := checkModel(vhost, name); err != nil {
		return nil, err
	}
	list, err := s.blobs.List(ctx, vhost, "models/"+name+"/", true)
	if err != nil {
		return nil, err
	}
	var out []worker.Version
	for _, b := range list {
		var m versionMeta
		if path.Base(b.Key) != "meta" || json.Unmarshal(b.Data, &m) != nil {
			continue
		}
		out = append(out, m.Version)
	}
	if len(out) == 0 {
		return nil, notFound("Model %q has no versions in vhost %q.", name, vhost)
	}
	slices.SortFunc(out, func(a, b worker.Version) int {
		na, _ := worker.ParseBuiltinVersion(a.Version)
		nb, _ := worker.ParseBuiltinVersion(b.Version)
		return nb - na
	})
	return out, nil
}

// file reads a version and its artifact, which must be of the kind file.
func (s *modelStore) file(ctx context.Context, vhost, name, version, file string) (worker.Version, []byte, error) {
	if err := checkModel(vhost, name); err != nil {
		return worker.Version{}, nil, err
	}
	if !worker.IsBuiltinVersion(version) {
		return worker.Version{}, nil, notFound("Model %q has no version %q.", name, version)
	}
	raw, err := s.blobs.Get(ctx, vhost, metaKey(name, version))
	if errors.Is(err, ErrBlobNotFound) {
		return worker.Version{}, nil, notFound("Model %q has no version %q.", name, version)
	}
	if err != nil {
		return worker.Version{}, nil, err
	}
	var m versionMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return worker.Version{}, nil, fmt.Errorf("version %s of model %q is unreadable: %w", version, name, err)
	}
	if m.File != file {
		return m.Version, nil, notFound("Version %s of model %q has no %s.", version, name, file)
	}
	data, err := s.blobs.Get(ctx, vhost, m.Artifact)
	if errors.Is(err, ErrBlobNotFound) {
		return m.Version, nil, notFound("Version %s of model %q has lost its %s.", version, name, file)
	}
	return m.Version, data, err
}

func (s *modelStore) delete(ctx context.Context, vhost, name string) error {
	if err := checkModel(vhost, name); err != nil {
		return err
	}
	n, err := s.blobs.Delete(ctx, vhost, "models/"+name+"/")
	if err != nil {
		return err
	}
	if _, err := s.blobs.Delete(ctx, vhost, "artifacts/"+name+"/"); err != nil {
		return err
	}
	if n == 0 {
		return notFound("Model %q does not exist in vhost %q.", name, vhost)
	}
	return nil
}
