package onnxscore

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

// Tree ensembles, as ONNX Runtime evaluates TreeEnsembleClassifier and
// TreeEnsembleRegressor (onnxruntime/core/providers/cpu/ml/tree_ensemble_*):
// each tree is walked from its root, the first node listed for it, to a
// leaf, whose weights are added to the scores in float32, tree by tree in
// the order the trees are listed.

type treeMode uint8

const (
	modeLeaf treeMode = iota
	modeLEQ
	modeLT
	modeGTE
	modeGT
	modeEQ
	modeNEQ
)

var treeModes = map[string]treeMode{
	"LEAF": modeLeaf, "BRANCH_LEQ": modeLEQ, "BRANCH_LT": modeLT, "BRANCH_GTE": modeGTE,
	"BRANCH_GT": modeGT, "BRANCH_EQ": modeEQ, "BRANCH_NEQ": modeNEQ,
}

type leafWeight struct {
	target int
	weight float32
}

type treeNode struct {
	mode        treeMode
	feature     int
	value       float32
	missingTrue bool
	yes, no     int
	weights     []leafWeight
}

type ensemble struct {
	nodes      []treeNode
	roots      []int
	maxFeature int
	targets    int
}

// ensembleAttrs are a tree ensemble's attributes: the nodes, and the leaf
// weights under a prefix ("class" or "target").
type ensembleAttrs struct {
	prefix                              string
	treeIDs, nodeIDs, features, yes, no []int64
	missing                             []int64
	values                              []float32
	modes                               []string
	wTrees, wNodes, wIDs                []int64
	weights                             []float32
}

// nodeKey names one node of one tree.
type nodeKey struct{ tree, node int64 }

// readEnsemble reads the nodes_* attributes and the leaf weights under
// prefix ("class" or "target"), for an ensemble with targets outputs. It
// also returns the weights and the target each one is for.
func readEnsemble(c *nodeCtx, prefix string, targets int) (ensemble, []int64, []float32, error) {
	e := ensemble{targets: targets, maxFeature: -1}
	a, err := readEnsembleAttrs(c, prefix)
	if err != nil {
		return e, nil, nil, err
	}
	if err := a.checkLengths(); err != nil {
		return e, nil, nil, err
	}
	index, err := e.addNodes(a)
	if err != nil {
		return e, nil, nil, err
	}
	if err := e.link(a, index); err != nil {
		return e, nil, nil, err
	}
	if err := e.attachWeights(a, index); err != nil {
		return e, nil, nil, err
	}
	if err := e.checkAcyclic(); err != nil {
		return e, nil, nil, err
	}
	return e, a.wIDs, a.weights, nil
}

func readEnsembleAttrs(c *nodeCtx, prefix string) (ensembleAttrs, error) {
	a := ensembleAttrs{prefix: prefix}
	var err error
	for _, f := range []struct {
		name string
		dst  *[]int64
	}{
		{"nodes_treeids", &a.treeIDs}, {"nodes_nodeids", &a.nodeIDs}, {"nodes_featureids", &a.features},
		{"nodes_truenodeids", &a.yes}, {"nodes_falsenodeids", &a.no}, {"nodes_missing_value_tracks_true", &a.missing},
		{prefix + "_treeids", &a.wTrees}, {prefix + "_nodeids", &a.wNodes}, {prefix + "_ids", &a.wIDs},
	} {
		if *f.dst, err = c.ints(f.name); err != nil {
			return a, err
		}
	}
	if a.values, err = c.floats("nodes_values"); err != nil {
		return a, err
	}
	if a.modes, err = c.strs("nodes_modes"); err != nil {
		return a, err
	}
	// Hit rates are statistics from training; they do not change a result.
	c.ignore("nodes_hitrates")
	if a.weights, err = c.floats(prefix + "_weights"); err != nil {
		return a, err
	}
	return a, nil
}

// checkLengths makes sure the per-node and per-weight lists line up.
func (a *ensembleAttrs) checkLengths() error {
	n := len(a.nodeIDs)
	if n == 0 {
		return errors.New("the ensemble has no nodes")
	}
	for _, l := range [][]int64{a.treeIDs, a.features, a.yes, a.no} {
		if len(l) != n {
			return errors.New("the nodes_* attributes differ in length")
		}
	}
	if len(a.values) != n || len(a.modes) != n || (len(a.missing) != 0 && len(a.missing) != n) {
		return errors.New("the nodes_* attributes differ in length")
	}
	m := len(a.weights)
	if len(a.wTrees) != m || len(a.wNodes) != m || len(a.wIDs) != m {
		return fmt.Errorf("the %s_* attributes differ in length", a.prefix)
	}
	return nil
}

// addNodes builds the nodes and the roots, and returns where each node is.
func (e *ensemble) addNodes(a ensembleAttrs) (map[nodeKey]int, error) {
	n := len(a.nodeIDs)
	index := make(map[nodeKey]int, n)
	e.nodes = make([]treeNode, n)
	seen := map[int64]bool{}
	for i := range n {
		k := nodeKey{a.treeIDs[i], a.nodeIDs[i]}
		if _, dup := index[k]; dup {
			return nil, fmt.Errorf("tree %d has node %d twice", k.tree, k.node)
		}
		index[k] = i
		if i == 0 || a.treeIDs[i] != a.treeIDs[i-1] {
			if seen[a.treeIDs[i]] {
				// ONNX Runtime takes the first node of each run of a tree
				// id as a root; a tree listed in pieces is not one tree.
				return nil, unsupported("the nodes of tree %d are not listed together", a.treeIDs[i])
			}
			seen[a.treeIDs[i]] = true
			e.roots = append(e.roots, i)
		}
		mode, ok := treeModes[a.modes[i]]
		if !ok {
			return nil, unsupported("node mode %q", a.modes[i])
		}
		e.nodes[i] = treeNode{mode: mode, value: a.values[i], missingTrue: len(a.missing) > 0 && a.missing[i] != 0}
		if mode == modeLeaf {
			continue
		}
		if a.features[i] < 0 || a.features[i] > math.MaxInt32 {
			return nil, fmt.Errorf("feature id %d", a.features[i])
		}
		e.nodes[i].feature = int(a.features[i])
		e.maxFeature = max(e.maxFeature, int(a.features[i]))
	}
	return index, nil
}

// link points each branch at its two children.
func (e *ensemble) link(a ensembleAttrs, index map[nodeKey]int) error {
	for i := range e.nodes {
		if e.nodes[i].mode == modeLeaf {
			continue
		}
		t, okT := index[nodeKey{a.treeIDs[i], a.yes[i]}]
		f, okF := index[nodeKey{a.treeIDs[i], a.no[i]}]
		if !okT || !okF {
			return fmt.Errorf("node %d of tree %d points at a node the tree does not have", a.nodeIDs[i], a.treeIDs[i])
		}
		e.nodes[i].yes, e.nodes[i].no = t, f
	}
	return nil
}

// attachWeights gives each leaf its weights.
func (e *ensemble) attachWeights(a ensembleAttrs, index map[nodeKey]int) error {
	for j := range a.weights {
		i, ok := index[nodeKey{a.wTrees[j], a.wNodes[j]}]
		if !ok || e.nodes[i].mode != modeLeaf {
			return fmt.Errorf("a weight is attached to node %d of tree %d, which is not a leaf", a.wNodes[j], a.wTrees[j])
		}
		if a.wIDs[j] < 0 || a.wIDs[j] >= int64(e.targets) {
			return fmt.Errorf("weight for %s %d of %d", a.prefix, a.wIDs[j], e.targets)
		}
		e.nodes[i].weights = append(e.nodes[i].weights, leafWeight{target: int(a.wIDs[j]), weight: a.weights[j]})
	}
	return nil
}

// checkAcyclic makes sure every walk from a root reaches a leaf.
func (e *ensemble) checkAcyclic() error {
	state := make([]uint8, len(e.nodes)) // 0 unseen, 1 on the path, 2 done
	var visit func(i int) error
	visit = func(i int) error {
		switch state[i] {
		case 1:
			return errors.New("a tree has a cycle")
		case 2:
			return nil
		}
		state[i] = 1
		if n := e.nodes[i]; n.mode != modeLeaf {
			if err := visit(n.yes); err != nil {
				return err
			}
			if err := visit(n.no); err != nil {
				return err
			}
		}
		state[i] = 2
		return nil
	}
	for _, r := range e.roots {
		if err := visit(r); err != nil {
			return err
		}
	}
	return nil
}

// score adds every tree's leaf weights for one row to scores, setting has
// for each target a weight was added to.
func (e *ensemble) score(row []float32, scores []float32, has []bool) {
	for _, i := range e.roots {
		n := &e.nodes[i]
		for n.mode != modeLeaf {
			x := row[n.feature]
			var yes bool
			switch n.mode {
			case modeLEQ:
				yes = x <= n.value
			case modeLT:
				yes = x < n.value
			case modeGTE:
				yes = x >= n.value
			case modeGT:
				yes = x > n.value
			case modeEQ:
				yes = x == n.value
			case modeNEQ:
				yes = x != n.value
			}
			if !yes && n.missingTrue && math.IsNaN(float64(x)) {
				yes = true
			}
			if yes {
				n = &e.nodes[n.yes]
			} else {
				n = &e.nodes[n.no]
			}
		}
		for _, w := range n.weights {
			scores[w.target] += w.weight
			has[w.target] = true
		}
	}
}

// input reads the feature matrix and checks it is wide enough.
func (e *ensemble) input(x *Tensor) (n, cols int, v []float32, err error) {
	if v, err = x.floats(); err != nil {
		return 0, 0, nil, err
	}
	if n, cols, err = x.rows(); err != nil {
		return 0, 0, nil, err
	}
	if e.maxFeature >= cols {
		return 0, 0, nil, fmt.Errorf("the trees read feature %d of %d", e.maxFeature, cols)
	}
	return n, cols, v, nil
}

// How a classifier turns its scores into a label and probabilities. Only
// the combinations the worker's exports use, each checked against ONNX
// Runtime by the fixtures, are accepted.
type classifierKind uint8

const (
	// More than two classes: base values added, the label is the highest
	// score among the classes that have one, then post_transform.
	multiClass classifierKind = iota
	// Two classes, weights only for class 0, all of them non-negative and no
	// post_transform (random forests): s is the probability of the second
	// class, the label is the second class when s > 0.5, probabilities
	// [1-s, s].
	binaryProbability
	// Two classes, weights only for class 0, some negative, LOGISTIC
	// (gradient boosting and XGBoost): s is a log-odds, the label is the
	// second class when s > 0, probabilities [logistic(-s), logistic(s)].
	binaryLogOdds
)

func compileTreeClassifier(c *nodeCtx) (op, error) {
	if err := c.arity(1, 1, 2); err != nil {
		return nil, err
	}
	labels, err := readLabels(c, "classlabels_strings", "classlabels_int64s")
	if err != nil {
		return nil, err
	}
	classes := labels.len()
	e, ids, weights, err := readEnsemble(c, "class", classes)
	if err != nil {
		return nil, err
	}
	base, err := c.floats("base_values")
	if err != nil {
		return nil, err
	}
	post, err := c.str("post_transform", "NONE")
	if err != nil {
		return nil, err
	}
	kind, err := classifierKindOf(classes, ids, weights, base, post)
	if err != nil {
		return nil, err
	}
	tc := &treeClassifier{e: e, kind: kind, base: base, post: post, classes: classes}
	return opFunc(func(in []*Tensor) ([]*Tensor, error) {
		best, probs, err := tc.run(in[0])
		if err != nil {
			return nil, err
		}
		return []*Tensor{labels.pick(best), probs}, nil
	}), nil
}

// treeClassifier scores a TreeEnsembleClassifier as ONNX Runtime does.
type treeClassifier struct {
	e       ensemble
	kind    classifierKind
	base    []float32
	post    string
	classes int
}

// run returns each row's class index and the [n, classes] scores.
func (tc *treeClassifier) run(x *Tensor) ([]int, *Tensor, error) {
	n, cols, v, err := tc.e.input(x)
	if err != nil {
		return nil, nil, err
	}
	k := tc.classes
	best := make([]int, n)
	probs := make([]float32, n*k)
	scores, has := make([]float32, k), make([]bool, k)
	for r := range n {
		clear(scores)
		clear(has)
		tc.e.score(v[r*cols:(r+1)*cols], scores, has)
		if best[r], err = tc.finish(scores, has, probs[r*k:(r+1)*k]); err != nil {
			return nil, nil, err
		}
	}
	return best, &Tensor{Type: Float, Shape: []int{n, k}, Floats: probs}, nil
}

// finish turns one row's summed scores into its class and its output scores.
func (tc *treeClassifier) finish(scores []float32, has []bool, out []float32) (int, error) {
	switch tc.kind {
	case multiClass:
		for k, b := range tc.base {
			scores[k] += b
			has[k] = true
		}
		best := -1
		for k := range scores {
			if has[k] && (best < 0 || scores[k] > scores[best]) {
				best = k
			}
		}
		if best < 0 {
			return 0, errors.New("no tree gave any class a score")
		}
		copy(out, scores)
		transform(out, tc.post)
		return best, nil
	case binaryProbability:
		s := scores[0]
		out[0], out[1] = 1-s, s
		if has[0] && s > 0.5 {
			return 1, nil
		}
		return 0, nil
	default: // binaryLogOdds
		s := scores[0]
		if len(tc.base) == 1 {
			s += tc.base[0]
		}
		out[0], out[1] = logistic(-s), logistic(s)
		if has[0] && s > 0 {
			return 1, nil
		}
		return 0, nil
	}
}

func classifierKindOf(classes int, ids []int64, weights, base []float32, post string) (classifierKind, error) {
	if classes > 2 {
		return multiClass, checkMultiClass(classes, base, post)
	}
	onlyFirst := len(ids) > 0 && !slices.ContainsFunc(ids, func(id int64) bool { return id != 0 })
	allPositive := !slices.ContainsFunc(weights, func(w float32) bool { return w < 0 })
	if classes == 2 && onlyFirst {
		switch {
		case allPositive && len(base) == 0 && post == "NONE":
			return binaryProbability, nil
		case !allPositive && len(base) <= 1 && post == "LOGISTIC":
			return binaryLogOdds, nil
		}
	}
	return 0, unsupported("a two-class ensemble with these weights, %d base values and post_transform %q", len(base), post)
}

func checkMultiClass(classes int, base []float32, post string) error {
	if len(base) != 0 && len(base) != classes {
		return fmt.Errorf("%d base values for %d classes", len(base), classes)
	}
	if post != "NONE" && post != "LOGISTIC" && post != "SOFTMAX" {
		return unsupported("post_transform %q", post)
	}
	return nil
}

func compileTreeRegressor(c *nodeCtx) (op, error) {
	if err := c.arity(1, 1, 1); err != nil {
		return nil, err
	}
	targets, err := c.int("n_targets", 1)
	if err != nil {
		return nil, err
	}
	if targets < 1 || targets > math.MaxInt32 {
		return nil, fmt.Errorf("n_targets %d", targets)
	}
	e, _, _, err := readEnsemble(c, "target", int(targets))
	if err != nil {
		return nil, err
	}
	base, average, err := readRegressorAttrs(c, int(targets))
	if err != nil {
		return nil, err
	}
	t := int(targets)
	trees := float32(len(e.roots))
	return opFunc(func(in []*Tensor) ([]*Tensor, error) {
		n, cols, v, err := e.input(in[0])
		if err != nil {
			return nil, err
		}
		out := make([]float32, n*t)
		has := make([]bool, t)
		for r := range n {
			scores := out[r*t : (r+1)*t]
			e.score(v[r*cols:(r+1)*cols], scores, has)
			finishRegression(scores, base, average, trees)
		}
		return []*Tensor{{Type: Float, Shape: []int{n, t}, Floats: out}}, nil
	}), nil
}

// readRegressorAttrs reads a regressor's base values and whether it
// averages its trees, refusing any post_transform.
func readRegressorAttrs(c *nodeCtx, targets int) (base []float32, average bool, err error) {
	if base, err = c.floats("base_values"); err != nil {
		return nil, false, err
	}
	if len(base) != 0 && len(base) != targets {
		return nil, false, fmt.Errorf("%d base values for %d targets", len(base), targets)
	}
	agg, err := c.str("aggregate_function", "SUM")
	if err != nil {
		return nil, false, err
	}
	if agg != "SUM" && agg != "AVERAGE" {
		return nil, false, unsupported("aggregate_function %q", agg)
	}
	post, err := c.str("post_transform", "NONE")
	if err != nil {
		return nil, false, err
	}
	if post != "NONE" {
		return nil, false, unsupported("post_transform %q", post)
	}
	return base, agg == "AVERAGE", nil
}

// finishRegression averages one row's sums over the trees if asked, then
// adds the base values.
func finishRegression(scores, base []float32, average bool, trees float32) {
	for k := range scores {
		if average {
			scores[k] /= trees
		}
		if len(base) > 0 {
			scores[k] += base[k]
		}
	}
}
