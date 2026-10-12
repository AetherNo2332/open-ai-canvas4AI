"""Select Electron runtime files from a release ZIP without executing its contents."""
import argparse
import json
import shutil
import stat
import zipfile
from pathlib import Path, PurePosixPath


def safe_member(entry):
    name = entry.filename
    path = PurePosixPath(name)
    if "\\" in name or path.is_absolute() or any(part in ("..", ".") or ":" in part for part in path.parts):
        raise ValueError(f"Unsafe archive member: {name}")
    if stat.S_ISLNK(entry.external_attr >> 16):
        raise ValueError(f"Archive symlinks are not allowed: {name}")
    return path


def prepare_runtime(source, destination):
    destination = Path(destination).resolve()
    if destination.exists():
        raise FileExistsError(f"Destination already exists: {destination}")
    with zipfile.ZipFile(source) as archive:
        paths = [(entry, safe_member(entry)) for entry in archive.infolist()]
        versions = [(entry, path) for entry, path in paths if len(path.parts) == 2 and path.name == "version"]
        if len(versions) != 1:
            raise ValueError("Expected exactly one Electron runtime root")
        version_entry, version_path = versions[0]
        root = version_path.parts[0]
        version = archive.read(version_entry).decode("utf-8").strip()
        selected = []
        for entry, path in paths:
            if entry.is_dir() or not path.parts or path.parts[0] != root:
                continue
            relative = PurePosixPath(*path.parts[1:])
            if not relative.parts or relative.parts[0] == "resources":
                continue
            if len(relative.parts) > 1 and relative.parts[0] != "locales":
                continue
            if len(relative.parts) == 1 and not (
                relative.suffix.lower() in {".exe", ".dll", ".pak", ".bin", ".dat", ".json"}
                or relative.name in {"version", "LICENSE", "LICENSES.chromium.html"}
            ):
                continue
            selected.append((entry, relative))
        executables = [str(path) for _, path in selected if path.suffix.lower() == ".exe"]
        if len(executables) != 1:
            raise ValueError("Expected one Electron executable")
        destination.mkdir(parents=True, exist_ok=False)
        for entry, path in selected:
            target = destination.joinpath(*path.parts)
            target.parent.mkdir(parents=True, exist_ok=True)
            with archive.open(entry) as reader, target.open("xb") as writer:
                shutil.copyfileobj(reader, writer)
        result = {"electronVersion": version, "executable": executables[0], "files": [str(path) for _, path in selected]}
        (destination / "runtime-manifest.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-zip", required=True)
    parser.add_argument("--destination", required=True)
    args = parser.parse_args()
    result = prepare_runtime(args.source_zip, args.destination)
    print(json.dumps({"electronVersion": result["electronVersion"], "fileCount": len(result["files"])}, ensure_ascii=False))
