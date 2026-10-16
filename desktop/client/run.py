"""Host runner: build the image, own containers, and trigger their tests/UI."""
import base64
import json
import os
from pathlib import Path
import re
import runpy
import shutil
import signal
import subprocess
import sys
import tempfile
import threading

from fixture import CLIENT, REPO, DOCKER, IMAGE, CONTROL, Fixture, client_args, command

BUILD = CLIENT / "build"


def prepare():
    if os.getuid() == 0:
        raise RuntimeError("Run GTK fixtures as a normal user so permission checks are meaningful")
    BUILD.mkdir(exist_ok=True)
    print("Preparing GTK images and binaries (reusing cached layers)...", flush=True)
    with (BUILD / "images.log").open("w") as log:
        subprocess.run(["bash", str(REPO / "tests/build-image.sh"), "gtk", "--progress=plain", "--build-arg", f"CLIENT_UID={os.getuid()}",
            "--build-arg", f"CLIENT_GID={os.getgid()}", "-t", IMAGE, "-"],
            input=(CLIENT / "Dockerfile").read_bytes(), stdout=log, stderr=log, check=True)
        subprocess.run(["bash", str(REPO / "tests/build-image.sh"), "sshd", "--progress=plain", "-t", "keyparcel-test-sshd:local", str(REPO / "tests/e2e")],
                       stdout=log, stderr=log, check=True)
    with tempfile.TemporaryDirectory(prefix="builder.", dir=BUILD) as temporary:
        directory = Path(temporary)
        name = "keyparcel-gtk-builder-" + directory.name
        try:
            subprocess.run(DOCKER + ["run", "--name", name, "--init", *client_args(directory), IMAGE,
                "bash", "-c", "bash build.sh > build/build.log 2>&1 && go test -p 1 -c -o build/widgets.test . > build/test-build.log 2>&1"], check=True)
        finally:
            command(DOCKER + ["rm", "-f", name])


def handle_control(fixture, line):
    action, args = json.loads(line[len(CONTROL):])
    if action not in ("exec", "pause", "unpause") or not isinstance(args, list) or not all(isinstance(a, str) for a in args):
        raise ValueError("Invalid owned-server test operation")
    if action != "exec" and args:
        raise ValueError("Unexpected pause/unpause arguments")
    return fixture.control(action, args)


def journey(fixture, session):
    args = DOCKER + ["exec", "-i", "--env", f"KEYPARCEL_GTK_ATTACHED={fixture.directory}",
        *(["--env", "KEYPARCEL_INSTALLED_IMAGE=" + os.environ["KEYPARCEL_INSTALLED_IMAGE"]]
          if os.environ.get("KEYPARCEL_INSTALLED_IMAGE") else []),
        fixture.client, "bash", "test.sh", str(session)]
    process = subprocess.Popen(args, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT, text=True)
    # Also bound a wedged docker exec or desktop startup, outside test.sh's timeout.
    timer = threading.Timer(660, lambda: os.kill(os.getpid(), signal.SIGTERM))
    timer.start()
    try:
        with (session / "runner.log").open("w") as log:
            for line in process.stdout:
                if line.startswith(CONTROL):
                    try:
                        reply = {"output": base64.b64encode(handle_control(fixture, line)).decode()}
                    except Exception as error:
                        reply = {"error": str(error)}
                    process.stdin.write(json.dumps(reply) + "\n")
                    process.stdin.flush()
                else:
                    print(line, end="", flush=True)
                    log.write(line)
                    log.flush()
        if process.wait():
            raise RuntimeError(f"GTK journey failed; artifacts: {session}")
    finally:
        timer.cancel()
        process.stdin.close()
        process.stdout.close()
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


def test(installed=False):
    if installed:
        if os.getuid() == 0 or not os.environ.get("KEYPARCEL_INSTALLED_IMAGE"):
            raise RuntimeError("Installed tests require a normal user and KEYPARCEL_INSTALLED_IMAGE")
        BUILD.mkdir(exist_ok=True)
    else:
        prepare()
    session = Path(tempfile.mkdtemp(prefix="session.", dir=BUILD))
    fixture = Fixture(session / "fixture")
    print(f"Container test artifacts: {session}", flush=True)
    try:
        fixture.start(fresh=True)
        journey(fixture, session)
    finally:
        try:
            if fixture.container:
                for label, args in (("server.log", ["logs", fixture.container]),
                                    ("sftp.log", ["exec", fixture.container, "cat", "/var/log/sftp.log"])):
                    result = subprocess.run(DOCKER + args, capture_output=True, timeout=10)
                    (session / label).write_bytes(result.stdout + result.stderr)
        finally:
            fixture.down()
    # Lifecycle checks use a separate fixture because they restart/reset it.
    if not installed:
        runpy.run_path(str(CLIENT / "smoke_test.py"), run_name="__main__")


def display_mounts(directory):
    display = os.environ.get("DISPLAY", "")
    if not re.fullmatch(r":\d+(\.\d+)?", display):
        raise RuntimeError("GTK smoke needs a local X11 DISPLAY (including XWayland)")
    number = display[1:].split(".")[0]
    if not Path(f"/tmp/.X11-unix/X{number}").exists():
        raise RuntimeError("The local X11 display socket is unavailable")
    args = ["--hostname", os.uname().nodename,
            "--mount", "type=bind,src=/tmp/.X11-unix,dst=/tmp/.X11-unix,readonly"]
    authority = Path(os.environ.get("XAUTHORITY") or Path.home() / ".Xauthority")
    if authority.is_file():
        shutil.copyfile(authority, directory / "xauthority")
        (directory / "xauthority").chmod(0o600)
    else:
        (directory / "xauthority").write_bytes(b"")
    return args


def check_smoke_idle(fixture, action):
    if fixture.state.exists():
        fixture.load()
        owned = fixture.check_owner(missing=action == "down")
        if fixture.client not in owned:
            return
        # Inspect only our client; container PIDs are invisible in host /proc.
        running = command(DOCKER + ["inspect", "--format", "{{.State.Running}}", fixture.client]).decode().strip() == "true"
        processes = command(DOCKER + ["top", fixture.client, "-eo", "pid,args"]).decode() if running else ""
        gui, peer = "keyparcel-gtk --config" in processes, "bash --rcfile" in processes
        if (action in ("fresh", "down") and (gui or peer)) or (action == "up" and gui) or (action == "peer" and peer):
            raise RuntimeError("Close the relevant GTK smoke window/peer shell before reopening or resetting")


def smoke(action):
    import fcntl
    BUILD.mkdir(exist_ok=True)
    state = BUILD / "smoke-container"
    with (BUILD / "smoke-container.lock").open("w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        fixture = Fixture(state)
        check_smoke_idle(fixture, action)
        if action == "down":
            fixture.down()
            print("GTK smoke containers, network and owned settings removed")
            return
        prepare()
        state.mkdir(exist_ok=True, mode=0o700)
        fixture.extra_mounts = display_mounts(state)
        fixture.start(fresh=action == "fresh")
        exchange = state / "files"
        exchange.mkdir(exist_ok=True)
        print(f"Smoke files (host and container): {exchange}", flush=True)
        if action == "peer":
            shell = state / "peer-shell.sh"
            peer = state / "peer"
            import shlex
            shell.write_text("keyparcel() { " + shlex.quote(str(fixture.binary)) + " \"$1\" --config " + shlex.quote(str(peer / "config.json")) + " --ssh-config " + shlex.quote(str(peer / "ssh_config")) + " \"${@:2}\"; }\nPS1='gtk-peer$ '\necho 'Try: keyparcel send --to desktop --text hello; keyparcel inbox'\n")
            args = DOCKER + ["exec", "-it", "--workdir", str(exchange), fixture.client, "bash", "--rcfile", str(shell), "-i"]
        else:
            print("Peer shell: make smoke-gtk-peer; reset: make smoke-gtk-down", flush=True)
            if action == "fresh":
                print(f"Settings: target inbox; identity {state / 'desktop/id_ed25519'}", flush=True)
            args = DOCKER + ["exec", "--env", "DISPLAY=" + os.environ["DISPLAY"],
                "--env", f"XAUTHORITY={state}/xauthority", "--env", "GDK_BACKEND=x11",
                "--env", "GSK_RENDERER=cairo", "--env", "GTK_USE_PORTAL=0", "--env", "GIO_USE_VFS=local",
                "--env", "GTK_A11Y=none", "--workdir", str(exchange), fixture.client,
                "dbus-run-session", "--", str(BUILD / "keyparcel-gtk"), "--config", str(state / "desktop/config.json"),
                "--ssh-config", str(state / "desktop/ssh_config")]
    # Long-lived GUI/shell must not retain the ownership lock.
    process = subprocess.Popen(args)
    try:
        if process.wait():
            raise RuntimeError("GTK smoke session failed")
    except KeyboardInterrupt:
        # Disconnecting docker exec does not signal its process inside the container.
        prefix = ("bash --rcfile " + str(shell)) if action == "peer" else (str(BUILD / "keyparcel-gtk") + " --config " + str(state / "desktop/config.json"))
        subprocess.run(DOCKER + ["exec", fixture.client, "pkill", "-TERM", "-f", "^" + re.escape(prefix) + " "],
                       capture_output=True, timeout=10)
        raise
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


def interrupted(signum, frame):
    raise KeyboardInterrupt(f"Interrupted by signal {signum}")


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, interrupted)
    action = sys.argv[1] if len(sys.argv) > 1 else "test"
    try:
        if action == "test":
            subprocess.run([sys.executable, "-m", "unittest", "-v", "runner_test"], cwd=CLIENT, check=True)
            test()
        elif action == "installed":
            test(installed=True)
        elif action == "build":
            prepare()
        elif action in ("up", "fresh", "peer", "down"):
            smoke(action)
        else:
            raise ValueError("Use test, build, up, fresh, peer, or down")
    except (RuntimeError, subprocess.CalledProcessError, KeyboardInterrupt) as error:
        raise SystemExit(str(error))
