import importlib.util
from pathlib import Path
import struct
import tempfile
import unittest


spec = importlib.util.spec_from_file_location("verify_apk", Path(__file__).with_name("verify-apk.py"))
verify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verify)


def elf(bits, alignment=16384, virtual_address=0):
    data = bytearray(128)
    data[:6] = b"\x7fELF" + bytes((1 if bits == 32 else 2, 1))
    struct.pack_into("<H", data, 18, 40 if bits == 32 else 183)
    if bits == 32:
        struct.pack_into("<I", data, 28, 64)
        struct.pack_into("<HH", data, 42, 32, 1)
        struct.pack_into("<IIIIIIII", data, 64, 1, 0, virtual_address, 0, 128, 128, 5, alignment)
    else:
        struct.pack_into("<Q", data, 32, 64)
        struct.pack_into("<HH", data, 54, 56, 1)
        struct.pack_into("<IIQQQQQQ", data, 64, 1, 5, 0, virtual_address, 0, 128, 128, alignment)
    return data


WATCH_BADGING = """uses-feature: name='android.hardware.type.watch'
uses-feature-not-required: name='android.hardware.touchscreen'
uses-feature-not-required: name='android.hardware.faketouch'
"""
WATCH_XML = """  E: application
    E: meta-data (line=47)
      A: http://schemas.android.com/apk/res/android:name(0x01010003)="com.google.android.wearable.standalone" (Raw: "com.google.android.wearable.standalone")
      A: http://schemas.android.com/apk/res/android:value(0x01010024)=true
    E: activity
"""


class ElfTests(unittest.TestCase):
    def test_both_arm_formats(self):
        for bits, abi in ((32, "armeabi-v7a"), (64, "arm64-v8a")):
            with self.subTest(abi=abi):
                self.assertEqual([16384], verify.elf_load_alignments(elf(bits), f"lib/{abi}/libgojni.so"))

    def test_wrong_class_does_not_pass_as_another_abi(self):
        with self.assertRaisesRegex(AssertionError, "ELF format"):
            verify.elf_load_alignments(elf(64), "lib/armeabi-v7a/libgojni.so")

    def test_wrong_machine(self):
        data = elf(32)
        struct.pack_into("<H", data, 18, 3)
        with self.assertRaisesRegex(AssertionError, "ELF machine"):
            verify.elf_load_alignments(data, "lib/armeabi-v7a/libgojni.so")

    def test_both_formats_reject_4k_alignment(self):
        for bits, abi in ((32, "armeabi-v7a"), (64, "arm64-v8a")):
            with self.subTest(abi=abi), self.assertRaisesRegex(AssertionError, "16 KiB"):
                verify.elf_load_alignments(elf(bits, alignment=4096), f"lib/{abi}/libgojni.so")

    def test_segment_offset_must_match_virtual_address(self):
        with self.assertRaisesRegex(AssertionError, "16 KiB"):
            verify.elf_load_alignments(elf(32, virtual_address=4096), "lib/armeabi-v7a/libgojni.so")

    def test_truncated_program_headers(self):
        with self.assertRaisesRegex(AssertionError, "program headers"):
            verify.elf_load_alignments(elf(32)[:80], "lib/armeabi-v7a/libgojni.so")


def sections_elf(bits, extra_section):
    data = bytearray(1024)
    data[:128] = elf(bits)
    names = b"\0.shstrtab\0" + extra_section + b"\0"
    data.extend(names)
    if bits == 32:
        struct.pack_into("<I", data, 32, 128)
        struct.pack_into("<HHH", data, 46, 40, 3, 1)
        struct.pack_into("<IIIIIIIIII", data, 168, 1, 3, 0, 0, 1024, len(names), 0, 0, 1, 0)
        struct.pack_into("<I", data, 208, 11)
    else:
        struct.pack_into("<Q", data, 40, 128)
        struct.pack_into("<HHH", data, 58, 64, 3, 1)
        struct.pack_into("<IIQQQQIIQQ", data, 192, 1, 3, 0, 0, 1024, len(names), 0, 0, 1, 0)
        struct.pack_into("<I", data, 256, 11)
    return data


class StripTests(unittest.TestCase):
    def test_dynamic_symbols_and_runtime_metadata_are_kept(self):
        for bits in (32, 64):
            for name in (b".dynsym", b".gopclntab"):
                verify.check_stripped(sections_elf(bits, name), "fixture.so")

    def test_debug_and_static_symbols_are_rejected(self):
        for bits in (32, 64):
            for name in (b".symtab", b".debug_info", b".zdebug_abbrev", b".gnu_debugdata"):
                with self.subTest(bits=bits, name=name), self.assertRaisesRegex(AssertionError, "Unstripped"):
                    verify.check_stripped(sections_elf(bits, name), "fixture.so")

    def test_truncated_section_table(self):
        with self.assertRaisesRegex(AssertionError, "Truncated"):
            verify.check_stripped(sections_elf(64, b".dynsym")[:200], "fixture.so")


class DeliveryTests(unittest.TestCase):
    def test_checksums_only_reference_delivered_files(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            paths = [output / name for name in ("release.apk", "mapping.txt", "build-manifest.json")]
            for path in paths:
                path.write_text(path.name)
            verify.write_checksums(output, paths)
            lines = (output / "SHA256SUMS").read_text().splitlines()
            self.assertEqual([f"{verify.sha(path)}  {path.name}" for path in paths], lines)
            self.assertTrue(all("../" not in line for line in lines))

    def test_atomic_copy_replaces_file_and_leaves_no_temporary_file(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            source, target = output / "source.apk", output / "release.apk"
            source.write_bytes(b"new release")
            target.write_bytes(b"old release")
            verify.copy_artifact(source, target)
            self.assertEqual(b"new release", target.read_bytes())
            self.assertEqual([], list(output.glob(".release-*")))


class DeviceManifestTests(unittest.TestCase):
    def test_watch_required_with_optional_touch_and_standalone(self):
        self.assertEqual({"watch_required": True, "standalone": True},
                         verify.check_device_manifest("wear", WATCH_BADGING, WATCH_XML))

    def test_phone_does_not_require_a_watch(self):
        self.assertEqual({"watch_required": False, "standalone": False},
                         verify.check_device_manifest("app", "uses-feature: name='android.hardware.faketouch'", ""))
        with self.assertRaisesRegex(AssertionError, "Watch requirement"):
            verify.check_device_manifest("app", WATCH_BADGING, WATCH_XML)

    def test_optional_watch_is_not_a_watch_distribution(self):
        with self.assertRaisesRegex(AssertionError, "Watch requirement"):
            verify.check_device_manifest("wear", WATCH_BADGING.replace("uses-feature:", "uses-feature-not-required:"), WATCH_XML)

    def test_standalone_false_or_missing_is_rejected(self):
        for xml in (WATCH_XML.replace("=true", "=false"), ""):
            with self.subTest(xml=xml), self.assertRaisesRegex(AssertionError, "standalone"):
                verify.check_device_manifest("wear", WATCH_BADGING, xml)

    def test_required_phone_touch_is_rejected(self):
        with self.assertRaisesRegex(AssertionError, "phone touch"):
            verify.check_device_manifest("wear", WATCH_BADGING.replace("uses-feature-not-required:", "uses-feature:"), WATCH_XML)

    def test_old_aapt_boolean_representation(self):
        xml = WATCH_XML.replace("http://schemas.android.com/apk/res/android:", "android:").replace("=true", "=(type 0x12)0xffffffff")
        self.assertTrue(verify.check_device_manifest("wear", WATCH_BADGING, xml)["standalone"])


if __name__ == "__main__":
    unittest.main()
