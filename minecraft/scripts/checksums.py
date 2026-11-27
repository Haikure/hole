"""Write integrity manifests for generated platform resources."""
import hashlib
from pathlib import Path
import sys

root = Path(sys.argv[1])
for path in sorted(root.glob("*/*")):
    if path.is_file() and path.suffix != ".sha256":
        path.with_name(path.name + ".sha256").write_text(hashlib.sha256(path.read_bytes()).hexdigest() + "\n")
