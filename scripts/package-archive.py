#!/usr/bin/env python3
"""Create a reproducible Hash plugin archive from one binary and manifest."""

import gzip
import io
import os
import sys
import tarfile


def add_file(archive, path, name, mode):
    info = tarfile.TarInfo(name)
    info.size = os.path.getsize(path)
    info.mode = mode
    info.mtime = 0
    info.uid = 0
    info.gid = 0
    info.uname = ""
    info.gname = ""
    with open(path, "rb") as source:
        archive.addfile(info, source)


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: package-archive.py <binary> <manifest> <archive>")
    binary, manifest, output = sys.argv[1:]
    with open(output, "wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
            with tarfile.open(mode="w", fileobj=compressed, format=tarfile.PAX_FORMAT) as archive:
                add_file(archive, binary, os.path.basename(binary), 0o755)
                add_file(archive, manifest, "hash-plugin.toml", 0o644)


if __name__ == "__main__":
    main()
