"""Custom training scripts the sandbox tests run.

Each one follows the documented interface: train(df, spec) -> model and
export_onnx(model, spec) -> bytes.
"""

from __future__ import annotations

import hashlib

# A scikit-learn pipeline over every feature, exported with skl2onnx: one input
# per feature, label + probabilities for a classifier, one value for a
# regressor. This is what a real custom script looks like.
GOOD = '''
import numpy as np
from skl2onnx import convert_sklearn
from skl2onnx.common.data_types import FloatTensorType, StringTensorType
from sklearn.compose import ColumnTransformer
from sklearn.ensemble import ExtraTreesClassifier, ExtraTreesRegressor
from sklearn.pipeline import Pipeline
from sklearn.preprocessing import OneHotEncoder


def train(df, spec):
    print("training on", len(df), "rows")
    features = spec["features"]
    numeric = [f for f in features if spec["feature_types"][f] == "number"]
    strings = [f for f in features if spec["feature_types"][f] == "string"]
    parts = []
    if numeric:
        parts.append(("num", "passthrough", numeric))
    if strings:
        parts.append(("str", OneHotEncoder(handle_unknown="ignore"), strings))
    if spec["task"] == "classification":
        model = ExtraTreesClassifier(n_estimators=20, random_state=spec["seed"])
    else:
        model = ExtraTreesRegressor(n_estimators=20, random_state=spec["seed"])
    pipe = Pipeline([("prep", ColumnTransformer(parts, sparse_threshold=0)), ("model", model)])
    pipe.fit(df[features], df[spec["target"]])
    return pipe


def export_onnx(model, spec):
    types = [
        (f, FloatTensorType([None, 1]) if spec["feature_types"][f] == "number" else StringTensorType([None, 1]))
        for f in spec["features"]
    ]
    options = {id(model.steps[-1][1]): {"zipmap": False}} if spec["task"] == "classification" else None
    return convert_sklearn(model, initial_types=types, options=options).SerializeToString()
'''

# Exports with the default ZipMap: the probabilities come out as a sequence of
# maps, which the serving path cannot read.
ZIPMAP = GOOD.replace('{id(model.steps[-1][1]): {"zipmap": False}}', "None")

# Renames its first input, so it no longer matches a feature.
WRONG_INPUTS = GOOD.replace(
    "return convert_sklearn(model, initial_types=types, options=options).SerializeToString()",
    """onx = convert_sklearn(model, initial_types=types, options=options)
    old = onx.graph.input[0].name
    onx.graph.input[0].name = "renamed"
    for node in onx.graph.node:
        for i, name in enumerate(node.input):
            if name == old:
                node.input[i] = "renamed"
    return onx.SerializeToString()""",
)

# Returns bytes that are not an ONNX model at all.
NOT_ONNX = GOOD.replace("return convert_sklearn(model, initial_types=types, options=options).SerializeToString()",
                        "return b'definitely not onnx'")

# Trains on a one-class subset. onnxruntime 1.20 fails a C++ assertion
# running such a tree ensemble and aborts the whole process.
ONE_CLASS = GOOD.replace(
    'pipe.fit(df[features], df[spec["target"]])',
    'pipe.fit(df[features], df[spec["target"]].map(lambda v: spec["labels"][0]))',
)

# Adds a third class, so the graph has three probability columns where the
# spec has two labels.
EXTRA_CLASS = GOOD.replace(
    'pipe.fit(df[features], df[spec["target"]])',
    'y = df[spec["target"]].copy()\n    y.iloc[:10] = "maybe"\n    pipe.fit(df[features], y)',
)

# Moves one initializer's data to an external file: onnxruntime would read
# that path from disk when the graph is loaded.
EXTERNAL_DATA = GOOD.replace(
    "return convert_sklearn(model, initial_types=types, options=options).SerializeToString()",
    """onx = convert_sklearn(model, initial_types=types, options=options)
    from onnx import numpy_helper, TensorProto
    t = numpy_helper.from_array(np.zeros((1,), dtype=np.float32), name="sneaky")
    t.ClearField("raw_data")
    t.data_location = TensorProto.EXTERNAL
    entry = t.external_data.add()
    entry.key, entry.value = "location", "/etc/passwd"
    onx.graph.initializer.append(t)
    return onx.SerializeToString()""",
)


def sha(source: str) -> str:
    return hashlib.sha256(source.encode()).hexdigest()


def script(name: str, source: str) -> dict:
    return {"name": name, "sha256": sha(source), "source": source}


def with_body(train_body: str, export_body: str = "return b'x'") -> str:
    """A script whose train and export_onnx run the given statements."""
    indent = lambda s: "\n".join("    " + line for line in s.splitlines())
    return f"def train(df, spec):\n{indent(train_body)}\n\n\ndef export_onnx(model, spec):\n{indent(export_body)}\n"
