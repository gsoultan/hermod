package builtin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/builtin/table"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// The on-disk layout, beside the worker's own:
//
//	DIR/datasets/{vhost}/{name}/index.json         the parts and their columns
//	                             part-000001.json  one table per append
//	DIR/models/{vhost}/{name}/{version}/meta.json  the version (worker.Version)
//	                                    model.json.gz  a column model (learn)
//	                                    model.rec      a recommender (pkg/ml/recommend)
//
// Each store reaches its files through an os.Root, so no name can lead
// outside its directory, and names are checked besides. Every write goes to
// a temporary name in the same directory first and is renamed into place,
// so a crash never leaves half a part, index or version.

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

// -- datasets ----------------------------------------------------------------

// datasetStore keeps datasets as parts, one per append. One lock keeps
// appends, replaces and deletes from interleaving; reads take it too, so they
// never see a dataset halfway through a swap.
type datasetStore struct {
	root *os.Root
	mu   sync.RWMutex
}

// datasetIndex lists a dataset's parts, so a listing reads one small file.
type datasetIndex struct {
	Parts []partInfo `json:"parts"`
}

type partInfo struct {
	File    string         `json:"file"`
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

// dir is a dataset's directory in the store's root.
func (s *datasetStore) dir(vhost, name string) (string, error) {
	if err := checkName("vhost", vhost); err != nil {
		return "", err
	}
	if err := checkName("dataset", name); err != nil {
		return "", err
	}
	return path.Join(vhost, name), nil
}

func notFoundDataset(vhost, name string) error {
	return notFound("Dataset %q does not exist in vhost %q.", name, vhost)
}

func (s *datasetStore) readIndex(dir string) (datasetIndex, error) {
	var idx datasetIndex
	raw, err := s.root.ReadFile(path.Join(dir, "index.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return idx, nil
	}
	if err != nil {
		return idx, err
	}
	return idx, json.Unmarshal(raw, &idx)
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

// writePart writes a table as the next part of the dataset in dir and
// returns the index that lists it.
func (s *datasetStore) writePart(dir string, idx datasetIndex, t *table.Table) (datasetIndex, error) {
	pf, info := encodePart(t)
	raw, err := json.Marshal(pf)
	if err != nil {
		return idx, err
	}
	info.File = fmt.Sprintf("part-%06d.json", len(idx.Parts)+1)
	if err := writeFileAtomic(s.root, path.Join(dir, info.File), raw); err != nil {
		return idx, err
	}
	idx.Parts = append(idx.Parts, info)
	return idx, nil
}

func (s *datasetStore) writeIndex(dir string, idx datasetIndex) error {
	raw, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.root, path.Join(dir, "index.json"), raw)
}

// append adds t as a new part, or replaces the dataset with it, and returns
// how many rows the dataset holds now.
func (s *datasetStore) append(vhost, name string, t *table.Table, replace bool) (int, error) {
	dir, err := s.dir(vhost, name)
	if err != nil {
		return 0, err
	}
	if replace {
		return t.Rows, s.replace(vhost, name, t)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.root.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	idx, err := s.readIndex(dir)
	if err != nil {
		return 0, err
	}
	if t.Rows > 0 {
		if idx, err = s.writePart(dir, idx, t); err != nil {
			return 0, err
		}
	}
	if err := s.writeIndex(dir, idx); err != nil {
		return 0, err
	}
	return idx.rows(), nil
}

func (idx datasetIndex) rows() int {
	n := 0
	for _, p := range idx.Parts {
		n += p.Rows
	}
	return n
}

// replace swaps the dataset for one holding only t. The new dataset is
// staged in a sibling directory and renamed into place.
func (s *datasetStore) replace(vhost, name string, t *table.Table) error {
	dir, err := s.dir(vhost, name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.root.MkdirAll(vhost, 0o700); err != nil {
		return err
	}
	staging := path.Join(vhost, tempName(".staging-"))
	if err := s.root.Mkdir(staging, 0o700); err != nil {
		return err
	}
	defer func() { _ = s.root.RemoveAll(staging) }()
	idx := datasetIndex{}
	if t.Rows > 0 || len(t.Columns) > 0 {
		if idx, err = s.writePart(staging, idx, t); err != nil {
			return err
		}
	}
	if err := s.writeIndex(staging, idx); err != nil {
		return err
	}
	if exists(s.root, dir) {
		if err := removeDir(s.root, dir); err != nil {
			return err
		}
	}
	return s.root.Rename(staging, dir)
}

func (s *datasetStore) delete(vhost, name string) error {
	dir, err := s.dir(vhost, name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !exists(s.root, dir) {
		return notFoundDataset(vhost, name)
	}
	return removeDir(s.root, dir)
}

// schema is the dataset's columns: in first-seen order, each the kind every
// part holding a value in it agrees on, else string.
func (idx datasetIndex) schema() []worker.Column {
	var order []string
	votes := map[string]map[string]bool{}
	for _, p := range idx.Parts {
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

func (s *datasetStore) infoLocked(dir, name string) (worker.DatasetInfo, error) {
	idx, err := s.readIndex(dir)
	if err != nil {
		return worker.DatasetInfo{}, err
	}
	updated := time.Time{}
	if st, err := s.root.Stat(path.Join(dir, "index.json")); err == nil {
		updated = st.ModTime()
	} else if st, err := s.root.Stat(dir); err == nil {
		updated = st.ModTime()
	}
	return worker.DatasetInfo{Name: name, Rows: idx.rows(), Columns: idx.schema(), UpdatedAt: updated.UTC().Truncate(time.Second)}, nil
}

func (s *datasetStore) info(vhost, name string) (worker.DatasetInfo, error) {
	dir, err := s.dir(vhost, name)
	if err != nil {
		return worker.DatasetInfo{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !exists(s.root, dir) {
		return worker.DatasetInfo{}, notFoundDataset(vhost, name)
	}
	return s.infoLocked(dir, name)
}

func (s *datasetStore) list(vhost string) ([]worker.DatasetInfo, error) {
	if err := checkName("vhost", vhost); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := fs.ReadDir(s.root.FS(), vhost)
	if errors.Is(err, fs.ErrNotExist) {
		return []worker.DatasetInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []worker.DatasetInfo{}
	for _, e := range entries {
		if !e.IsDir() || !validName.MatchString(e.Name()) {
			continue
		}
		info, err := s.infoLocked(path.Join(vhost, e.Name()), e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

// load reads the dataset into one table, at most limit rows (0 for all),
// with the dataset's column kinds.
func (s *datasetStore) load(vhost, name string, limit int) (*table.Table, error) {
	dir, err := s.dir(vhost, name)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !exists(s.root, dir) {
		return nil, notFoundDataset(vhost, name)
	}
	idx, err := s.readIndex(dir)
	if err != nil {
		return nil, err
	}
	total := idx.rows()
	if limit > 0 {
		total = min(total, limit)
	}
	out := &table.Table{Rows: total}
	cols := map[string]*table.Column{}
	for _, c := range idx.schema() {
		col := emptyColumn(c.Name, c.Type, total)
		cols[c.Name] = col
		out.Columns = append(out.Columns, col)
	}
	at := 0
	for _, p := range idx.Parts {
		if at >= total {
			break
		}
		n, err := s.readPart(path.Join(dir, p.File), cols, at, total-at)
		if err != nil {
			return nil, fmt.Errorf("dataset %q part %s is unreadable: %w", name, p.File, err)
		}
		at += n
	}
	return out, nil
}

// readPart reads a part's values into cols from row at on, at most room
// rows, and returns how many it read.
func (s *datasetStore) readPart(name string, cols map[string]*table.Column, at, room int) (int, error) {
	raw, err := s.root.ReadFile(name)
	if err != nil {
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

// The files of a version.
const (
	metaFile        = "meta.json"
	modelFile       = "model.json.gz"
	recommenderFile = "model.rec"
)

// modelStore keeps each model's versions, numbered from 1.
type modelStore struct {
	root *os.Root
	mu   sync.Mutex
}

func (s *modelStore) dir(vhost, name string) (string, error) {
	if err := checkName("vhost", vhost); err != nil {
		return "", err
	}
	if err := checkName("model", name); err != nil {
		return "", err
	}
	return path.Join(vhost, name), nil
}

// numbers are the committed versions, newest first.
func (s *modelStore) numbers(dir string) []int {
	entries, _ := fs.ReadDir(s.root.FS(), dir)
	var out []int
	for _, e := range entries {
		if n, err := strconv.Atoi(e.Name()); err == nil && e.IsDir() && n > 0 && strconv.Itoa(n) == e.Name() {
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(a, b int) int { return b - a })
	return out
}

// save writes a new version, numbered after the newest, and returns its
// meta with the version filled in.
func (s *modelStore) save(vhost, name string, v worker.Version, file string, data []byte) (worker.Version, error) {
	dir, err := s.dir(vhost, name)
	if err != nil {
		return v, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.root.MkdirAll(dir, 0o700); err != nil {
		return v, err
	}
	next := 1
	if ns := s.numbers(dir); len(ns) > 0 {
		next = ns[0] + 1
	}
	v.Model, v.Version = name, strconv.Itoa(next)
	meta, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return v, fmt.Errorf("encoding the version: %w", err)
	}
	tmp := path.Join(dir, tempName(".tmp-"))
	if err := s.root.Mkdir(tmp, 0o700); err != nil {
		return v, err
	}
	defer func() { _ = s.root.RemoveAll(tmp) }()
	if err := s.root.WriteFile(path.Join(tmp, file), data, 0o600); err != nil {
		return v, err
	}
	if err := s.root.WriteFile(path.Join(tmp, metaFile), meta, 0o600); err != nil {
		return v, err
	}
	return v, s.root.Rename(tmp, path.Join(dir, v.Version))
}

func (s *modelStore) versions(vhost, name string) ([]worker.Version, error) {
	dir, err := s.dir(vhost, name)
	if err != nil {
		return nil, err
	}
	ns := s.numbers(dir)
	if len(ns) == 0 {
		return nil, notFound("Model %q has no versions in vhost %q.", name, vhost)
	}
	out := make([]worker.Version, 0, len(ns))
	for _, n := range ns {
		v, err := s.meta(dir, name, strconv.Itoa(n))
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *modelStore) meta(dir, name, version string) (worker.Version, error) {
	var v worker.Version
	raw, err := s.root.ReadFile(path.Join(dir, version, metaFile))
	if err != nil {
		return v, notFound("Model %q has no version %q.", name, version)
	}
	return v, json.Unmarshal(raw, &v)
}

// file reads one of a version's files: its meta, and the file's bytes.
func (s *modelStore) file(vhost, name, version, file string) (worker.Version, []byte, error) {
	dir, err := s.dir(vhost, name)
	if err != nil {
		return worker.Version{}, nil, err
	}
	if n, err := strconv.Atoi(version); err != nil || n <= 0 || strconv.Itoa(n) != version {
		return worker.Version{}, nil, notFound("Model %q has no version %q.", name, version)
	}
	v, err := s.meta(dir, name, version)
	if err != nil {
		return v, nil, err
	}
	raw, err := s.root.ReadFile(path.Join(dir, version, file))
	if err != nil {
		return v, nil, notFound("Version %s of model %q has no %s.", version, name, file)
	}
	return v, raw, nil
}

func (s *modelStore) delete(vhost, name string) error {
	dir, err := s.dir(vhost, name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !exists(s.root, dir) {
		return notFound("Model %q does not exist in vhost %q.", name, vhost)
	}
	return removeDir(s.root, dir)
}
