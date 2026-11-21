#!/usr/bin/env python3
"""Genera THIRD_PARTY_NOTICES.md: las licencias de TODO lo que termina dentro del
ejecutable (dependencias de Go y la propia biblioteca estándar), para Linux y Windows.

    python3 scripts/gen-notices.py            # escribe THIRD_PARTY_NOTICES.md
    python3 scripts/gen-notices.py --check    # solo imprime el resumen y avisa si hay copyleft

Las licencias de terceros obligan a conservar sus avisos al distribuir el programa."""
import os, re, subprocess, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LICENSE_FILE = re.compile(r"^(LICEN[CS]E|COPYING|NOTICE|UNLICENSE|PATENTS)([._-].*)?$", re.I)

def sh(*cmd, env=None):
    e = dict(os.environ, **(env or {}))
    return subprocess.run(cmd, cwd=ROOT, env=e, check=True, capture_output=True, text=True).stdout

def modules():
    mods = {}
    for goos in ("linux", "windows"):
        out = sh("go", "list", "-deps", "-f",
                 "{{if .Module}}{{.Module.Path}}\t{{.Module.Version}}\t{{.Module.Dir}}{{end}}",
                 "./cmd/accesql", env={"GOOS": goos, "CGO_ENABLED": "0"})
        for line in out.splitlines():
            path, ver, d = line.split("\t")
            if path != "accesql" and d:
                mods[path] = (ver, d)
    return dict(sorted(mods.items()))

def classify(text):
    t = text[:6000]
    if re.search(r"Mozilla Public License", t, re.I): return "MPL-2.0"
    if re.search(r"GNU AFFERO", t, re.I): return "AGPL"
    if re.search(r"GNU (LESSER|LIBRARY) GENERAL", t, re.I): return "LGPL"
    if re.search(r"GNU GENERAL PUBLIC", t, re.I): return "GPL"
    if re.search(r"Apache License", t, re.I): return "Apache-2.0"
    if re.search(r"Permission is hereby granted, free of charge", t, re.I): return "MIT"
    if re.search(r"Redistribution and use in source and binary forms", t, re.I):
        return "BSD-3-Clause" if re.search(r"Neither the name", t, re.I) else "BSD-2-Clause"
    if re.search(r"Permission to use, copy, modify, and/or distribute", t, re.I): return "ISC"
    if re.search(r"This is free and unencumbered software", t, re.I): return "Unlicense"
    return "?"

def license_files(d):
    out = []
    for name in sorted(os.listdir(d)):
        p = os.path.join(d, name)
        if os.path.isfile(p) and LICENSE_FILE.match(name):
            out.append((name, open(p, encoding="utf-8", errors="replace").read().strip()))
    return out

def main():
    check = "--check" in sys.argv
    entries = []  # (nombre, versión, tipo, [(archivo, texto)])
    goroot = sh("go", "env", "GOROOT").strip()
    entries.append(("Go (biblioteca estándar y runtime)", sh("go", "env", "GOVERSION").strip(), "BSD-3-Clause",
                    [("LICENSE", open(os.path.join(goroot, "LICENSE"), encoding="utf-8").read().strip())]))
    for path, (ver, d) in modules().items():
        files = license_files(d)
        extra = os.path.join(d, "THIRDPARTY-LICENSES.md")
        if os.path.exists(extra):
            files.append(("THIRDPARTY-LICENSES.md", open(extra, encoding="utf-8").read().strip()))
        lgpl = os.path.join(d, "internal", "cmdb", "mdbtools", "COPYING.LIB")
        if os.path.exists(lgpl):
            files.append(("internal/cmdb/mdbtools/COPYING.LIB", open(lgpl, encoding="utf-8", errors="replace").read().strip()))
        main_text = files[0][1] if files else ""
        kind = classify(main_text) if files else "SIN ARCHIVO DE LICENCIA"
        if not files:
            for rd in sorted(os.listdir(d)):
                if rd.lower().startswith("readme"):
                    m = re.search(r"^#+\s*Licen[cs]e\s*\n+\s*([A-Za-z0-9.\- ]+)", open(os.path.join(d, rd), encoding="utf-8", errors="replace").read(), re.M | re.I)
                    if m:
                        kind = m.group(1).strip() + " (declarada en el README)"
                        files = [(rd, "El módulo no incluye un archivo de licencia; su README declara: «License: %s»." % m.group(1).strip())]
                        break
        entries.append((path, ver, kind, files))

    print(f"{'módulo':58s} {'versión':14s} licencia")
    risky = []
    for name, ver, kind, files in entries:
        print(f"{name:58s} {ver:14s} {kind}")
        if kind in ("AGPL", "GPL", "LGPL", "MPL-2.0", "?", "SIN ARCHIVO DE LICENCIA"):
            risky.append((name, kind))
    if risky:
        print("\nREVISAR:", ", ".join(f"{n} ({k})" for n, k in risky))
    if check:
        return

    out = ["# Avisos de terceros", "",
           "Accesql incluye software de terceros, cada uno con su propia licencia. Estos avisos se distribuyen",
           "junto con el programa porque esas licencias lo exigen. La licencia de Accesql (ver `LICENSE`)",
           "se aplica solo al código de Accesql, no a los componentes de esta lista.", "",
           "> `mdbgo` incluye código portado de [mdbtools](https://github.com/mdbtools/mdbtools), licenciado",
           "> como **LGPL-2.0 o posterior**. El código fuente completo de Accesql está disponible en su",
           "> repositorio, de modo que cualquiera puede volver a compilar el programa con otra versión de",
           "> esos componentes.", "",
           "| Componente | Versión | Licencia |", "|---|---|---|"]
    for name, ver, kind, _ in entries:
        out.append(f"| `{name}` | {ver} | {kind} |")
    out.append("")
    for name, ver, kind, files in entries:
        out += [f"## {name} {ver}", ""]
        if kind == "MPL-2.0":
            out += [f"Código fuente de este componente (MPL-2.0): https://{name}/tree/{ver}", ""]
        for fname, text in files:
            out += [f"### {fname}", "", "```text", text, "```", ""]
        if not files:
            out += ["(el módulo no incluye un archivo de licencia)", ""]
    with open(os.path.join(ROOT, "THIRD_PARTY_NOTICES.md"), "w", encoding="utf-8") as f:
        f.write("\n".join(out))
    print("\nescrito THIRD_PARTY_NOTICES.md")

main()
