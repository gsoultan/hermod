"""Runs an untrusted ONNX graph once, in a fresh interpreter.

artifacts.py starts this file as `python -I -B onnx_child.py WORKDIR`; the
worker never imports it. onnxruntime aborts the whole process on some
malformed graphs (a failed C++ assertion is not an exception), so a graph
the worker did not build is first run here, where an abort costs this
process only.

WORKDIR holds model.onnx and job.json:

    {"limits": {"cpu_seconds", "memory_mb"}, "task": ..., "features": [...],
     "numeric": {feature: bool}, "columns": {feature: [values]}}

The result goes to result.json: {"labels": [...], "probabilities": [[...]]}
for a classifier, {"labels": [values]} for a regressor.
"""

import json
import os
import resource
import sys


def main():
    workdir = sys.argv[1]
    with open(os.path.join(workdir, "job.json"), encoding="utf-8") as f:
        job = json.load(f)
    limits = job["limits"]
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    resource.setrlimit(resource.RLIMIT_CPU, (limits["cpu_seconds"], limits["cpu_seconds"]))
    resource.setrlimit(resource.RLIMIT_AS, (limits["memory_mb"] << 20, limits["memory_mb"] << 20))

    import numpy as np
    import onnxruntime as ort

    feed = {}
    for feature in job["features"]:
        values = job["columns"][feature]
        if job["numeric"][feature]:
            feed[feature] = np.asarray(values, dtype=np.float32).reshape(-1, 1)
        else:
            feed[feature] = np.asarray(values, dtype=object).reshape(-1, 1)
    session = ort.InferenceSession(os.path.join(workdir, "model.onnx"), providers=["CPUExecutionProvider"])
    outputs = session.run(None, feed)
    result = {"labels": np.asarray(outputs[0]).ravel().tolist()}
    if job["task"] == "classification":
        result["probabilities"] = np.asarray(outputs[1], dtype=np.float64).tolist()
    with open(os.path.join(workdir, "result.json"), "w", encoding="utf-8") as f:
        json.dump(result, f)


if __name__ == "__main__":
    main()
