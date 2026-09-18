#!/usr/bin/env python3
"""Verify the installed toolchain's APK and write a reproducible delivery manifest.

No SDK/dependency installation, signing-key generation or device connection.
"""
import argparse
import base64
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import sys
import tempfile
from datetime import datetime, timezone
import xml.etree.ElementTree as ET
import zipfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
from build_meta import core_version, source_commit

APK_ABIS = {"app": ("arm64-v8a",), "wear": ("armeabi-v7a", "arm64-v8a")}
CORE_ABIS = {"armeabi-v7a", "arm64-v8a"}


def run(*args):
    return subprocess.check_output([str(arg) for arg in args], stderr=subprocess.STDOUT).decode().strip()


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def cert(apksigner, apk):
    output = run(apksigner, "verify", "--verbose", "--print-certs", apk)
    assert "Verified using v2 scheme (APK Signature Scheme v2): true" in output, "APK v2 signature missing"
    matches = re.findall(r"Signer #\d+ certificate SHA-256 digest: ([0-9a-f]+)", output)
    assert len(matches) == 1, "Expected one APK signer"
    return matches[0]


def elf_load_alignments(data, name):
    """Check ABI/class/machine and every LOAD segment for both ARM ELF formats."""
    abi = name.split("/")[1]
    elf_class, machine = {"armeabi-v7a": (1, 40), "arm64-v8a": (2, 183)}[abi]
    assert len(data) >= 64 and data[:4] == b"\x7fELF" and data[4] == elf_class and data[5] == 1, f"Unexpected ELF format: {name}"
    assert struct.unpack_from("<H", data, 18)[0] == machine, f"ELF machine does not match ABI: {name}"
    if elf_class == 1:
        offset = struct.unpack_from("<I", data, 28)[0]
        size, count = struct.unpack_from("<HH", data, 42)
        header_format = "<IIIIIIII"
    else:
        offset = struct.unpack_from("<Q", data, 32)[0]
        size, count = struct.unpack_from("<HH", data, 54)
        header_format = "<IIQQQQQQ"
    assert size >= struct.calcsize(header_format) and offset + size * count <= len(data), f"Invalid ELF program headers: {name}"
    alignments = []
    for index in range(count):
        header = struct.unpack_from(header_format, data, offset + index * size)
        if elf_class == 1:
            p_type, p_offset, p_vaddr, _, _, _, _, p_align = header
        else:
            p_type, _, p_offset, p_vaddr, _, _, _, p_align = header
        if p_type == 1:
            assert p_align >= 16384 and (p_vaddr - p_offset) % 16384 == 0, f"16 KiB ELF alignment failed: {name}"
            alignments.append(p_align)
    assert alignments, f"No LOAD segments: {name}"
    return alignments


def check_stripped(data, name):
    """Dynamic symbols and Go's runtime tables remain; debug/static symbols do not."""
    assert len(data) >= 64 and data[:4] == b"\x7fELF", f"Invalid ELF: {name}"
    if data[4] == 1:
        offset = struct.unpack_from("<I", data, 32)[0]
        size, count, strings_index = struct.unpack_from("<HHH", data, 46)
        section_format = "<IIIIIIIIII"
    else:
        assert data[4] == 2, f"Invalid ELF class: {name}"
        offset = struct.unpack_from("<Q", data, 40)[0]
        size, count, strings_index = struct.unpack_from("<HHH", data, 58)
        section_format = "<IIQQQQIIQQ"
    if offset == 0 and count == 0:
        return
    assert count > 0 and size >= struct.calcsize(section_format), f"Invalid ELF sections: {name}"
    assert strings_index < count and offset + size * count <= len(data), f"Truncated ELF sections: {name}"
    strings = struct.unpack_from(section_format, data, offset + size * strings_index)
    assert strings[4] + strings[5] <= len(data), f"Truncated ELF string table: {name}"
    names = data[strings[4]:strings[4] + strings[5]]
    for index in range(count):
        name_offset = struct.unpack_from("<I", data, offset + size * index)[0]
        assert name_offset < len(names), f"Invalid ELF section name: {name}"
        section_name = names[name_offset:].split(b"\0", 1)[0]
        assert section_name not in (b".symtab", b".gnu_debugdata") and not section_name.startswith((b".debug", b".zdebug")), f"Unstripped ELF section {section_name!r}: {name}"


def copy_artifact(source, destination):
    if source.resolve() == destination.resolve():
        return
    with tempfile.NamedTemporaryFile(dir=destination.parent, prefix=".release-", delete=False) as temporary:
        path = Path(temporary.name)
    try:
        shutil.copy2(source, path)
        path.replace(destination)
    finally:
        path.unlink(missing_ok=True)


def write_checksums(output, artifacts):
    # A release directory should be verifiable without the source checkout/AAR.
    assert all(path.parent.resolve() == output.resolve() for path in artifacts)
    (output / "SHA256SUMS").write_text("".join(f"{sha(path)}  {path.name}\n" for path in artifacts))


def check_device_manifest(module, badging, xmltree):
    required = set(re.findall(r"^\s*uses-feature: name='([^']+)'", badging, re.M))
    watch = "android.hardware.type.watch" in required
    assert watch == (module == "wear"), "Watch requirement does not match the selected app module"
    if module == "wear":
        assert not required.intersection({"android.hardware.touchscreen", "android.hardware.faketouch"}), "Wear APK must not require phone touch features"
        metadata = re.findall(r"E: meta-data[^\n]*\n((?:\s+A:[^\n]*(?:\n|$))+)", xmltree)
        standalone = any('="com.google.android.wearable.standalone"' in entry and
                         re.search(r"A: (?:android:|http://schemas\.android\.com/apk/res/android:)value[^\n]*=(?:true|\(type 0x12\)0xffffffff)\s*(?:\n|$)", entry) for entry in metadata)
        assert standalone, "Wear APK must declare standalone=true"
    return dict(watch_required=watch, standalone=module == "wear")


def test_counts(variant, app_module="app"):
    result = {}
    for module in (app_module, "corebridge"):
        totals = dict(tests=0, failures=0, errors=0, skipped=0)
        reports = sorted((ROOT / f"android/{module}/build/test-results/test{variant.capitalize()}UnitTest").glob("TEST-*.xml"))
        for report in reports:
            suite = ET.parse(report).getroot()
            for field in totals:
                totals[field] += int(suite.get(field, "0"))
        assert totals["tests"] > 0 and totals["failures"] == totals["errors"] == 0, f"Failed or missing {module} test reports: {totals}"
        result[module] = totals
    return result


def main():
    if not __debug__:
        raise RuntimeError("Run APK verification without Python optimization so validation checks remain enabled")
    parser = argparse.ArgumentParser()
    parser.add_argument("apk", type=Path)
    parser.add_argument("--output", type=Path, help="Defaults to dist/android for phone, dist/wear for watch")
    parser.add_argument("--module", choices=tuple(APK_ABIS), default="app", help="APK application module, not a build flavor")
    parser.add_argument("--variant", choices=("debug", "release"), default="release")
    parser.add_argument("--test-variant", choices=("debug", "release"), default="debug", help="Variant used for host unit/UI tests; independent of APK optimization")
    parser.add_argument("--check-reports", action="store_true", help="Additionally require passing unit-test and lint reports; ordinary builds do not claim to run these checks")
    parser.add_argument("--previous", type=Path, help="Optionally verify upgrade identity against a retained previous APK")
    parser.add_argument("--debug-keystore", type=Path, help="Verify the chosen existing debug identity; never generate a key")
    args = parser.parse_args()
    apk = args.apk.resolve()
    sdk = Path(os.environ.get("ANDROID_HOME", Path.home() / ".local/share/hole-android/android-sdk"))
    build_tools = sdk / "build-tools/36.0.0"
    signer = build_tools / "apksigner"
    certificate = cert(signer, apk)
    if args.debug_keystore:
        pem = run("keytool", "-exportcert", "-rfc", "-keystore", args.debug_keystore, "-storepass", "android", "-alias", "androiddebugkey")
        encoded = re.search(r"-----BEGIN CERTIFICATE-----\s*(.*?)\s*-----END CERTIFICATE-----", pem, re.S).group(1)
        expected = hashlib.sha256(base64.b64decode(encoded)).hexdigest()
        assert certificate == expected, "APK does not use the existing selected debug key"
    signing_keys = ("HOLE_SIGNING_STORE_FILE", "HOLE_SIGNING_STORE_PASSWORD", "HOLE_SIGNING_KEY_ALIAS")
    signing_checked = all(os.environ.get(key) for key in signing_keys)
    if signing_checked:
        pem = run("keytool", "-exportcert", "-rfc", "-keystore", os.environ[signing_keys[0]],
                  "-storepass:env", signing_keys[1], "-alias", os.environ[signing_keys[2]])
        encoded = re.search(r"-----BEGIN CERTIFICATE-----\s*(.*?)\s*-----END CERTIFICATE-----", pem, re.S).group(1)
        assert certificate == hashlib.sha256(base64.b64decode(encoded)).hexdigest(), "APK signing identity differs from the configured key"
    run(build_tools / "zipalign", "-c", "-P", "16", "-v", "4", apk)
    badging = run(build_tools / "aapt2", "dump", "badging", apk)
    device_manifest = check_device_manifest(args.module, badging,
        run(build_tools / "aapt2", "dump", "xmltree", "--file", "AndroidManifest.xml", apk))
    expected_abis = set(APK_ABIS[args.module])
    debuggable = "application-debuggable" in badging
    if args.variant == "release":
        assert not debuggable, "Release APK must not be debuggable"
    package, version_code, version_name = re.search(r"package: name='([^']+)' versionCode='(\d+)' versionName='([^']+)'", badging).groups()
    assert package == "dev.hole.app" and int(version_code) > 0, "Unexpected package/version"
    assert re.search(r"^(?:sdkVersion|minSdkVersion):'26'$", badging, re.M) and "targetSdkVersion:'36'" in badging
    if args.previous:
        assert certificate == cert(signer, args.previous), "Previous APK signing certificate differs"
        previous = run(build_tools / "aapt2", "dump", "badging", args.previous)
        previous_package, previous_code = re.search(r"package: name='([^']+)' versionCode='(\d+)'", previous).groups()
        assert package == previous_package and int(version_code) > int(previous_code)
    expected_core = core_version()
    libraries = []
    dex_files = []
    with zipfile.ZipFile(apk) as archive:
        for name in archive.namelist():
            if re.fullmatch(r"classes\d*\.dex", name):
                dex = archive.read(name)
                assert dex[:4] == b"dex\n", f"Unexpected DEX format: {name}"
                dex_files.append(dict(path=name, bytes=len(dex), classes=struct.unpack_from("<I", dex, 96)[0]))
            if not (name.startswith("lib/") and name.endswith(".so")):
                continue
            data = archive.read(name)
            assert name.split("/")[1] in expected_abis, f"Unexpected APK ABI: {name}"
            alignments = elf_load_alignments(data, name)
            if args.variant == "release":
                check_stripped(data, name)
            if name.endswith("/libgojni.so"):
                assert expected_core.encode() in data, f"AAR/APK Core is stale: expected {expected_core}"
            libraries.append(dict(path=name, load_alignments=alignments))
    assert {item["path"].split("/")[1] for item in libraries} == expected_abis, "APK ABI set differs from the selected module"
    assert {item["path"].split("/")[1] for item in libraries if item["path"].endswith("/libgojni.so")} == expected_abis, "Go JNI core missing for an APK ABI"
    reports, issues = None, None
    if args.check_reports:
        reports = test_counts(args.test_variant, args.module)
        lint = ROOT / f"android/{args.module}/build/reports/lint-results-{args.variant}.xml"
        issues = ET.parse(lint).getroot().findall("issue")
        assert not [i for i in issues if i.get("severity") in ("Error", "Fatal")], "Android lint has errors"
    output = (args.output or ROOT / ("dist/wear" if args.module == "wear" else "dist/android")).resolve()
    output.mkdir(parents=True, exist_ok=True)
    stem = f"hole-{version_name}{'-wear' if args.module == 'wear' else ''}-{args.variant}"
    delivered = output / f"{stem}.apk"
    optimization = dict(debuggable=debuggable, dex_files=dex_files)
    mapping_output = None
    if args.variant == "release":
        mapping = ROOT / f"android/{args.module}/build/outputs/mapping/release/mapping.txt"
        configuration = mapping.with_name("configuration.txt")
        assert mapping.is_file() and configuration.is_file(), "Release R8 outputs missing"
        mapping_text = mapping.read_text()
        names = dict(re.findall(r"^([^ #\s][^\s]*) -> ([^\s]+):$", mapping_text, re.M))
        for component in ("MainActivity", "EngineService", "ResumeReceiver"):
            assert f"dev.hole.app.{component}" in names, f"APK is missing shared host code: {component}"
        with zipfile.ZipFile(ROOT / "android/corebridge/libs/holecore.aar") as aar:
            assert {name.split("/")[1] for name in aar.namelist() if name.startswith("jni/") and name.endswith(".so")} == CORE_ABIS, "Shared AAR must contain ARM32 and ARM64"
            assert {name.split("/")[1] for name in aar.namelist() if name.startswith("jni/") and name.endswith("/libgojni.so")} == CORE_ABIS, "Shared AAR is missing a Go JNI ABI"
            for name in aar.namelist():
                if name.startswith("jni/") and name.endswith(".so"):
                    elf_load_alignments(aar.read(name), name)
            with zipfile.ZipFile(io.BytesIO(aar.read("classes.jar"))) as classes:
                bindings = [name[:-6].replace("/", ".") for name in classes.namelist() if name.endswith(".class")]
        assert bindings and all(names.get(name) == name for name in bindings), "R8 removed or renamed a JNI binding class"
        renamed = sum(original != renamed for original, renamed in names.items())
        assert renamed > 0, "Release class shrinking/obfuscation was not applied"
        config = configuration.read_text()
        assert not re.search(r"^-(dontoptimize|dontshrink|dontobfuscate)\s*$", config, re.M), "Release optimizer disabled"
        mapping_output = output / f"{stem}-mapping.txt"
        optimization.update(r8=True, native_stripped=True, mapping=mapping_output.name, mapping_sha256=sha(mapping),
                            renamed_classes=renamed, jni_binding_classes_kept=len(bindings))
        resources = mapping.with_name("resources.txt")
        assert resources.is_file() and resources.stat().st_size > 0, "Release resource shrinking report missing"
        optimization.update(resource_shrinking=True, resource_report_sha256=sha(resources))
    # Publish only after all requested checks have succeeded.
    copy_artifact(apk, delivered)
    if mapping_output is not None:
        copy_artifact(mapping, mapping_output)
    manifest = dict(
        generated_at=datetime.now(timezone.utc).isoformat(), package=package, module=args.module,
        version_name=version_name, version_code=int(version_code), variant=args.variant, apk=delivered.name,
        apk_sha256=sha(delivered), signer_sha256=certificate,
        apk_bytes=delivered.stat().st_size, abis=list(APK_ABIS[args.module]), device_manifest=device_manifest, optimization=optimization,
        core_version=expected_core, aar_sha256=sha(ROOT / "android/corebridge/libs/holecore.aar"),
        default_stun="stun:stun.cloudflare.com:3478",
        protocols=dict(ice="ice-quic-mux-v1", alpn="hole-ice-mux-v1", application_session=2, legacy="legacy-ipv6-quic-v2"),
        worker_sources={name: sha(ROOT / name) for name in ("worker.js", "worker_ice.mjs", "wrangler.toml") if (ROOT / name).is_file()},
        source_commit=source_commit(), source_state="worktree",
        tools=dict(go=run("go", "version"), javac=run("javac", "-version"), gradle="9.4.1", agp="9.2.1", kotlin="2.4.0", sdk_compile=37, sdk_target=36, sdk_min=26, ndk="28.2.13676358", build_tools="36.0.0"),
        checks=dict(apk_v2_signature=True, configured_signing_identity=signing_checked, existing_debug_identity=bool(args.debug_keystore), zip_alignment_16k=True,
                    elf_libraries=libraries, reports_checked=args.check_reports, unit_tests=reports,
                    unit_test_variant=args.test_variant if args.check_reports else None,
                    lint_errors=0 if args.check_reports else None,
                    lint_warnings=sum(i.get("severity") == "Warning" for i in issues) if args.check_reports else None),
        device_validation="Not run: device installation and runtime validation were not performed. Physical switching, idle/background endurance and installed upgrade remain device checks.",
    )
    if args.previous:
        previous_bytes = args.previous.stat().st_size
        manifest["size_comparison"] = dict(previous_apk=args.previous.name, previous_bytes=previous_bytes,
                                           reduction_percent=round(100*(1-manifest["apk_bytes"]/previous_bytes), 2))
    manifest_path = output / "build-manifest.json"
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    write_checksums(output, [delivered, *([mapping_output] if mapping_output else []), manifest_path])
    print(f"APK: {delivered}\nSigner SHA-256: {certificate}\nCore: {expected_core}\nManifest: {output / 'build-manifest.json'}")


if __name__ == "__main__":
    main()
