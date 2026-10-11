"""The sandbox a custom training script runs in.

These run real subprocesses: each test starts a fresh interpreter, so the
limits are the ones the kernel enforces, not a mock of them.
"""

from __future__ import annotations

import json
import os
import resource
import shutil
import subprocess
import sys
import tempfile
import textwrap
import time
from pathlib import Path

import onnx
import pandas as pd
import pytest

from hermod_ml import sandbox
from hermod_ml.sandbox import SandboxSettings, ScriptFailed, run_script
from tests.scripts import GOOD, with_body

FRAME = pd.DataFrame({"x": [float(i) for i in range(20)], "y": ["a", "b"] * 10})
SPEC = {"task": "classification", "target": "y", "features": ["x"], "feature_types": {"x": "number"}, "labels": ["a", "b"], "seed": 1}
LIMITS = SandboxSettings(enabled=True, cpu_seconds=60, memory_mb=2048, timeout_seconds=60, file_mb=64, nproc=4096, output_mb=10, log_kb=64)


def run(source: str, limits: SandboxSettings = LIMITS, **kw):
    return run_script(source, FRAME, SPEC, limits, **kw)


# -- the happy path ------------------------------------------------------------


def test_a_good_script_returns_its_onnx_and_its_output():
    result = run(GOOD)
    onnx.load_from_string(result.onnx)  # a serialised ModelProto
    assert "training on 20 rows" in result.log


def test_train_sees_the_dataset_and_the_spec():
    source = with_body(
        "print(json.dumps({'rows': len(df), 'columns': list(df.columns), 'spec': spec}))",
        "return b'ok'",
    )
    result = run("import json\n" + source)
    seen = json.loads(result.log.strip().splitlines()[-1])
    assert seen == {"rows": 20, "columns": ["x", "y"], "spec": SPEC}
    assert result.onnx == b"ok"


# -- what the script can see ---------------------------------------------------


def test_the_script_sees_no_tokens_in_its_environment(monkeypatch):
    monkeypatch.setenv("HERMOD_ML_TOKEN", "worker-secret-token")
    monkeypatch.setenv("HERMOD_ML_WORKER_TOKEN", "hermod-secret-token")
    monkeypatch.setenv("AWS_SECRET_ACCESS_KEY", "aws-secret")
    result = run("import os, json\n" + with_body("print(json.dumps(dict(os.environ)))"))
    env = json.loads(result.log.strip().splitlines()[-1])
    assert "secret" not in result.log
    assert set(env) <= sandbox.CHILD_ENV_KEYS | {"HOME", "TMPDIR", "LC_CTYPE"}


def _without_capabilities() -> list[str]:
    """The prefix that runs a command with no capabilities, as in the pod.

    Root, or any process holding CAP_SYS_PTRACE or CAP_PERFMON, reads a
    non-dumpable process's environ anyway; the chart drops every capability.
    """
    if os.geteuid() != 0:
        return []
    setpriv = shutil.which("setpriv")
    if setpriv is None:
        pytest.skip("running as root without setpriv to drop capabilities")
    return [setpriv, "--bounding-set=-all", "--inh-caps=-all", "--"]


@pytest.mark.parametrize("harden", [True, False], ids=["hardened", "control"])
def test_the_script_cannot_read_the_workers_environment_from_proc(harden):
    """The worker's token is in its initial environment, which /proc exposes to
    the same user unless the worker is non-dumpable. The control run proves
    the probe would see it."""
    reader = with_body(
        "import os\n"
        "try:\n"
        "    print('READ', open(f'/proc/{os.getppid()}/environ', 'rb').read())\n"
        "except OSError as exc:\n"
        "    print('DENIED', type(exc).__name__)"
    )
    parent = textwrap.dedent(
        f"""
        import pandas as pd, sys
        from hermod_ml.sandbox import SandboxSettings, harden_parent, run_script
        if {harden!r}:
            harden_parent()
        frame = pd.DataFrame({{"x": [1.0, 2.0]}})
        result = run_script({reader!r}, frame, {{}}, SandboxSettings(enabled=True))
        sys.stdout.write(result.log)
        """
    )
    worker_dir = Path(__file__).parents[1]
    env = {**os.environ, "HERMOD_ML_TOKEN": "worker-secret-token", "PYTHONPATH": str(worker_dir)}
    out = subprocess.run([*_without_capabilities(), sys.executable, "-c", parent], env=env, cwd=worker_dir,
                         capture_output=True, text=True, timeout=120)
    assert out.returncode == 0, out.stderr
    if harden:
        assert "DENIED" in out.stdout
        assert "worker-secret-token" not in out.stdout
    else:
        assert "worker-secret-token" in out.stdout


def test_harden_parent_makes_the_worker_non_dumpable():
    code = "from hermod_ml.sandbox import harden_parent, is_dumpable; harden_parent(); print(is_dumpable())"
    out = subprocess.run([sys.executable, "-c", code], cwd=Path(__file__).parents[1], capture_output=True, text=True, timeout=60)
    assert out.returncode == 0, out.stderr
    assert out.stdout.strip() == "False"


def test_stdin_is_closed():
    result = run("import sys\n" + with_body("print('STDIN', repr(sys.stdin.read()))"))
    assert "STDIN ''" in result.log


def test_the_script_runs_in_a_temp_dir_that_is_removed_afterwards():
    result = run("import os\n" + with_body("print('CWD', os.getcwd())"))
    cwd = next(line.split(" ", 1)[1] for line in result.log.splitlines() if line.startswith("CWD "))
    assert cwd.startswith(os.path.realpath(tempfile.gettempdir()))
    assert not os.path.exists(cwd)


def test_the_kernel_limits_are_the_configured_ones():
    probe = with_body(
        "import resource\n"
        "for name in ('RLIMIT_CPU', 'RLIMIT_AS', 'RLIMIT_FSIZE', 'RLIMIT_NPROC', 'RLIMIT_CORE'):\n"
        "    print(name, *resource.getrlimit(getattr(resource, name)))"
    )
    limits = SandboxSettings(enabled=True, cpu_seconds=50, memory_mb=1500, timeout_seconds=60, file_mb=7, nproc=300)
    log = run(probe, limits).log
    assert "RLIMIT_CPU 50 55" in log
    assert f"RLIMIT_AS {1500 << 20} {1500 << 20}" in log
    assert f"RLIMIT_FSIZE {7 << 20} {7 << 20}" in log
    assert "RLIMIT_NPROC 300 300" in log
    assert "RLIMIT_CORE 0 0" in log


# -- what stops a script -------------------------------------------------------


def test_a_script_over_its_memory_limit_is_stopped():
    limits = SandboxSettings(enabled=True, memory_mb=1024, cpu_seconds=60, timeout_seconds=60)
    with pytest.raises(ScriptFailed) as err:
        run(with_body("hog = bytearray(3 << 30)\nprint(len(hog))"), limits)
    assert "memory" in err.value.message.lower()
    assert "1024 MB" in err.value.message


def test_a_script_over_its_cpu_limit_is_stopped():
    limits = SandboxSettings(enabled=True, cpu_seconds=4, timeout_seconds=120)
    start = time.monotonic()
    with pytest.raises(ScriptFailed) as err:
        run(with_body("while True:\n    pass"), limits)
    assert "4 CPU seconds" in err.value.message
    assert time.monotonic() - start < 60


def test_a_script_over_its_wall_clock_limit_is_killed():
    limits = SandboxSettings(enabled=True, cpu_seconds=600, timeout_seconds=4)
    start = time.monotonic()
    with pytest.raises(ScriptFailed) as err:
        run("import time\n" + with_body("time.sleep(600)"), limits)
    assert "4 seconds" in err.value.message
    assert time.monotonic() - start < 30


def test_a_script_that_forks_a_sleeper_does_not_outlive_the_timeout():
    """The whole process group is killed, not only the direct child."""
    limits = SandboxSettings(enabled=True, timeout_seconds=4)
    script = "import os, subprocess, sys, time\n" + with_body(
        "p = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(600)'])\n"
        "print('GRANDCHILD', p.pid, flush=True)\n"
        "time.sleep(600)"
    )
    with pytest.raises(ScriptFailed) as err:
        run(script, limits)
    pid = int(next(line.split()[1] for line in err.value.log.splitlines() if line.startswith("GRANDCHILD")))
    for _ in range(50):
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            break
        # A killed child that its parent never reaped shows as a zombie.
        try:
            if Path(f"/proc/{pid}/stat").read_text().split()[2] == "Z":
                break
        except OSError:
            break
        time.sleep(0.1)
    else:
        pytest.fail(f"grandchild {pid} survived the timeout")


def test_a_script_that_writes_a_huge_file_is_stopped():
    limits = SandboxSettings(enabled=True, file_mb=1)
    with pytest.raises(ScriptFailed) as err:
        run(with_body("open('big.bin', 'wb').write(b'x' * (8 << 20))"), limits)
    assert "1 MB" in err.value.message


def test_an_oversized_model_is_refused():
    limits = SandboxSettings(enabled=True, output_mb=1, file_mb=16)
    with pytest.raises(ScriptFailed) as err:
        run(with_body("return 1", "return b'x' * (2 << 20)"), limits)
    assert "larger than 1 MB" in err.value.message


def test_export_must_return_bytes():
    with pytest.raises(ScriptFailed) as err:
        run(with_body("return 1", "return 'a string'"))
    assert "export_onnx must return bytes" in err.value.message


def test_the_interface_functions_are_required():
    with pytest.raises(ScriptFailed) as err:
        run("def train(df, spec):\n    return 1\n")
    assert "export_onnx" in err.value.message


def test_an_exception_in_the_script_is_reported_with_its_message():
    with pytest.raises(ScriptFailed) as err:
        run(with_body("raise ValueError('the boom we expect')"))
    assert "ValueError: the boom we expect" in err.value.message
    assert "Traceback" in err.value.log


def test_a_syntax_error_is_reported():
    with pytest.raises(ScriptFailed) as err:
        run("def train(df, spec)\n    return 1\n")
    assert "SyntaxError" in err.value.message


def test_output_is_bounded_to_the_last_bytes():
    limits = SandboxSettings(enabled=True, log_kb=4)
    result = run(with_body("print('A' * 200000)\nprint('THE END')", "return b'ok'"), limits)
    assert len(result.log.encode()) <= 4 * 1024
    assert "THE END" in result.log


def test_a_symlink_swapped_in_for_the_result_is_not_followed(tmp_path):
    secret = tmp_path / "secret.txt"
    secret.write_text("other tenant's data")
    script = "import atexit, os\n" + with_body(
        "return 1",
        "def swap():\n"
        "    for root, _, files in os.walk('..'):\n"
        "        for f in files:\n"
        "            if f == 'model.onnx':\n"
        "                p = os.path.join(root, f)\n"
        "                os.remove(p)\n"
        f"                os.symlink({str(secret)!r}, p)\n"
        "atexit.register(swap)\n"
        "return b'onnx bytes'",
    )
    with pytest.raises(ScriptFailed) as err:
        run(script)
    assert "regular file" in err.value.message
    assert "other tenant" not in err.value.message


# -- settings ------------------------------------------------------------------


def test_settings_default_to_off():
    assert SandboxSettings.from_env({}).enabled is False


def test_settings_from_env():
    s = SandboxSettings.from_env(
        {
            "HERMOD_ML_CUSTOM_SCRIPTS": "true",
            "HERMOD_ML_SANDBOX_CPU_SECONDS": "30",
            "HERMOD_ML_SANDBOX_MEMORY_MB": "4096",
            "HERMOD_ML_SANDBOX_TIMEOUT_SECONDS": "90",
            "HERMOD_ML_SANDBOX_FILE_MB": "10",
            "HERMOD_ML_SANDBOX_NPROC": "64",
            "HERMOD_ML_SANDBOX_OUTPUT_MB": "5",
            "HERMOD_ML_SANDBOX_LOG_KB": "8",
        }
    )
    assert s == SandboxSettings(enabled=True, cpu_seconds=30, memory_mb=4096, timeout_seconds=90, file_mb=10, nproc=64, output_mb=5, log_kb=8)


@pytest.mark.parametrize(
    "env",
    [
        {"HERMOD_ML_CUSTOM_SCRIPTS": "maybe"},
        {"HERMOD_ML_SANDBOX_CPU_SECONDS": "0"},
        {"HERMOD_ML_SANDBOX_MEMORY_MB": "100"},  # below what the interpreter needs
        {"HERMOD_ML_SANDBOX_NPROC": "x"},
    ],
)
def test_bad_settings_are_refused(env):
    with pytest.raises(ValueError):
        SandboxSettings.from_env(env)


def test_custom_scripts_need_a_worker_token():
    with pytest.raises(ValueError, match="HERMOD_ML_TOKEN"):
        SandboxSettings(enabled=True).check(token="")
    SandboxSettings(enabled=True).check(token="t")
    SandboxSettings(enabled=False).check(token="")


@pytest.mark.parametrize("child", ["sandbox_child.py", "onnx_child.py"])
def test_the_child_runners_do_not_import_the_worker(child):
    """The children run in a fresh interpreter with -I, so they must not import
    the hermod_ml package (which would not be on their path)."""
    source = (Path(sandbox.__file__).parent / child).read_text()
    assert "hermod_ml" not in source
    assert "from ." not in source


def test_resource_module_is_available():
    # The sandbox depends on setrlimit; a platform without it cannot run scripts.
    assert hasattr(resource, "RLIMIT_AS")


def test_the_worker_will_not_start_with_scripts_on_and_no_token(monkeypatch, tmp_path, capsys):
    from hermod_ml import __main__ as entry

    monkeypatch.setenv("HERMOD_ML_DATA_DIR", str(tmp_path))
    monkeypatch.setenv("HERMOD_ML_CUSTOM_SCRIPTS", "true")
    monkeypatch.delenv("HERMOD_ML_TOKEN", raising=False)
    assert entry.main() == 2
    assert "HERMOD_ML_TOKEN" in capsys.readouterr().err
