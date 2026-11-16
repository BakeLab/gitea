#!/usr/bin/env python3
"""Package a prebuilt Go `gt` executable as a platform-specific Python wheel."""

import argparse
import base64
import csv
import hashlib
import io
import json
from pathlib import Path
import zipfile


def digest(data: bytes) -> str:
    return "sha256=" + base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--tag", required=True, help="PEP 425 platform tag")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()

    name = "bakelab_gt"
    dist = f"{name}-{args.version}"
    wheel_name = f"{dist}-py3-none-{args.tag}.whl"
    args.output.mkdir(parents=True, exist_ok=True)
    files: dict[str, bytes] = {
        f"{dist}.data/scripts/gt": args.binary.read_bytes(),
        f"{dist}.data/data/share/bakelab-gt/build.json": json.dumps(
            {"upstream_commit": Path("UPSTREAM_COMMIT").read_text().strip(), "build_commit": args.commit},
            sort_keys=True,
            indent=2,
        ).encode() + b"\n",
        f"{dist}.dist-info/METADATA": (
            f"Metadata-Version: 2.1\nName: BakeLab-GT\nVersion: {args.version}\n"
            "Summary: Platform-packaged Gitea command line client (Go executable)\n"
            "Requires-Python: >=3.8\n\n"
            "This wheel installs the native Go executable `gt`; it is not a Python implementation.\n"
        ).encode(),
        f"{dist}.dist-info/WHEEL": (
            "Wheel-Version: 1.0\nGenerator: BakeLab gt wheel packer\nRoot-Is-Purelib: false\n"
            f"Tag: py3-none-{args.tag}\n"
        ).encode(),
    }
    record = io.StringIO(newline="")
    writer = csv.writer(record, lineterminator="\n")
    for path, data in sorted(files.items()):
        writer.writerow((path, digest(data), str(len(data))))
    record_path = f"{dist}.dist-info/RECORD"
    writer.writerow((record_path, "", ""))
    files[record_path] = record.getvalue().encode()

    with zipfile.ZipFile(args.output / wheel_name, "w", compression=zipfile.ZIP_DEFLATED) as wheel:
        for path, data in files.items():
            info = zipfile.ZipInfo(path)
            info.external_attr = (0o100755 if path.endswith("/gt") else 0o100644) << 16
            wheel.writestr(info, data)
    print(args.output / wheel_name)


if __name__ == "__main__":
    main()
