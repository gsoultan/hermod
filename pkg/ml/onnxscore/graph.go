package onnxscore

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The opset versions this package has been checked against. A model
// declaring a later one may use semantics it does not know.
const (
	maxDefaultOpset = 23
	maxMLOpset      = 3
	mlDomain        = "ai.onnx.ml"
)

// Model is a loaded, checked graph. It is immutable and safe for concurrent
// use.
type Model struct {
	inputs  []valueInfo
	outputs []valueInfo
	consts  map[string]*Tensor
	steps   []step
	ops     []string
}

// step is one node: its operator, and the names it reads and writes.
type step struct {
	name    string
	op      op
	inputs  []string
	outputs []string
}

// op is a compiled operator.
type op interface {
	run(in []*Tensor) ([]*Tensor, error)
}

// compiler builds an operator from its node, or says why it cannot.
type compiler func(c *nodeCtx) (op, error)

// compilers are the supported operators, by domain-qualified name: the
// default domain's operators by name alone.
var compilers = map[string]compiler{
	"Concat":                             compileConcat,
	"Gather":                             compileGather,
	"Reshape":                            compileReshape,
	"Identity":                           compileIdentity,
	"Cast":                               compileCast,
	"Softmax":                            compileSoftmax,
	"ArgMax":                             compileArgMax,
	mlDomain + ".Scaler":                 compileScaler,
	mlDomain + ".Normalizer":             compileNormalizer,
	mlDomain + ".OneHotEncoder":          compileOneHotEncoder,
	mlDomain + ".FeatureVectorizer":      compileFeatureVectorizer,
	mlDomain + ".LinearClassifier":       compileLinearClassifier,
	mlDomain + ".LinearRegressor":        compileLinearRegressor,
	mlDomain + ".TreeEnsembleClassifier": compileTreeClassifier,
	mlDomain + ".TreeEnsembleRegressor":  compileTreeRegressor,
}

// opName is the operator's domain-qualified name.
func opName(domain, op string) string {
	if domain == "" || domain == "ai.onnx" {
		return op
	}
	return domain + "." + op
}

// Load decodes an ONNX model and checks that every part of it can be scored
// exactly. It returns an error wrapping ErrUnsupported, naming every
// operator it does not support, for a model it cannot; any other error means
// the bytes are not a well-formed model.
func Load(raw []byte) (*Model, error) {
	mp, err := decodeModel(raw)
	if err != nil {
		return nil, err
	}
	versions, err := opsetVersions(mp.opsets)
	if err != nil {
		return nil, err
	}
	if err := checkOps(mp.graph.nodes); err != nil {
		return nil, err
	}
	m := &Model{consts: map[string]*Tensor{}}
	defined, err := m.declare(mp.graph)
	if err != nil {
		return nil, err
	}
	for _, n := range mp.graph.nodes {
		if err := m.compile(n, versions, defined); err != nil {
			return nil, err
		}
	}
	sort.Strings(m.ops)
	if err := m.setOutputs(mp.graph.outputs, defined); err != nil {
		return nil, err
	}
	return m, nil
}

// opsetVersions checks the opsets the model imports and returns the
// version of each domain the scorer knows, the default domain under "".
func opsetVersions(opsets map[string]int64) (map[string]int64, error) {
	versions := map[string]int64{}
	for domain, v := range opsets {
		switch domain {
		case "", "ai.onnx":
			if v < 1 || v > maxDefaultOpset {
				return nil, unsupported("ai.onnx opset %d (supported: 1 to %d)", v, maxDefaultOpset)
			}
			versions[""] = v
		case mlDomain:
			if v < 1 || v > maxMLOpset {
				return nil, unsupported("ai.onnx.ml opset %d (supported: 1 to %d)", v, maxMLOpset)
			}
			versions[mlDomain] = v
		}
	}
	return versions, nil
}

// checkOps refuses a graph using any operator without a compiler, naming
// every one of them.
func checkOps(nodes []nodeProto) error {
	var missing []string
	for _, n := range nodes {
		name := opName(n.domain, n.op)
		if _, ok := compilers[name]; !ok && !slices.Contains(missing, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return unsupported("operators %s are not supported", strings.Join(missing, ", "))
}

// declare loads the initializers and the graph's inputs, and returns the
// names they define.
func (m *Model) declare(g graphProto) (map[string]bool, error) {
	defined := map[string]bool{}
	for i := range g.inits {
		t, err := g.inits[i].tensor()
		if err != nil {
			return nil, err
		}
		m.consts[g.inits[i].name] = t
		defined[g.inits[i].name] = true
	}
	for _, in := range g.inputs {
		if defined[in.name] {
			continue // an initializer listed as an input, as IR version 3 did
		}
		if !in.tensor {
			return nil, unsupported("input %q is not a tensor", in.name)
		}
		m.inputs = append(m.inputs, in)
		defined[in.name] = true
	}
	return defined, nil
}

// compile adds one node as a step, after the nodes before it.
func (m *Model) compile(n nodeProto, versions map[string]int64, defined map[string]bool) error {
	name := opName(n.domain, n.op)
	domain := n.domain
	if domain == "ai.onnx" {
		domain = ""
	}
	version, ok := versions[domain]
	if !ok {
		return unsupported("operator %s is in a domain the model does not import", name)
	}
	for _, in := range n.inputs {
		if !defined[in] {
			return fmt.Errorf("operator %s reads %q, which nothing before it defines", name, in)
		}
	}
	c := newNodeCtx(n, version, m.consts)
	o, err := compilers[name](c)
	if err == nil {
		err = c.done()
	}
	if err != nil {
		return fmt.Errorf("operator %s: %w", name, err)
	}
	for _, out := range n.outputs {
		if out == "" || defined[out] {
			return unsupported("operator %s writes %q, which is empty or already defined", name, out)
		}
		defined[out] = true
	}
	m.steps = append(m.steps, step{name: name, op: o, inputs: n.inputs, outputs: n.outputs})
	if !slices.Contains(m.ops, name) {
		m.ops = append(m.ops, name)
	}
	return nil
}

// setOutputs checks the graph's outputs are tensors it computes.
func (m *Model) setOutputs(outputs []valueInfo, defined map[string]bool) error {
	for _, out := range outputs {
		if !out.tensor {
			return unsupported("output %q is not a tensor", out.name)
		}
		if !defined[out.name] {
			return fmt.Errorf("output %q is not computed by the graph", out.name)
		}
		m.outputs = append(m.outputs, out)
	}
	if len(m.outputs) == 0 {
		return errors.New("the graph has no outputs")
	}
	return nil
}

// Ops lists the operators the graph uses, sorted: the default domain's by
// name, others qualified by their domain ("ai.onnx.ml.Scaler").
func (m *Model) Ops() []string {
	return slices.Clone(m.ops)
}

// InputType is the element type a graph input declares, and whether the
// graph has that input.
func (m *Model) InputType(name string) (DataType, bool) {
	for _, in := range m.inputs {
		if in.name == name {
			return DataType(in.elemType), true
		}
	}
	return 0, false
}

// Inputs lists the graph's input names, in order.
func (m *Model) Inputs() []string {
	out := make([]string, len(m.inputs))
	for i, in := range m.inputs {
		out[i] = in.name
	}
	return out
}

// Run evaluates the graph on the named inputs and returns its outputs in the
// order the graph declares them.
func (m *Model) Run(inputs map[string]*Tensor) ([]*Tensor, error) {
	env := make(map[string]*Tensor, len(m.consts)+len(inputs)+2*len(m.steps))
	for k, v := range m.consts {
		env[k] = v
	}
	for _, in := range m.inputs {
		t, ok := inputs[in.name]
		if !ok || t == nil {
			return nil, fmt.Errorf("input %q is missing", in.name)
		}
		if !t.valid() {
			return nil, fmt.Errorf("input %q holds %d values for shape %v", in.name, t.count(), t.Shape)
		}
		if in.elemType != 0 && DataType(in.elemType) != t.Type {
			return nil, fmt.Errorf("input %q is %s; the model takes %s", in.name, t.Type, DataType(in.elemType))
		}
		env[in.name] = t
	}
	args := make([]*Tensor, 0, 4)
	for _, s := range m.steps {
		args = args[:0]
		for _, name := range s.inputs {
			args = append(args, env[name])
		}
		outs, err := s.op.run(args)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.name, err)
		}
		if len(outs) < len(s.outputs) {
			return nil, fmt.Errorf("%s computed %d outputs; the graph names %d", s.name, len(outs), len(s.outputs))
		}
		for i, name := range s.outputs {
			env[name] = outs[i]
		}
	}
	out := make([]*Tensor, len(m.outputs))
	for i, o := range m.outputs {
		out[i] = env[o.name]
	}
	return out, nil
}

// nodeCtx is what a compiler reads a node through. Every attribute must be
// read; done reports the ones nobody did, which this package therefore does
// not understand.
type nodeCtx struct {
	node    nodeProto
	version int64
	consts  map[string]*Tensor
	attrs   map[string]attrProto
	read    map[string]bool
}

func newNodeCtx(n nodeProto, version int64, consts map[string]*Tensor) *nodeCtx {
	c := &nodeCtx{node: n, version: version, consts: consts, attrs: map[string]attrProto{}, read: map[string]bool{}}
	for _, a := range n.attrs {
		c.attrs[a.name] = a
	}
	return c
}

func (c *nodeCtx) done() error {
	var extra []string
	for name := range c.attrs {
		if !c.read[name] {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return unsupported("attributes %s are not supported", strings.Join(extra, ", "))
	}
	return nil
}

// arity checks the node's input and output counts.
func (c *nodeCtx) arity(minIn, maxIn, outputs int) error {
	if n := len(c.node.inputs); n < minIn || n > maxIn {
		return fmt.Errorf("has %d inputs, expected %d to %d", n, minIn, maxIn)
	}
	if len(c.node.outputs) > outputs || len(c.node.outputs) == 0 {
		return unsupported("has %d outputs, expected 1 to %d", len(c.node.outputs), outputs)
	}
	for _, in := range c.node.inputs {
		if in == "" {
			return unsupported("an optional input is left out")
		}
	}
	return nil
}

func (c *nodeCtx) attr(name string, typ int32) (attrProto, bool, error) {
	a, ok := c.attrs[name]
	if !ok {
		return attrProto{}, false, nil
	}
	c.read[name] = true
	if a.typ != typ {
		return attrProto{}, false, fmt.Errorf("attribute %q has type %d, expected %d", name, a.typ, typ)
	}
	return a, true, nil
}

func (c *nodeCtx) int(name string, def int64) (int64, error) {
	a, ok, err := c.attr(name, attrInt)
	if !ok {
		return def, err
	}
	return a.i, nil
}

func (c *nodeCtx) str(name, def string) (string, error) {
	a, ok, err := c.attr(name, attrString)
	if !ok {
		return def, err
	}
	return string(a.s), nil
}

func (c *nodeCtx) ints(name string) ([]int64, error) {
	a, _, err := c.attr(name, attrInts)
	return a.ints, err
}

func (c *nodeCtx) floats(name string) ([]float32, error) {
	a, _, err := c.attr(name, attrFloats)
	return a.floats, err
}

func (c *nodeCtx) strs(name string) ([]string, error) {
	a, _, err := c.attr(name, attrStrings)
	out := make([]string, len(a.strings))
	for i, s := range a.strings {
		out[i] = string(s)
	}
	return out, err
}

// ignore marks attributes that do not change the result, such as a tree's
// hit rates.
func (c *nodeCtx) ignore(names ...string) {
	for _, n := range names {
		if _, ok := c.attrs[n]; ok {
			c.read[n] = true
		}
	}
}

// constant is an input that must be an initializer, such as Reshape's shape.
func (c *nodeCtx) constant(i int) (*Tensor, error) {
	t, ok := c.consts[c.node.inputs[i]]
	if !ok {
		return nil, unsupported("input %q must be a constant", c.node.inputs[i])
	}
	return t, nil
}

// axis normalises a possibly negative axis for a tensor of the given rank.
func axis(a int64, rank int) (int, error) {
	if a < 0 {
		a += int64(rank)
	}
	if a < 0 || a >= int64(rank) {
		return 0, fmt.Errorf("axis %d is out of range for rank %d", a, rank)
	}
	return int(a), nil
}
