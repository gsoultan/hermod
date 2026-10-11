"""Runs one custom training script, in a fresh interpreter.

sandbox.py starts this file as `python -I -B sandbox_child.py WORKDIR`; the
worker never imports it. It must stay self-contained: with -I the worker's
package is not on the child's path.

The order matters. The job file is read first (it holds the limits), the
kernel limits are set next, and only then is anything heavy imported or any
script code run, so the script never runs a line without its limits. The
limits are set with soft and hard values the script cannot raise again.

WORKDIR holds:

    job.json        {"limits": {...}, "spec": {...}}
    script.py       the script
    train.parquet   the training rows
    work/           the script's cwd, HOME and TMPDIR
    out/            where model.onnx is written

Anything the script prints goes to the worker's log of the run. A line that
starts with PREFIX is this runner's verdict; exit codes say what kind.
"""

import errno
import importlib.util
import json
import os
import resource
import sys
import traceback

PREFIX = "hermod-sandbox: "
EXIT_SCRIPT_ERROR = 1
EXIT_INTERFACE = 3
EXIT_MEMORY = 4
EXIT_FILE_SIZE = 5

# Seconds between the CPU soft limit (SIGXCPU, which ends the process) and
# the hard limit (SIGKILL, for a script that catches SIGXCPU).
CPU_GRACE = 5


def fail(code, message):
    sys.stdout.flush()
    sys.stderr.write(PREFIX + message + "\n")
    sys.stderr.flush()
    os._exit(code)


def set_limit(which, soft, hard=None):
    resource.setrlimit(which, (soft, soft if hard is None else hard))


def load_script(path):
    spec = importlib.util.spec_from_file_location("training_script", path)
    module = importlib.util.module_from_spec(spec)
    sys.modules["training_script"] = module
    spec.loader.exec_module(module)
    return module


def main():
    workdir = sys.argv[1]
    with open(os.path.join(workdir, "job.json"), encoding="utf-8") as f:
        job = json.load(f)
    limits = job["limits"]
    mb = 1 << 20
    set_limit(resource.RLIMIT_CORE, 0)
    set_limit(resource.RLIMIT_CPU, limits["cpu_seconds"], limits["cpu_seconds"] + CPU_GRACE)
    set_limit(resource.RLIMIT_AS, limits["memory_mb"] * mb)
    set_limit(resource.RLIMIT_FSIZE, limits["file_mb"] * mb)
    set_limit(resource.RLIMIT_NPROC, limits["nproc"])
    os.chdir(os.path.join(workdir, "work"))

    try:
        import pandas as pd

        df = pd.read_parquet(os.path.join(workdir, "train.parquet"))
        module = load_script(os.path.join(workdir, "script.py"))
        train = getattr(module, "train", None)
        export = getattr(module, "export_onnx", None)
        if not callable(train) or not callable(export):
            fail(EXIT_INTERFACE, "the script must define train(df, spec) and export_onnx(model, spec)")
        model = train(df, job["spec"])
        data = export(model, job["spec"])
    except MemoryError:
        traceback.print_exc()
        fail(EXIT_MEMORY, "MemoryError")
    except OSError as exc:
        traceback.print_exc()
        # Python ignores SIGXFSZ, so the file size limit shows up as EFBIG.
        fail(EXIT_FILE_SIZE if exc.errno == errno.EFBIG else EXIT_SCRIPT_ERROR, f"{type(exc).__name__}: {exc}")
    except BaseException as exc:  # SystemExit and KeyboardInterrupt from a script are failures too
        traceback.print_exc()
        fail(EXIT_SCRIPT_ERROR, f"{type(exc).__name__}: {exc}")

    if isinstance(data, (bytearray, memoryview)):
        data = bytes(data)
    if not isinstance(data, bytes):
        fail(EXIT_INTERFACE, f"export_onnx must return bytes, not {type(data).__name__}")
    if len(data) > limits["output_mb"] * mb:
        fail(EXIT_INTERFACE, f"export_onnx returned {len(data)} bytes, larger than {limits['output_mb']} MB")

    out = os.path.join(workdir, "out")
    tmp = os.path.join(out, ".model.tmp")
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "wb") as f:
        f.write(data)
    os.replace(tmp, os.path.join(out, "model.onnx"))


if __name__ == "__main__":
    main()
