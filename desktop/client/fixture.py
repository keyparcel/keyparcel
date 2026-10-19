"""Host-owned Docker fixture; UI tests attach without Docker access."""
import base64
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import time

REPO = Path(__file__).resolve().parents[2]
CLIENT = REPO / "desktop/client"
DOCKER = shlex.split(os.environ.get("KEYPARCEL_DOCKER", "docker"))
IMAGE = os.environ.get("KEYPARCEL_INSTALLED_IMAGE", "keyparcel-test-gtk:local")
CONTROL = "KEYPARCEL_FIXTURE "


def command(args, **kwargs):
    return subprocess.check_output([str(a) for a in args], timeout=60, **kwargs)


def mounts(directory):
    build = CLIENT / "build"
    args = ["--mount", f"type=bind,src={REPO},dst={REPO},readonly",
            "--mount", f"type=bind,src={build},dst={build}"]
    if not directory.is_relative_to(build):
        args += ["--mount", f"type=bind,src={directory},dst={directory}"]
    return args


def client_args(directory):
    home = directory / "home"
    home.mkdir(exist_ok=True, mode=0o700)
    cache = CLIENT / "build/container-cache"
    cache.mkdir(exist_ok=True)
    return ["--user", f"{os.getuid()}:{os.getgid()}", "--workdir", str(CLIENT),
            "--env", f"HOME={home}", "--env", f"GOCACHE={cache}/build",
            "--env", f"GOMODCACHE={cache}/mod", "--env", "LC_ALL=C.UTF-8",
            *mounts(directory)]


class Fixture:
    def __init__(self, directory):
        self.directory = Path(directory).resolve()
        self.state = self.directory / "fixture.json"
        self.binary = Path("/usr/bin/keyparcel") if os.environ.get("KEYPARCEL_INSTALLED_IMAGE") else CLIENT / "build/keyparcel"
        self.container = self.client = self.network = self.driver = None
        self.hostname, self.port = "inbox", "22"
        self.attached = os.environ.get("KEYPARCEL_GTK_ATTACHED") == str(self.directory)

    def load(self):
        state = json.loads(self.state.read_text())
        self.container, self.client, self.network = (state[k] for k in ("container", "client", "network"))
        self.driver = state.get("driver")

    def save(self):
        state = {"container": self.container, "client": self.client, "network": self.network}
        if self.driver:
            state["driver"] = self.driver
        self.state.write_text(json.dumps(state))

    def start(self, fresh=False):
        if self.attached:
            self.load()
            return self
        self.directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        if self.state.exists():
            self.load()
            self.check_owner()
            command(DOCKER + ["start", self.container, self.client, *([self.driver] if self.driver else [])])
        else:
            stem = f"keyparcel-gtk-{os.getpid()}-{time.time_ns()}"
            label = "dev.keyparcel.gtk-owner=" + str(self.directory)
            # Docker may create a resource before its interrupted command returns.
            self.network, self.container, self.client = stem, stem + "-server", stem + "-client"
            driver_image = os.environ.get("KEYPARCEL_GTK_DRIVER_IMAGE")
            if driver_image:
                self.driver = stem + "-driver"
            self.save()
            try:
                command(DOCKER + ["network", "create", "--internal", "--label", label, self.network])
                command(DOCKER + ["run", "-d", "--name", self.container,
                    "--label", label, "--network", stem, "--network-alias", "inbox",
                    "keyparcel-test-sshd:local", "sh", "-c",
                    "syslogd -O /var/log/sftp.log && { test -f /etc/ssh/ssh_host_ed25519_key || ssh-keygen -q -t ed25519 -N '' -f /etc/ssh/ssh_host_ed25519_key; } && exec /usr/sbin/sshd -D -e"])
                command(DOCKER + ["run", "-d", "--init", "--name", self.client,
                    "--label", label, "--network", stem, *client_args(self.directory), *getattr(self, "extra_mounts", []), IMAGE])
                if self.driver:
                    command(DOCKER + ["run", "-d", "--init", "--name", self.driver,
                        "--label", label, "--network", stem, *client_args(self.directory), driver_image])
            except BaseException:
                self.down()
                raise
        for _ in range(100):
            try:
                self.hostkey = self.exec("cat", "/etc/ssh/ssh_host_ed25519_key.pub").decode().strip()
                self.local(["ssh-keyscan", "-T", "1", "inbox"], stderr=subprocess.DEVNULL)
                break
            except subprocess.CalledProcessError:
                time.sleep(.1)
        else:
            raise RuntimeError("SSH fixture did not become ready")
        new_peer = not (self.directory / "peer/config.json").exists()
        for name in ("desktop", "peer"):
            path = self.directory / name
            path.mkdir(exist_ok=True, mode=0o700)
            identity = path / "id_ed25519"
            if not identity.exists():
                self.local(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", name, "-f", identity])
            if not (path / "ssh_config").exists():
                (path / "ssh_config").write_text(f"Host inbox\n  HostName inbox\n  Port 22\n  User parcel\n  IdentityFile {identity}\n  IdentityAgent none\n  UserKnownHostsFile {path / 'known_hosts'}\n  GlobalKnownHostsFile /dev/null\n  StrictHostKeyChecking ask\n")
            known = path / "known_hosts"
            if not os.path.lexists(known):
                with known.open("x") as output:
                    output.write(f"inbox {self.hostkey}\n")
            cfg = path / "config.json"
            if not cfg.exists() and not (path / "fresh").exists():
                self.cli(name, "setup", "--host", "inbox", "--identity", str(identity), "--default")
        authorized = "\n".join(self.public(name) for name in ("desktop", "peer")) + "\n"
        subprocess.run(DOCKER + ["exec", "-i", self.container, "sh", "-c",
            "if [ ! -f /home/parcel/.ssh/authorized_keys ]; then cat > /home/parcel/.ssh/authorized_keys; chown parcel:parcel /home/parcel/.ssh/authorized_keys; chmod 600 /home/parcel/.ssh/authorized_keys; else cat >/dev/null; fi"],
            input=authorized.encode(), check=True, timeout=10)
        if new_peer:
            cfg = self.directory / "peer/config.json"
            state = json.loads(cfg.read_text())
            state["peers"]["desktop"] = self.public("desktop")
            cfg.write_text(json.dumps(state))
        if fresh:
            (self.directory / "desktop/fresh").touch()
            (self.directory / "desktop/config.json").unlink(missing_ok=True)
            known = self.directory / "desktop/known_hosts"
            known.unlink(missing_ok=True)
            known.touch(mode=0o600, exist_ok=False)
        return self

    def check_owner(self, missing=False):
        owned = set()
        for kind, resource in (("container", self.driver), ("container", self.client), ("container", self.container), ("network", self.network)):
            if not resource:
                continue
            label = '.Labels' if kind == "network" else '.Config.Labels'
            result = subprocess.run(DOCKER + [kind, "inspect", "--format", '{{index ' + label + ' "dev.keyparcel.gtk-owner"}}', resource], capture_output=True, timeout=10)
            if missing and result.returncode and (b"No such" in result.stderr or b"not found" in result.stderr):
                continue
            if result.returncode or result.stdout.decode().strip() != str(self.directory):
                raise RuntimeError(f"Refusing to modify unverified fixture {kind}: {resource}")
            owned.add(resource)
        return owned

    def down(self):
        if self.attached or not self.state.exists():
            return
        self.load()
        owned = self.check_owner(missing=True)
        for resource in (self.driver, self.client, self.container):
            if resource in owned:
                command(DOCKER + ["rm", "-f", resource])
        if self.network in owned:
            command(DOCKER + ["network", "rm", self.network])
        # Discard ownership state only after successful removal.
        import shutil
        shutil.rmtree(self.directory)

    def local(self, args, **kwargs):
        if self.attached:
            return command(args, **kwargs)
        return command(DOCKER + ["exec", "-i", self.client, *map(str, args)], **kwargs)

    def cli(self, name, action, *args, **kwargs):
        path = self.directory / name
        command_args = [self.binary, action, "--config", path / "config.json", "--ssh-config", path / "ssh_config", *args]
        if self.driver:
            if self.attached:
                return self.control("cli", [name, action, base64.b64encode(kwargs.get("input", b"")).decode(), *args])
            return command(DOCKER + ["exec", "-i", self.driver, *map(str, command_args)], **kwargs)
        return self.local(command_args, **kwargs)

    def public(self, name):
        return (self.directory / name / "id_ed25519.pub").read_text().strip()

    def fingerprint(self, name):
        return self.local(["ssh-keygen", "-lf", self.directory / name / "id_ed25519.pub"]).decode().split()[1]

    def inbox_path(self, name):
        key = base64.b64decode(self.public(name).split()[1])
        return "/home/parcel/.local/share/keyparcel/inbox/" + hashlib.sha256(key).hexdigest()

    def control(self, action, args=()):
        if action not in ("exec", "pause", "unpause", "cli"):
            raise ValueError("Unknown fixture operation")
        if self.attached:
            # Only owned-server administration and the independent CLI driver cross stdio.
            print(CONTROL + json.dumps([action, list(map(str, args))]), flush=True)
            response = json.loads(sys.stdin.readline())
            if "error" in response:
                raise RuntimeError(response["error"])
            return base64.b64decode(response["output"])
        return command(DOCKER + [action, self.container, *map(str, args)])

    def exec(self, *args):
        return self.control("exec", args)
