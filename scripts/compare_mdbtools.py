#!/usr/bin/env python3
"""Contrasta `accesql dump` contra `mdb-json` (mdbtools) para un archivo.

Uso: python3 -I scripts/compare_mdbtools.py bin/accesql testdb/Northwind.accdb

Herramienta de verificación, no forma parte del programa. Reglas de
comparación:
  - "columna ausente" y null se consideran iguales (mdb-json omite los nulls).
  - Los números se comparan con tolerancia (mdb-json imprime 200.0000).
  - Las filas se comparan en orden.
"""
import json
import math
import subprocess
import sys

tool, path = sys.argv[1], sys.argv[2]
FMT = "%Y-%m-%dT%H:%M:%S"


def run(*cmd):
    return subprocess.run(cmd, capture_output=True, check=True).stdout.decode("utf-8")


def clean(row):
    return {k: v for k, v in row.items() if v is not None}


def same(a, b):
    # mdb-json imprime los booleanos como 0/1; nosotros como false/true.
    if isinstance(a, bool) or isinstance(b, bool):
        return isinstance(a, (bool, int)) and isinstance(b, (bool, int)) and int(a) == int(b)
    if isinstance(a, (int, float)) and isinstance(b, (int, float)):
        return math.isclose(a, b, rel_tol=1e-6, abs_tol=1e-9)
    return a == b


tables = run("mdb-tables", "-1", path).split("\n")
tables = [t for t in tables if t]
ours_all = json.loads(run(tool, "dump", "-f", path))

total_rows = total_diffs = 0
for t in tables:
    ref = [
        clean(json.loads(line))
        for line in run("mdb-json", "-D", FMT, "-T", FMT, path, t).splitlines()
        if line.strip()
    ]
    ours = [clean(r) for r in ours_all.get(t, [])]
    diffs = []
    if len(ref) != len(ours):
        diffs.append(f"  filas: mdb-json={len(ref)} accesql={len(ours)}")
    for i, (r, o) in enumerate(zip(ref, ours)):
        for k in sorted(set(r) | set(o)):
            if k not in r or k not in o or not same(r[k], o[k]):
                diffs.append(f"  fila {i} col {k!r}: mdb-json={r.get(k, '<ausente>')!r} accesql={o.get(k, '<ausente>')!r}")
    total_rows += len(ref)
    total_diffs += len(diffs)
    print(f"{'OK  ' if not diffs else 'DIFF'} {t}: {len(ref)} filas")
    for d in diffs[:8]:
        print(d)
    if len(diffs) > 8:
        print(f"  ... y {len(diffs) - 8} diferencias más")

print(f"\nTotal: {len(tables)} tablas, {total_rows} filas, {total_diffs} diferencias")
sys.exit(1 if total_diffs else 0)
