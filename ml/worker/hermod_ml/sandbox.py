"""The sandbox a custom training script runs in.

A custom script is code a person uploaded, so it never runs in the worker's
own process. Each run is a fresh interpreter (sandbox_child.py) started with:

- a minimal environment: no token, no credential, nothing the worker was
  started with;
- kernel limits on CPU time, address space, file size and processes, set
  before any script code runs and impossible for the script to raise;
- a wall-clock timeout, after which its whole process group is killed;
- its own temp dir as cwd, HOME and TMPDIR, removed afterwards;
- stdin closed, and stdout/stderr captured into a log of which only the
  last `log_kb` are kept.

The dataset goes in as a parquet file and the model comes back as a file the
worker opens without following links. The worker itself is made
non-dumpable (harden_parent), so a script running as the same user cannot
read the worker's environment from /proc or attach to it.

What this module cannot do from inside a container is enforced by the
deployment: no network egress (a NetworkPolicy), a read-only root, a
non-root user, seccomp, and a pod pid limit. See docs/ml.md.
"""

from __future__ import annotations

import ctypes
import json
import logging
import os
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import time
from collections.abc import Mapping
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any

import pandas as pd

log = logging.getLogger("hermod_ml.sandbox")

CHILD = Path(__file__).with_name("sandbox_child.py")

# Must match sandbox_child.py.
PREFIX = "hermod-sandbox: "
EXIT_MEMORY = 4
EXIT_FILE_SIZE = 5

# The whole environment a script sees.
CHILD_ENV_KEYS = frozenset(
    {"PATH", "HOME", "TMPDIR", "LANG", "OMP_NUM_THREADS", "OPENBLAS_NUM_THREADS", "MKL_NUM_THREADS"}
)

# Below this the interpreter cannot even import pandas and onnx.
MIN_MEMORY_MB = 1024

_PR_GET_DUMPABLE = 3
_PR_SET_DUMPABLE = 4

_TRUE = {"1", "true", "yes", "on"}
_FALSE = {"", "0", "false", "no", "off"}


def _int(env: Mapping[str, str], key: str, default: int, minimum: int = 1) -> int:
    raw = env.get(key)
    if raw is None or raw == "":
        return default
    try:
        value = int(raw)
    except ValueError:
        raise ValueError(f"{key} must be an integer, got {raw!r}") from None
    if value < minimum:
        raise ValueError(f"{key} must be at least {minimum}, got {value}")
    return value


@dataclass(frozen=True)
class SandboxSettings:
    """Whether custom scripts may run here, and the limits each run gets."""

    enabled: bool = False
    cpu_seconds: int = 600
    memory_mb: int = 2048
    timeout_seconds: int = 900
    file_mb: int = 256
    nproc: int = 512
    output_mb: int = 100
    log_kb: int = 64
    threads: int = 1

    @classmethod
    def from_env(cls, env: Mapping[str, str] | None = None) -> "SandboxSettings":
        """Read HERMOD_ML_CUSTOM_SCRIPTS and HERMOD_ML_SANDBOX_*. Bad values raise ValueError."""
        env = os.environ if env is None else env
        flag = env.get("HERMOD_ML_CUSTOM_SCRIPTS", "").strip().lower()
        if flag not in _TRUE | _FALSE:
            raise ValueError(f"HERMOD_ML_CUSTOM_SCRIPTS must be true or false, got {flag!r}")
        settings = cls(
            enabled=flag in _TRUE,
            cpu_seconds=_int(env, "HERMOD_ML_SANDBOX_CPU_SECONDS", 600),
            memory_mb=_int(env, "HERMOD_ML_SANDBOX_MEMORY_MB", 2048, MIN_MEMORY_MB),
            timeout_seconds=_int(env, "HERMOD_ML_SANDBOX_TIMEOUT_SECONDS", 900),
            file_mb=_int(env, "HERMOD_ML_SANDBOX_FILE_MB", 256),
            nproc=_int(env, "HERMOD_ML_SANDBOX_NPROC", 512),
            output_mb=_int(env, "HERMOD_ML_SANDBOX_OUTPUT_MB", 100),
            log_kb=_int(env, "HERMOD_ML_SANDBOX_LOG_KB", 64),
            threads=_int(env, "HERMOD_ML_SANDBOX_THREADS", 1),
        )
        # The model file and the log are files the script's process writes,
        # so both have to fit under the file size limit.
        if settings.output_mb > settings.file_mb:
            raise ValueError("HERMOD_ML_SANDBOX_OUTPUT_MB cannot be larger than HERMOD_ML_SANDBOX_FILE_MB")
        if settings.log_kb > settings.file_mb * 1024:
            raise ValueError("HERMOD_ML_SANDBOX_LOG_KB cannot be larger than HERMOD_ML_SANDBOX_FILE_MB")
        return settings

    def check(self, token: str) -> None:
        """Refuse to run scripts on a worker without a token.

        Without one, a script could call the worker's own API on localhost
        and read every dataset and model it holds.
        """
        if self.enabled and not token:
            raise ValueError("HERMOD_ML_CUSTOM_SCRIPTS=true needs HERMOD_ML_TOKEN: a script could otherwise call this worker's API")


@dataclass(frozen=True)
class ScriptResult:
    onnx: bytes
    log: str


class ScriptFailed(Exception):
    """The script did not produce a model. `message` is for the person who wrote it."""

    def __init__(self, message: str, log: str) -> None:
        super().__init__(message)
        self.message = message
        self.log = log


# --------------------------------------------------------------------------
# The worker's own process
# --------------------------------------------------------------------------


def _prctl(option: int, arg: int = 0) -> int | None:
    if not sys.platform.startswith("linux"):
        return None
    libc = ctypes.CDLL(None, use_errno=True)
    return libc.prctl(option, arg, 0, 0, 0)


def harden_parent() -> None:
    """Make this process non-dumpable.

    A non-dumpable process's /proc files belong to root and it cannot be
    ptraced by its own user, so a script running as the worker's user can
    neither read the worker's environment (its token) nor attach to it.
    """
    if _prctl(_PR_SET_DUMPABLE, 0) is None:
        log.warning("cannot make the worker non-dumpable on %s; run custom scripts on Linux only", sys.platform)


def is_dumpable() -> bool | None:
    result = _prctl(_PR_GET_DUMPABLE)
    return None if result is None else result == 1


# --------------------------------------------------------------------------
# One run
# --------------------------------------------------------------------------


def _child_env(work: Path, threads: int) -> dict[str, str]:
    env = {
        "PATH": "/usr/local/bin:/usr/bin:/bin",
        "HOME": str(work),
        "TMPDIR": str(work),
        "LANG": "C.UTF-8",
        "OMP_NUM_THREADS": str(threads),
        "OPENBLAS_NUM_THREADS": str(threads),
        "MKL_NUM_THREADS": str(threads),
    }
    assert set(env) == CHILD_ENV_KEYS
    return env


def _kill_group(pgid: int) -> None:
    try:
        os.killpg(pgid, signal.SIGKILL)
    except (ProcessLookupError, PermissionError):
        pass


def _wait(proc: subprocess.Popen, timeout: float) -> bool:
    """Wait for the child to exit without reaping it. False on timeout.

    Leaving it a zombie keeps its pid, and so its process group id, from
    being reused while the group is killed.
    """
    deadline = time.monotonic() + timeout
    delay = 0.01
    while True:
        info = os.waitid(os.P_PID, proc.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
        if info is not None:
            return True
        if time.monotonic() >= deadline:
            return False
        time.sleep(delay)
        delay = min(delay * 2, 0.2)


def _tail(path: Path, limit: int) -> str:
    with open(path, "rb") as f:
        f.seek(0, os.SEEK_END)
        size = f.tell()
        f.seek(max(0, size - limit))
        data = f.read(limit)
    # A cut through a multi-byte character loses that character only.
    return data.decode("utf-8", errors="ignore")


def _verdict(text: str) -> str | None:
    for line in reversed(text.splitlines()):
        if line.startswith(PREFIX):
            return line[len(PREFIX):].strip()
    return None


def _explain(code: int, text: str, limits: SandboxSettings) -> str:
    if code == EXIT_MEMORY:
        return f"The training script ran out of memory: it may use at most {limits.memory_mb} MB."
    if code == -signal.SIGXCPU:
        return f"The training script used more than its {limits.cpu_seconds} CPU seconds and was stopped."
    if code in (EXIT_FILE_SIZE, -signal.SIGXFSZ):
        return f"The training script wrote a file larger than {limits.file_mb} MB and was stopped."
    if code == -signal.SIGKILL:
        return "The training script was killed (SIGKILL); it may have run out of memory or CPU time."
    if code < 0:
        return f"The training script was killed by {signal.Signals(-code).name}."
    verdict = _verdict(text)
    if verdict:
        return f"The training script failed: {verdict}"
    return f"The training script failed with exit code {code}."


def _read_result(workdir_fd: int, limits: SandboxSettings) -> bytes:
    """Read out/model.onnx without following links at any step.

    The script runs as the same user and can rename, link and replace
    anything under its work dir, so the result is opened through directory
    file descriptors taken before it started, and must be a regular file
    with no other name.
    """
    cap = limits.output_mb << 20
    out_fd = fd = -1
    try:
        try:
            out_fd = os.open("out", os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=workdir_fd)
            fd = os.open("model.onnx", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=out_fd)
        except FileNotFoundError:
            raise ScriptFailed("The training script finished without writing a model.", "") from None
        except OSError:
            raise ScriptFailed("The training script's model file is not a regular file.", "") from None
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode) or st.st_nlink != 1:
            raise ScriptFailed("The training script's model file is not a regular file.", "")
        if st.st_size > cap:
            raise ScriptFailed(f"The training script's model is larger than {limits.output_mb} MB.", "")
        chunks, size = [], 0
        while chunk := os.read(fd, 1 << 20):
            size += len(chunk)
            if size > cap:
                raise ScriptFailed(f"The training script's model is larger than {limits.output_mb} MB.", "")
            chunks.append(chunk)
        return b"".join(chunks)
    finally:
        for f in (fd, out_fd):
            if f >= 0:
                os.close(f)


def run_script(
    source: str,
    frame: pd.DataFrame,
    spec: dict[str, Any],
    limits: SandboxSettings,
    tmp_root: Path | None = None,
) -> ScriptResult:
    """Run `source`'s train/export_onnx on `frame` and return the ONNX bytes.

    Raises ScriptFailed, with the captured log, when the script fails, breaks
    a limit or returns something that is not a model file. The ONNX itself is
    not checked here; see artifacts.check_onnx.
    """
    workdir = Path(tempfile.mkdtemp(prefix="hermod-script-", dir=tmp_root))
    logdir = Path(tempfile.mkdtemp(prefix="hermod-script-log-", dir=tmp_root))
    workdir_fd = os.open(workdir, os.O_RDONLY | os.O_DIRECTORY)
    try:
        work = workdir / "work"
        work.mkdir()
        (workdir / "out").mkdir()
        (workdir / "script.py").write_text(source, encoding="utf-8")
        frame.to_parquet(workdir / "train.parquet", index=False)
        job_limits = asdict(limits)
        del job_limits["enabled"]
        (workdir / "job.json").write_text(json.dumps({"limits": job_limits, "spec": spec}), encoding="utf-8")

        log_path = logdir / "output.log"
        with open(log_path, "wb") as out:
            proc = subprocess.Popen(
                [sys.executable, "-I", "-B", str(CHILD), str(workdir)],
                stdin=subprocess.DEVNULL,
                stdout=out,
                stderr=subprocess.STDOUT,
                cwd=work,
                env=_child_env(work, limits.threads),
                close_fds=True,
                start_new_session=True,
            )
            try:
                finished = _wait(proc, limits.timeout_seconds)
            finally:
                # Kill the group while the leader is unreaped, which also
                # ends anything it left running.
                _kill_group(proc.pid)
                code = proc.wait()

        text = _tail(log_path, limits.log_kb * 1024)
        if not finished:
            raise ScriptFailed(
                f"The training script ran longer than {limits.timeout_seconds} seconds and was stopped.", text
            )
        if code != 0:
            raise ScriptFailed(_explain(code, text, limits), text)
        try:
            model = _read_result(workdir_fd, limits)
        except ScriptFailed as exc:
            raise ScriptFailed(exc.message, text) from None
        return ScriptResult(model, text)
    finally:
        os.close(workdir_fd)
        shutil.rmtree(workdir, ignore_errors=True)
        shutil.rmtree(logdir, ignore_errors=True)
