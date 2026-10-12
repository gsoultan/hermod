package builtin

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"sync"

	"github.com/gsoultan/hermod/pkg/ml/recommend"
)

// recModel is a fitted recommender, as the format 1 artifact holds it.
type recModel struct {
	algorithm     string
	users, items  []string
	seen          [][]uint32 // each person's items, ascending
	popular       []uint32   // most popular first
	popularScores []float32
	factors       int
	userF, itemF  []float32 // [users x factors], [items x factors]
	neighbors     int
	nbrItems      []uint32 // [items x neighbors], emptyNeighbor for an unused slot
	nbrScores     []float32
}

// recFitter fits recommenders on some of the interactions.
type recFitter struct {
	in      *interactions
	seed    uint64
	workers int
}

// fit fits algorithm on the interactions rows (nil for all of them).
func (f recFitter) fit(ctx context.Context, algorithm string, rows []int) (*recModel, error) {
	in := f.in
	if rows == nil {
		rows = make([]int, len(in.user))
		for i := range rows {
			rows[i] = i
		}
	}
	byUser, byItem := make([][]entry, len(in.users)), make([][]entry, len(in.items))
	for _, r := range rows {
		byUser[in.user[r]] = append(byUser[in.user[r]], entry{in.item[r], in.strength[r]})
		byItem[in.item[r]] = append(byItem[in.item[r]], entry{in.user[r], in.strength[r]})
	}
	for _, es := range byUser {
		slices.SortFunc(es, func(a, b entry) int { return a.at - b.at })
	}
	m := &recModel{algorithm: algorithm, users: in.users, items: in.items, seen: make([][]uint32, len(in.users))}
	for u, es := range byUser {
		for _, e := range es {
			m.seen[u] = append(m.seen[u], uint32(e.at))
		}
	}
	m.popular, m.popularScores = popularOf(byItem)
	var err error
	if algorithm == algorithmALS {
		err = f.fitALS(ctx, m, byUser, byItem)
	} else {
		err = f.fitNeighbors(ctx, m, byUser, byItem)
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// entry is one cell of the interaction matrix: the column it is at, and
// its strength.
type entry struct {
	at       int
	strength float64
}

// popularOf is the popularItems items most people have, best first (ties
// by index), each scored by its share of the most popular one's count.
func popularOf(byItem [][]entry) ([]uint32, []float32) {
	var order []int
	for i, es := range byItem {
		if len(es) > 0 {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return len(byItem[order[a]]) > len(byItem[order[b]]) })
	order = order[:min(popularItems, len(order))]
	items, scores := make([]uint32, len(order)), make([]float32, len(order))
	for n, i := range order {
		items[n] = uint32(i)
		scores[n] = float32(float64(len(byItem[i])) / float64(len(byItem[order[0]])))
	}
	return items, scores
}

// parallel runs fn(i) for i in [0, n) on at most workers goroutines, and
// stops early once ctx ends.
func parallel(ctx context.Context, n, workers int, fn func(i int, scratch []float64), scratchSize int) error {
	var wg sync.WaitGroup
	next := make(chan int)
	for range max(1, min(workers, n)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scratch := make([]float64, scratchSize)
			for i := range next {
				fn(i, scratch)
			}
		}()
	}
	var err error
	for i := range n {
		if err = ctx.Err(); err != nil {
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	return err
}

// fitALS fits implicit alternating least squares (Hu, Koren and Volinsky):
// every interaction is a "liked" with a confidence of 1 + alsAlpha *
// log1p(strength), and each side's factors are solved against the other's.
func (f recFitter) fitALS(ctx context.Context, m *recModel, byUser, byItem [][]entry) error {
	k := max(1, min(alsFactors, len(m.users), len(m.items)))
	rng := newRand(f.seed, 43)
	users, items := make([]float64, len(m.users)*k), make([]float64, len(m.items)*k)
	for i := range users {
		users[i] = rng.NormFloat64() * 0.01
	}
	for i := range items {
		items[i] = rng.NormFloat64() * 0.01
	}
	for range alsIterations {
		if err := f.alsStep(ctx, byUser, items, users, k); err != nil {
			return err
		}
		if err := f.alsStep(ctx, byItem, users, items, k); err != nil {
			return err
		}
	}
	m.factors = k
	m.userF, m.itemF = toFloat32(users), toFloat32(items)
	return nil
}

func toFloat32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x)
	}
	return out
}

// alsStep solves each row's factors (into out) against the fixed side's:
// (YᵀY + reg I + Σ (c-1) y yᵀ) x = Σ c y over the row's entries.
func (f recFitter) alsStep(ctx context.Context, rows [][]entry, fixed, out []float64, k int) error {
	gram := make([]float64, k*k)
	for i := 0; i < len(fixed); i += k {
		y := fixed[i : i+k]
		for a := range k {
			for b := range k {
				gram[a*k+b] += y[a] * y[b]
			}
		}
	}
	for a := range k {
		gram[a*k+a] += alsRegularization
	}
	return parallel(ctx, len(rows), f.workers, func(r int, scratch []float64) {
		x := out[r*k : (r+1)*k]
		if len(rows[r]) == 0 {
			clear(x)
			return
		}
		a, b := scratch[:k*k], scratch[k*k:]
		copy(a, gram)
		clear(b)
		for _, e := range rows[r] {
			c := 1 + alsAlpha*math.Log1p(e.strength)
			y := fixed[e.at*k : (e.at+1)*k]
			for p := range k {
				b[p] += c * y[p]
				for q := range k {
					a[p*k+q] += (c - 1) * y[p] * y[q]
				}
			}
		}
		choleskySolve(a, b, k)
		copy(x, b)
	}, k*k+k)
}

// choleskySolve solves a x = b for a symmetric positive definite k x k a,
// in place: a becomes its Cholesky factor and b the solution.
func choleskySolve(a, b []float64, k int) {
	b = b[:k]
	for j := range k {
		s := a[j*k+j]
		for p := range j {
			s -= a[j*k+p] * a[j*k+p]
		}
		d := math.Sqrt(math.Max(s, 1e-12))
		a[j*k+j] = d
		for i := j + 1; i < k; i++ {
			s := a[i*k+j]
			for p := range j {
				s -= a[i*k+p] * a[j*k+p]
			}
			a[i*k+j] = s / d
		}
	}
	for i, v := range b {
		for p := range i {
			v -= a[i*k+p] * b[p] //nolint:gosec // G602: p < i < len(b).
		}
		b[i] = v / a[i*k+i]
	}
	for i := k - 1; i >= 0; i-- {
		v := b[i]
		for p := i + 1; p < k; p++ {
			v -= a[p*k+i] * b[p]
		}
		b[i] = v / a[i*k+i]
	}
}

// fitNeighbors keeps each item's similarNeighbors most similar items by
// cosine similarity over who has them, ranked in float32 as stored (ties by
// index).
func (f recFitter) fitNeighbors(ctx context.Context, m *recModel, byUser, byItem [][]entry) error {
	n := len(m.items)
	keep := max(1, min(similarNeighbors, n-1))
	m.neighbors = keep
	m.nbrItems, m.nbrScores = make([]uint32, n*keep), make([]float32, n*keep)
	for i := range m.nbrItems {
		m.nbrItems[i] = emptyNeighbor
	}
	return parallel(ctx, n, f.workers, func(i int, co []float64) {
		sims := similarTo(i, byUser, byItem, co)
		for s, c := range sims[:min(keep, len(sims))] {
			m.nbrItems[i*keep+s], m.nbrScores[i*keep+s] = uint32(c.item), c.score
		}
	}, n)
}

// similarity is how alike another item is to one.
type similarity struct {
	item  int
	score float32
}

// similarTo is every item someone who has item i also has, by cosine
// similarity, most similar first (ties by index). co is n zeros of scratch,
// left zero.
func similarTo(i int, byUser, byItem [][]entry, co []float64) []similarity {
	var touched []int
	for _, ue := range byItem[i] {
		for _, ie := range byUser[ue.at] {
			if ie.at != i && co[ie.at] == 0 {
				touched = append(touched, ie.at)
			}
			co[ie.at]++
		}
	}
	sims := make([]similarity, 0, len(touched))
	for _, j := range touched {
		if s := float32(co[j] / math.Sqrt(float64(len(byItem[i]))*float64(len(byItem[j])))); s > 0 {
			sims = append(sims, similarity{j, s})
		}
		co[j] = 0
	}
	co[i] = 0
	slices.SortFunc(sims, func(a, b similarity) int {
		switch {
		case a.score > b.score:
			return -1
		case a.score < b.score:
			return 1
		}
		return a.item - b.item
	})
	return sims
}

// encode writes the format 1 artifact (recommend.py encode; pkg/ml/recommend
// Parse), little-endian throughout.
func (m *recModel) encode() ([]byte, error) {
	seen := 0
	for _, s := range m.seen {
		seen += len(s)
	}
	var head bytes.Buffer
	enc := json.NewEncoder(&head)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"algorithm": m.algorithm, "users": m.users, "items": m.items, "factors": m.factors,
		"neighbors": m.neighbors, "seen": seen, "popular": len(m.popular)}); err != nil {
		return nil, err
	}
	h := bytes.TrimRight(head.Bytes(), "\n")
	var buf bytes.Buffer
	buf.WriteString(recommenderMagic)
	put := func(v any) { _ = binary.Write(&buf, binary.LittleEndian, v) }
	put(uint32(recommenderFormat))
	put(uint32(len(h)))
	buf.Write(h)
	if m.factors > 0 {
		put(m.userF)
		put(m.itemF)
	}
	if m.neighbors > 0 {
		put(m.nbrItems)
		put(m.nbrScores)
	}
	offsets := make([]uint32, 0, len(m.seen)+1)
	var items []uint32
	offsets = append(offsets, 0)
	for _, s := range m.seen {
		items = append(items, s...)
		offsets = append(offsets, uint32(len(items)))
	}
	put(offsets)
	put(items)
	put(m.popular)
	put(m.popularScores)
	return buf.Bytes(), nil
}

// choose is auto: both algorithms fitted on part of the training rows and
// scored by NDCG@10 on the rest; the simpler one wins a tie.
func (f recFitter) choose(ctx context.Context, trainRows []int, req *recommendRequest) (string, map[string]float64, error) {
	fitRows, validRows := f.in.holdout(trainRows, req.testSize, req.seed+1)
	if len(validRows) == 0 {
		return algorithmSimilar, nil, nil
	}
	truth := f.in.truths(validRows, req.seed)
	scores := map[string]float64{}
	for _, alg := range []string{algorithmALS, algorithmSimilar} {
		m, err := f.fit(ctx, alg, fitRows)
		if err != nil {
			return "", nil, err
		}
		metrics, _ := evaluate(m, f.in, truth)
		scores[alg] = metrics[ndcgKey]
	}
	if scores[algorithmALS] > scores[algorithmSimilar] {
		return algorithmALS, scores, nil
	}
	return algorithmSimilar, scores, nil
}

// The metric keys, at recK.
const (
	hitKey       = "hit_rate@10"
	precisionKey = "precision@10"
	recallKey    = "recall@10"
	ndcgKey      = "ndcg@10"
)

// served is the model as Hermod serves it: its artifact, parsed by
// pkg/ml/recommend, so the metrics measure what callers get.
func (m *recModel) served() *recommend.Model {
	raw, err := m.encode()
	if err != nil {
		return nil
	}
	p, err := recommend.Parse(raw)
	if err != nil {
		return nil
	}
	return p
}

// evaluate is the model's metrics, and the popular-items baseline's, on what
// the test people really picked.
func evaluate(m *recModel, in *interactions, truth map[int][]int) (map[string]float64, map[string]float64) {
	users := make([]int, 0, len(truth))
	for u := range truth {
		users = append(users, u)
	}
	slices.Sort(users)
	served := m.served()
	itemIndex := map[string]int{}
	for i, id := range in.items {
		itemIndex[id] = i
	}
	var lists, base [][]int
	for _, u := range users {
		var recs []int
		if served != nil {
			got, _ := served.ForUser(in.users[u], recK, true)
			for _, it := range got {
				recs = append(recs, itemIndex[it.Item])
			}
		}
		lists = append(lists, recs)
		var pop []int
		for _, p := range m.popular {
			if len(pop) == recK {
				break
			}
			if _, has := slices.BinarySearch(m.seen[u], p); !has {
				pop = append(pop, int(p))
			}
		}
		base = append(base, pop)
	}
	truths := make([][]int, len(users))
	for i, u := range users {
		truths[i] = truth[u]
	}
	return rankMetrics(lists, truths, len(in.items)), rankMetrics(base, truths, len(in.items))
}

// rankMetrics are hit rate, precision, recall and NDCG at recK, and the
// share of the catalogue recommended to anyone.
func rankMetrics(lists, truths [][]int, items int) map[string]float64 {
	var hits, precision, recall, ndcg float64
	shown := map[int]bool{}
	for n, recs := range lists {
		truth := truths[n]
		found, dcg := 0.0, 0.0
		for rank, r := range recs[:min(recK, len(recs))] {
			shown[r] = true
			if slices.Contains(truth, r) {
				found++
				dcg += 1 / math.Log2(float64(rank+2))
			}
		}
		for _, r := range recs[min(recK, len(recs)):] {
			shown[r] = true
		}
		ideal := 0.0
		for rank := range min(len(truth), recK) {
			ideal += 1 / math.Log2(float64(rank+2))
		}
		if found > 0 {
			hits++
		}
		precision += found / recK
		recall += found / float64(len(truth))
		ndcg += dcg / ideal
	}
	n := float64(max(1, len(lists)))
	return map[string]float64{hitKey: hits / n, precisionKey: precision / n, recallKey: recall / n, ndcgKey: ndcg / n,
		"coverage": float64(len(shown)) / float64(items)}
}

// recSample is one test person: what the model recommended, and what they
// really picked.
type recSample struct {
	User        string   `json:"user"`
	Recommended []string `json:"recommended"`
	Picked      []string `json:"picked"`
}

// sampleRecommendations are the first few test people, by index.
func sampleRecommendations(m *recModel, in *interactions, truth map[int][]int) []recSample {
	users := make([]int, 0, len(truth))
	for u := range truth {
		users = append(users, u)
	}
	slices.Sort(users)
	served := m.served()
	out := []recSample{}
	for _, u := range users[:min(recommendSamples, len(users))] {
		s := recSample{User: in.users[u], Recommended: []string{}}
		if served != nil {
			got, _ := served.ForUser(in.users[u], recK, true)
			for _, it := range got {
				s.Recommended = append(s.Recommended, it.Item)
			}
		}
		for _, i := range truth[u] {
			s.Picked = append(s.Picked, in.items[i])
		}
		slices.Sort(s.Picked)
		out = append(out, s)
	}
	return out
}
