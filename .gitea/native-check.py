"""Run native checks in an owned job so no test descendants survive CI."""
import ctypes
import os
import subprocess
from pathlib import Path
from processes import own_process_tree

own_process_tree()
if ctypes.windll.shell32.IsUserAnAdmin():
    raise RuntimeError("Native checks must not run as administrator")
if subprocess.check_output(["whoami"], text=True).strip().lower() not in {
    "lion\\lion-gitea-1", "lion\\lion-gitea-2", "lion\\lion-gitea-3"
}:
    raise RuntimeError("Native checks require Lion's non-administrator runners")
cache = Path(os.environ["USERPROFILE"]) / "beauties-build-cache"
cache.mkdir(exist_ok=True)
os.environ["GOCACHE"] = str(cache / "go-build")
os.environ["GOMODCACHE"] = str(cache / "gomod")
os.environ["PYTHONDONTWRITEBYTECODE"] = "1"
os.environ["GOTOOLCHAIN"] = "local"
os.environ["CGO_ENABLED"] = "1"
os.environ["PATH"] = r"C:\BuildTools\winlibs-16.2.0\mingw64\bin;" + os.environ["PATH"]
os.environ["GOFLAGS"] = "-mod=readonly -buildvcs=false"

def run(*args):
    child = subprocess.Popen(args)
    print("Started PID", child.pid, flush=True)
    try:
        if child.wait(timeout=900):
            raise SystemExit("Failed: " + " ".join(args))
    finally:
        if child.poll() is None:
            subprocess.run(["taskkill", "/PID", str(child.pid), "/T", "/F"], check=True, timeout=30)
            child.wait(timeout=30)

run("go", "version")
run("go", "mod", "download")
run("go", "vet", "./...")
run("go", "test", "-race", "-p", "2", "-count=1", "-timeout=5m", "./...")
run("go", "build", "-o", "bin/brote.exe", "./cmd/brote")
run("go", "build", "-o", "bin/brote-lsp.exe", "./cmd/brote-lsp")
