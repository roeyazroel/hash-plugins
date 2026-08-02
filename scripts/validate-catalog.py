#!/usr/bin/env python3
"""Validate the checked-in independent plugin release catalog."""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path


PLATFORMS = ("darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64")
SEMVER = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")


def field(text: str, name: str, path: Path) -> str:
    match = re.search(rf"^{re.escape(name)}\s*=\s*\"([^\"]+)\"\s*$", text, re.MULTILINE)
    if not match:
        raise ValueError(f"{path}: missing manifest {name}")
    return match.group(1)


def validate(root: Path) -> None:
    catalog_path = root / "HASH_PLUGINS.json"
    try:
        catalog = json.loads(catalog_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"{catalog_path}: cannot read catalog: {exc}") from exc
    if catalog.get("schema_version") != 2:
        raise ValueError(f"{catalog_path}: schema_version must be 2")
    entries = catalog.get("plugins")
    if not isinstance(entries, dict) or not entries:
        raise ValueError(f"{catalog_path}: plugins must be a non-empty object")

    manifests = {}
    for manifest_path in sorted((root / "plugins").glob("*/hash-plugin.toml")):
        text = manifest_path.read_text(encoding="utf-8")
        plugin_id = field(text, "id", manifest_path)
        if plugin_id in manifests:
            raise ValueError(f"duplicate manifest ID {plugin_id}")
        manifests[plugin_id] = (manifest_path, text)
    if set(entries) != set(manifests):
        raise ValueError(f"{catalog_path}: catalog IDs do not match plugin manifests")

    release_tags = {}
    for plugin_id, entry in entries.items():
        release_tag = entry.get("release_tag")
        if not isinstance(release_tag, str) or not release_tag:
            raise ValueError(f"{plugin_id}: release_tag must be a non-empty string")
        if release_tag in release_tags:
            raise ValueError(
                f"duplicate release_tag {release_tag!r} for {plugin_id} and {release_tags[release_tag]}"
            )
        release_tags[release_tag] = plugin_id

    for plugin_id, entry in entries.items():
        manifest_path, text = manifests[plugin_id]
        directory = manifest_path.parent.name
        binary = Path(field(text, "entrypoint", manifest_path)).name
        tag_prefix = directory
        version = entry.get("version")
        release_tag = entry.get("release_tag")
        if not isinstance(version, str) or not SEMVER.fullmatch(version):
            raise ValueError(f"{plugin_id}: invalid version {version!r}")
        expected_tag = f"{tag_prefix}-v{version}"
        if release_tag != expected_tag:
            raise ValueError(f"{plugin_id}: release_tag {release_tag!r}, want {expected_tag!r}")

        if field(text, "id", manifest_path) != plugin_id:
            raise ValueError(f"{manifest_path}: id does not match catalog")
        if field(text, "version", manifest_path) != version:
            raise ValueError(f"{manifest_path}: version does not match catalog {version}")

        artifacts = entry.get("artifacts")
        if not isinstance(artifacts, dict) or set(artifacts) != set(PLATFORMS):
            raise ValueError(f"{plugin_id}: artifacts must cover {list(PLATFORMS)}")
        for platform in PLATFORMS:
            os_name, arch = platform.split("/")
            expected_name = f"{binary}_{version}_{os_name}_{arch}.tar.gz"
            artifact = artifacts[platform]
            if not isinstance(artifact, dict) or artifact.get("name") != expected_name:
                raise ValueError(f"{plugin_id}: {platform} artifact does not match {expected_name}")


def main() -> int:
    root = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path(__file__).resolve().parent.parent
    try:
        validate(root)
    except (OSError, ValueError) as exc:
        print(f"catalog validation failed: {exc}", file=sys.stderr)
        return 1
    print(f"catalog validation passed: {root / 'HASH_PLUGINS.json'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
