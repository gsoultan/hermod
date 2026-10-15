"""Deep learning, cheaply: precision and threads settings, ONNX graph
optimisation, int8 quantisation with its tolerance check, and thread caps.

None of these needs torch. The end-to-end tests train through the API with
a stand-in network (scikit-learn's MLP, exported as torch exports one: Gemm
layers), so the quantisation path runs where the "-dl" image is absent.
"""

import os

import numpy as np
import onnx
import onnxruntime as ort
import pytest
from onnx import TensorProto, helper, numpy_helper

from hermod_ml import deep
from hermod_ml.errors import ApiError
from hermod_ml.settings import Settings
from tests.helpers import classification_rows, load, regression_rows, train

NETWORK_DEFAULTS = {"hidden_layers": [64, 32], "epochs": 200, "batch_size": 32, "learning_rate": 0.001, "patience": 10}


# --------------------------------------------------------------------------
# Settings
# --------------------------------------------------------------------------


def test_params_take_precision_and_threads_with_defaults():
    params = deep.parse_params(None)
    assert params == {**NETWORK_DEFAULTS, "precision": "auto", "threads": 0}
    assert deep.network_params(params) == NETWORK_DEFAULTS
    params = deep.parse_params({"precision": "int8", "threads": 2})
    assert (params["precision"], params["threads"]) == ("int8", 2)


@pytest.mark.parametrize(
    "raw, key",
    [
        ({"precision": "fp16"}, "params.precision"),
        ({"precision": 8}, "params.precision"),
        ({"threads": -1}, "params.threads"),
        ({"threads": 257}, "params.threads"),
        ({"threads": True}, "params.threads"),
        ({"threads": "2"}, "params.threads"),
    ],
)
def test_bad_efficiency_params_are_refused(raw, key):
    with pytest.raises(ApiError) as err:
        deep.parse_params(raw)
    assert err.value.status == 400
    assert key in err.value.message


def test_threads_default_to_every_cpu_but_one_and_obey_the_cap():
    cpus = deep.cpu_count()
    assert deep.resolve_threads(0, 0) == max(1, cpus - 1)
    assert deep.resolve_threads(2, 0) == 2
    assert deep.resolve_threads(6, 3) == 3
    assert deep.resolve_threads(0, 1) == 1


def test_settings_read_the_thread_caps():
    s = Settings.from_env({})
    assert (s.train_threads, s.serve_threads) == (0, 1)
    s = Settings.from_env({"HERMOD_ML_TRAIN_THREADS": "3", "HERMOD_ML_SERVE_THREADS": "0"})
    assert (s.train_threads, s.serve_threads) == (3, 0)
    for env in ({"HERMOD_ML_TRAIN_THREADS": "-1"}, {"HERMOD_ML_SERVE_THREADS": "many"}):
        with pytest.raises(ValueError):
            Settings.from_env(env)


@pytest.mark.parametrize(
    "requested, task, f32, i8, want",
    [
        ("auto", "classification", 0.90, 0.897, "int8"),
        ("auto", "classification", 0.90, 0.89, "float32"),
        ("auto", "regression", 1.00, 1.009, "int8"),
        ("auto", "regression", 1.00, 1.02, "float32"),
        ("int8", "classification", 0.90, 0.50, "int8"),
    ],
)
def test_int8_is_kept_only_within_the_tolerance(requested, task, f32, i8, want):
    check = deep.quant_check(task, f32, i8)
    assert deep.choose_precision(requested, check) == want


# --------------------------------------------------------------------------
# Graphs
# --------------------------------------------------------------------------


def gemm_network(sizes, seed=0, softmax=True) -> onnx.ModelProto:
    """A network as torch exports one: Gemm (transB=1) and Relu layers, then
    a softmax, from an [n, sizes[0]] input "x" to an output "y"."""
    rng = np.random.default_rng(seed)
    nodes, inits, last = [], [], "x"
    for i, (n_in, n_out) in enumerate(zip(sizes, sizes[1:])):
        w = (rng.standard_normal((n_out, n_in)) / np.sqrt(n_in)).astype(np.float32)
        b = (rng.standard_normal(n_out) * 0.1).astype(np.float32)
        inits += [numpy_helper.from_array(w, f"w{i}"), numpy_helper.from_array(b, f"b{i}")]
        nodes.append(helper.make_node("Gemm", [last, f"w{i}", f"b{i}"], [f"z{i}"], transB=1))
        last = f"z{i}"
        if i < len(sizes) - 2:
            nodes.append(helper.make_node("Relu", [last], [f"a{i}"]))
            last = f"a{i}"
    if softmax:
        nodes.append(helper.make_node("Softmax", [last], ["y"], axis=1))
    else:
        nodes.append(helper.make_node("Identity", [last], ["y"]))
    graph = helper.make_graph(
        nodes, "net", [helper.make_tensor_value_info("x", TensorProto.FLOAT, [None, sizes[0]])],
        [helper.make_tensor_value_info("y", TensorProto.FLOAT, [None, sizes[-1]])], inits,
    )
    return helper.make_model(graph, opset_imports=[helper.make_opsetid("", deep.OPSET)], ir_version=10)


def run(model_bytes: bytes, x: np.ndarray) -> np.ndarray:
    return ort.InferenceSession(model_bytes, providers=["CPUExecutionProvider"]).run(None, {"x": x})[0]


def test_gemm_becomes_matmul_and_computes_the_same():
    model = gemm_network([12, 8, 3])
    x = np.random.default_rng(1).standard_normal((50, 12)).astype(np.float32)
    want = run(model.SerializeToString(), x)
    deep.gemm_to_matmul(model)
    ops = [n.op_type for n in model.graph.node]
    assert "Gemm" not in ops and ops.count("MatMul") == 2
    onnx.checker.check_model(model)
    np.testing.assert_allclose(run(model.SerializeToString(), x), want, rtol=1e-5, atol=1e-6)


def test_optimising_a_graph_keeps_its_names_and_answers():
    model = gemm_network([12, 8, 3])
    x = np.random.default_rng(2).standard_normal((20, 12)).astype(np.float32)
    raw = model.SerializeToString()
    optimised = deep.optimize(raw)
    back = onnx.load_from_string(optimised)
    assert [i.name for i in back.graph.input] == ["x"]
    assert [o.name for o in back.graph.output] == ["y"]
    np.testing.assert_allclose(run(optimised, x), run(raw, x), rtol=1e-5, atol=1e-6)


def test_int8_quantisation_shrinks_the_graph_and_stays_close():
    model = gemm_network([200, 64, 32, 2], seed=3)
    raw = model.SerializeToString()
    small = deep.quantize(raw)
    ops = {n.op_type for n in onnx.load_from_string(small).graph.node}
    assert "MatMulInteger" in ops or "DynamicQuantizeMatMul" in ops
    assert len(small) < 0.4 * len(raw)
    x = np.random.default_rng(4).standard_normal((500, 200)).astype(np.float32)
    assert np.abs(run(small, x) - run(raw, x)).max() < 0.05


# --------------------------------------------------------------------------
# Through the API, with a stand-in network
# --------------------------------------------------------------------------


class StandInMLP(deep._MLP):
    """scikit-learn's MLP behind the deep-learning estimator's interface,
    exported to ONNX as torch's exporter writes one."""

    def _fit(self, X, y, X_val, y_val, outputs: int) -> None:
        from sklearn.neural_network import MLPRegressor

        target = np.eye(outputs, dtype=np.float32)[y] if self.task == "classification" else y.ravel()
        est = MLPRegressor(hidden_layer_sizes=tuple(self.hidden_layers), max_iter=max(self.epochs, 200),
                           learning_rate_init=0.01, random_state=self.seed)
        est.fit(X, target)
        self.coefs_ = [w.astype(np.float32) for w in est.coefs_]
        self.intercepts_ = [b.astype(np.float32) for b in est.intercepts_]
        self.outputs_ = outputs

    def _forward(self, X):
        a = X
        for i, (w, b) in enumerate(zip(self.coefs_, self.intercepts_)):
            a = a @ w + b
            if i < len(self.coefs_) - 1:
                a = np.maximum(a, 0)
        a = a.reshape(len(X), -1)
        if self.task == "classification":
            e = np.exp(a - a.max(axis=1, keepdims=True))
            return e / e.sum(axis=1, keepdims=True)
        return a * np.float32(self.std_) + np.float32(self.mean_)

    def to_onnx(self):
        nodes, inits, last = [], [], "x"
        for i, (w, b) in enumerate(zip(self.coefs_, self.intercepts_)):
            inits += [numpy_helper.from_array(np.ascontiguousarray(w.T), f"w{i}"), numpy_helper.from_array(b, f"b{i}")]
            nodes.append(helper.make_node("Gemm", [last, f"w{i}", f"b{i}"], [f"z{i}"], transB=1))
            last = f"z{i}"
            if i < len(self.coefs_) - 1:
                nodes.append(helper.make_node("Relu", [last], [f"a{i}"]))
                last = f"a{i}"
        if self.task == "classification":
            nodes.append(helper.make_node("Softmax", [last], ["y"], axis=1))
        else:
            inits += [numpy_helper.from_array(np.float32([self.std_]), "std"), numpy_helper.from_array(np.float32([self.mean_]), "mean")]
            nodes += [helper.make_node("Mul", [last, "std"], ["scaled"]), helper.make_node("Add", ["scaled", "mean"], ["y"])]
        graph = helper.make_graph(
            nodes, "net", [helper.make_tensor_value_info("x", TensorProto.FLOAT, [None, self.n_inputs_])],
            [helper.make_tensor_value_info("y", TensorProto.FLOAT, [None, self.outputs_])], inits,
        )
        return helper.make_model(graph, opset_imports=[helper.make_opsetid("", deep.OPSET)], ir_version=10)


@pytest.fixture
def stand_in(monkeypatch):
    def make(algorithm, task, params, seed):
        return StandInMLP(task=task, seed=seed, **deep.estimator_params(params))

    monkeypatch.setattr(deep, "make_estimator", make)


WIDTH = 12


def wide_rows(n=600, seed=5):
    """Rows whose yes/no target depends on two of WIDTH numbers."""
    rng = np.random.default_rng(seed)
    rows = []
    for _ in range(n):
        row = {f"x{j}": float(rng.standard_normal()) for j in range(WIDTH)}
        row["y"] = "yes" if row["x0"] + row["x1"] > 0 else "no"
        rows.append(row)
    return rows


def test_a_network_is_kept_in_int8_when_it_scores_as_well(client, tmp_path, stand_in):
    load(client, "wide", wide_rows())
    resp = train(client, "c", dataset="wide", target="y", algorithm="pytorch_mlp", params={"hidden_layers": [64, 32], "threads": 2})
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    eff = meta["efficiency"]
    assert eff["requested"] == "auto"
    assert eff["threads"] == 2
    assert eff["metric"] == "accuracy" and eff["tolerance"] == 0.005
    assert eff["precision"] == "int8", eff
    assert eff["int8"] >= eff["float32"] - eff["tolerance"]
    assert meta["params"] == {**NETWORK_DEFAULTS, "hidden_layers": [64, 32]}
    ops = {n.op_type for n in onnx.load(str(tmp_path / "data/models/acme/c/1/model.onnx")).graph.node}
    assert "MatMulInteger" in ops or "DynamicQuantizeMatMul" in ops
    # Scored as served, in int8, it scores like the same network in float32.
    f32 = train(client, "c32", dataset="wide", target="y", algorithm="pytorch_mlp",
                params={"hidden_layers": [64, 32], "precision": "float32"}).json()
    assert f32["efficiency"]["precision"] == "float32"
    assert abs(meta["metrics"]["accuracy"] - f32["metrics"]["accuracy"]) <= 0.05, (meta["metrics"], f32["metrics"])
    assert meta["metrics"]["accuracy"] > 0.8
    out = client.post("/vhosts/acme/v2/models/c/infer", json={"inputs": [
        {"name": f"x{j}", "shape": [2], "datatype": "FP64", "data": [3.0, -3.0] if j < 2 else [0.0, 0.0]} for j in range(WIDTH)
    ]})
    assert out.status_code == 200, out.text
    assert out.json()["outputs"][0]["data"] == ["yes", "no"]


def test_float32_keeps_the_float_graph(client, tmp_path, stand_in):
    load(client, "sales", regression_rows())
    resp = train(client, "r", dataset="sales", target="amount", algorithm="pytorch_mlp", params={"precision": "float32"})
    assert resp.status_code == 200, resp.text
    eff = resp.json()["efficiency"]
    assert (eff["requested"], eff["precision"]) == ("float32", "float32")
    assert "int8" not in eff
    ops = {n.op_type for n in onnx.load(str(tmp_path / "data/models/acme/r/1/model.onnx")).graph.node}
    assert not ops & {"MatMulInteger", "DynamicQuantizeMatMul", "DynamicQuantizeLinear"}


def test_a_regression_network_checks_int8_on_rmse(client, stand_in):
    load(client, "sales", regression_rows())
    resp = train(client, "r", dataset="sales", target="amount", algorithm="pytorch_mlp")
    assert resp.status_code == 200, resp.text
    eff = resp.json()["efficiency"]
    assert eff["metric"] == "rmse" and eff["tolerance"] == 0.01
    assert eff["precision"] in ("int8", "float32")
    if eff["precision"] == "int8":
        assert eff["int8"] <= eff["float32"] * 1.01 + 1e-12


def test_other_algorithms_record_no_efficiency(client):
    load(client, "orders", classification_rows())
    resp = train(client, "lin", dataset="orders", target="churned", algorithm="linear")
    assert resp.status_code == 200, resp.text
    assert "efficiency" not in resp.json()


def test_serving_sessions_obey_the_thread_cap(make_client):
    client = make_client(serve_threads=1)
    load(client, "orders", classification_rows())
    assert train(client, "lin", dataset="orders", target="churned", algorithm="linear").status_code == 200
    out = client.post("/vhosts/acme/v2/models/lin/infer", json={"inputs": [
        {"name": "id", "shape": [1], "datatype": "FP64", "data": [1.0]},
        {"name": "x1", "shape": [1], "datatype": "FP64", "data": [1.0]},
        {"name": "x2", "shape": [1], "datatype": "FP64", "data": [1.0]},
        {"name": "city", "shape": [1], "datatype": "BYTES", "data": ["Oslo"]},
        {"name": "flag", "shape": [1], "datatype": "BOOL", "data": [True]},
    ]})
    assert out.status_code == 200, out.text
    server = client.app.state.server
    [key] = server.cache.keys()
    session, _ = server.cache.get(key, None)
    assert session.get_session_options().intra_op_num_threads == 1


def test_cpu_count_respects_the_affinity_mask():
    if hasattr(os, "sched_getaffinity"):
        assert deep.cpu_count() == len(os.sched_getaffinity(0))


def test_a_network_one_hot_encodes_only_a_columns_top_100_values(client, tmp_path, stand_in):
    rng = np.random.default_rng(8)
    rows = []
    for i in range(1500):
        # 300 shop ids; s0..s9 are the frequent ones, the rest rare.
        shop = f"s{i % 10}" if i % 2 else f"s{10 + int(rng.integers(290))}"
        x = float(rng.standard_normal())
        rows.append({"x": x, "shop": shop, "y": "yes" if x > 0 else "no"})
    load(client, "shops", rows)
    resp = train(client, "c", dataset="shops", target="y", algorithm="pytorch_mlp",
                 params={"hidden_layers": [8], "precision": "float32"})
    assert resp.status_code == 200, resp.text
    model = onnx.load(str(tmp_path / "data/models/acme/c/1/model.onnx"))
    first = next(t for t in model.graph.initializer if t.name == "nn_w0")
    # One number plus at most 100 shop columns, not one per shop.
    assert list(first.dims) == [8, 1 + deep.MAX_CATEGORIES]
    out = client.post("/vhosts/acme/v2/models/c/infer", json={"inputs": [
        {"name": "x", "shape": [2], "datatype": "FP64", "data": [2.0, -2.0]},
        {"name": "shop", "shape": [2], "datatype": "BYTES", "data": ["never-seen", "s1"]},
    ]})
    assert out.status_code == 200, out.text
    assert out.json()["outputs"][0]["data"] == ["yes", "no"]


def test_top_categories_are_the_most_frequent_ties_to_the_smaller():
    import pandas as pd

    values = pd.Series(["b", "a", "c", "a", "b", "d"] + [f"z{i:03d}" for i in range(200)])
    top = deep.top_categories(values)
    assert len(top) == deep.MAX_CATEGORIES
    assert top == sorted(top)
    assert {"a", "b"} <= set(top) and "z199" not in top
