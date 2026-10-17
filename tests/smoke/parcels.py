"""Representative GUI smoke parcels, newest first; no application state or keys."""
from pathlib import Path
import subprocess
import sys
import time


def samples(directory):
    directory = Path(directory)
    directory.mkdir(parents=True, exist_ok=True)
    entries = [
        ("text", "01-small-text.txt", b"A small text parcel from the smoke peer.\n"),
        ("file", "02-small-utf8.txt", "A UTF-8 text file: hello 世界 🌍\n".encode()),
        ("file", "03-small-binary.bin", bytes(range(256))),
        ("text", "04-medium-text.txt", b"Bounded preview: the complete text can still be copied or saved.\n" * 2000),
        ("text", "05-large-text.txt", b"Large text: save this parcel to read all of it.\n" * 400000),
        ("file", "06-large-binary.bin", bytes(range(256)) * (17 * 4096)),
    ]
    result = []
    for kind, name, content in entries:
        path = directory / name
        path.write_bytes(content)
        result.append((kind, path))
    return result


def seed(directory, send):
    marker = Path(directory) / "seeded"
    if marker.exists():
        return
    for kind, path in reversed(samples(directory)):
        # SFTP timestamps have whole-second precision; avoid random-ID ties.
        time.sleep(1)
        send(kind, path)
    marker.touch()


if __name__ == "__main__":
    # The caller owns the fixture and supplies its existing CLI/container command.
    directory, remote, *command = sys.argv[1:]
    def send(kind, path):
        if kind == "text":
            subprocess.run(command + ["--stdin"], input=path.read_bytes(), check=True)
        else:
            subprocess.run(command + ["--file", str(Path(remote) / path.name)], check=True)
    seed(directory, send)
