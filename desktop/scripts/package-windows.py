"""Assemble a portable package from existing build artifacts; no network access."""
import argparse
import json
import shutil
from pathlib import Path


def package_windows(runtime, host, web, output, app_dir, mcp=None):
    runtime, host, web, output, app_dir = map(lambda value: Path(value).resolve(), (runtime, host, web, output, app_dir))
    if output.exists():
        raise FileExistsError(f"Output already exists: {output}")
    if not host.is_file() or not (web / "index.html").is_file():
        raise ValueError("Built host executable and Vite index.html are required")
    for name in ("package.json", "main.cjs", "preload.cjs", "runtime.cjs", "secret-store.cjs"):
        if not (app_dir / name).is_file():
            raise ValueError(f"Missing application file: {name}")
    manifest = json.loads((runtime / "runtime-manifest.json").read_text(encoding="utf-8"))
    selected = manifest["files"]
    if any(Path(name).is_absolute() or ".." in Path(name).parts or ":" in name or "\\" in name for name in selected):
        raise ValueError("Unsafe runtime manifest")
    for name in selected:
        source = runtime / name
        if not source.is_file() or source.is_symlink() or not source.resolve().is_relative_to(runtime):
            raise ValueError(f"Invalid runtime file: {name}")
    output.mkdir(parents=True, exist_ok=False)
    for name in selected:
        target = output / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(runtime / name, target)
    resources = output / "resources"
    (resources / "bin").mkdir(parents=True)
    shutil.copy2(host, resources / "bin/canvas-host.exe")
    if mcp:
        shutil.copy2(mcp, resources / "bin/canvas-mcp.exe")
    shutil.copytree(web, resources / "web")
    (resources / "app").mkdir()
    for name in ("package.json", "main.cjs", "preload.cjs", "runtime.cjs", "secret-store.cjs"):
        shutil.copy2(app_dir / name, resources / "app" / name)
    project = app_dir.parent
    shutil.copy2(project / "LICENSE", resources / "LICENSE")
    shutil.copy2(project / "docs/upstream-baseline.json", resources / "upstream-baseline.json")
    print(str(output))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    for key in ("runtime", "host", "web", "output"):
        parser.add_argument(f"--{key}", required=True)
    parser.add_argument("--app", default=str(Path(__file__).resolve().parents[1]))
    parser.add_argument("--mcp")
    args = parser.parse_args()
    package_windows(args.runtime, args.host, args.web, args.output, args.app, args.mcp)
