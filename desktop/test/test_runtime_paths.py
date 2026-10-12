import importlib.util
import stat
import tempfile
import unittest
import zipfile
from pathlib import Path


SCRIPT = Path(__file__).parents[1] / "scripts" / "prepare-runtime.py"
spec = importlib.util.spec_from_file_location("prepare_runtime", SCRIPT)
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)


class RuntimeSafetyTests(unittest.TestCase):
    def make_archive(self, directory, extra=None):
        source = Path(directory) / "source.zip"
        with zipfile.ZipFile(source, "w") as archive:
            for name, body in {
                "无限画布-Windows-x64/version": "44.5.1",
                "无限画布-Windows-x64/无限画布.exe": "runtime",
                "无限画布-Windows-x64/icudtl.dat": "icu",
                "无限画布-Windows-x64/locales/zh-CN.pak": "locale",
                "无限画布-Windows-x64/resources/app/main.cjs": "old app",
                "无限画布-Windows-x64/resources/backend.exe": "old backend",
            }.items():
                archive.writestr(name, body)
            if extra:
                for entry, body in extra:
                    archive.writestr(entry, body)
        return source

    def test_selects_runtime_without_old_application(self):
        with tempfile.TemporaryDirectory(prefix="画布 runtime ") as directory:
            destination = Path(directory) / "新 运行时"
            result = runtime.prepare_runtime(self.make_archive(directory), destination)
            self.assertEqual(result["electronVersion"], "44.5.1")
            self.assertTrue((destination / "无限画布.exe").is_file())
            self.assertTrue((destination / "locales/zh-CN.pak").is_file())
            self.assertFalse((destination / "resources").exists())

    def test_rejects_traversal_before_writing(self):
        for unsafe in ["../escaped.txt", "root/../../escaped.txt", "C:/escaped.txt", "root\\..\\escaped.txt"]:
            with self.subTest(unsafe=unsafe), tempfile.TemporaryDirectory() as directory:
                destination = Path(directory) / "runtime"
                archive = self.make_archive(directory, [(unsafe, "bad")])
                with self.assertRaises(ValueError):
                    runtime.prepare_runtime(archive, destination)
                self.assertFalse(destination.exists())

    def test_rejects_archive_symlink(self):
        with tempfile.TemporaryDirectory() as directory:
            entry = zipfile.ZipInfo("无限画布-Windows-x64/locales/link")
            entry.create_system = 3
            entry.external_attr = (stat.S_IFLNK | 0o777) << 16
            archive = self.make_archive(directory, [(entry, "../outside")])
            with self.assertRaises(ValueError):
                runtime.prepare_runtime(archive, Path(directory) / "runtime")

    def test_never_overwrites_existing_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            destination = Path(directory) / "runtime"
            destination.mkdir()
            marker = destination / "keep.txt"
            marker.write_text("keep", encoding="utf-8")
            with self.assertRaises(FileExistsError):
                runtime.prepare_runtime(self.make_archive(directory), destination)
            self.assertEqual(marker.read_text(encoding="utf-8"), "keep")


if __name__ == "__main__":
    unittest.main()
