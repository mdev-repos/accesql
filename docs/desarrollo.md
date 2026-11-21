# Desarrollo

## Compilar

Requiere **Go ≥ 1.26** (lo exige la librería `mdbgo`; con Go 1.21+ la toolchain se descarga sola).
No hace falta compilador de C: todo es Go puro (`CGO_ENABLED=0`).

```bash
# Linux
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/access-tool ./cmd/access-tool

# Windows, desde Linux
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/access-tool.exe ./cmd/access-tool
```

La versión que muestran las interfaces y los scripts se puede fijar al compilar:

```bash
go build -ldflags "-X access2mysql/internal/version.Version=0.2.0" ./cmd/access-tool
```

## Estructura del proyecto

```text
cmd/access-tool/     punto de entrada (main), sin lógica
internal/cli/        comandos cobra (raíz = menú interactivo, dump, query, serve, web)
internal/access/     lectura de Access (envuelve mdbgo): tablas, tipos y relaciones
internal/engine/     motor SQL en memoria (go-mysql-server) cargado desde Access; servidor MySQL
internal/session/    estado y operaciones compartidas por las interfaces: abrir, generar, consultar,
                     servidor, esquema, exportar, scripts, explorador de archivos
internal/export/     resultado → JSON / CSV / INSERT
internal/migrate/    base Access → script MySQL (base nueva / agregar a tablas existentes)
internal/version/    versión de la aplicación
internal/tui/app/    menú interactivo de la terminal (bubbletea), sobre la sesión
internal/web/        servidor HTTP + interfaz gráfica embebida (static/)
internal/jsonout/    JSON con orden de columnas conservado
scripts/             verificación (mdbtools, PHP) y generación de capturas
docs/                documentación
testdb/              bases de prueba locales (Northwind); NO se versiona
```

La idea central: **la interfaz web y el menú de la terminal no tocan el motor**. Las dos llaman a
`session.Session`, así que se comportan igual y comparten la base cargada. Cualquier mejora se hace una
sola vez.

## Tests

```bash
go test ./...
```

Cubre la sesión, los scripts MySQL, las exportaciones, el servidor web (token, `Host`, `Origin`,
aislamiento de archivos) y el menú de terminal (flujo completo, límites de pantalla). Las pruebas que
usan la base real se saltan solas si falta `testdb/Northwind.accdb`.

```bash
# Detector de condiciones de carrera
CGO_ENABLED=1 go test -race ./internal/session ./internal/web ./internal/tui/app

# Ejecutar los scripts generados en un MySQL/MariaDB real
ACCESQL_MYSQL_ARGS="--socket=/tmp/x.sock -uroot" go test ./internal/session -run RealMySQL -v

# Comparar `dump` contra mdbtools (requiere mdbtools y Python 3)
python3 -I scripts/compare_mdbtools.py bin/access-tool testdb/Northwind.accdb

# Probar `serve` con PHP real (con `serve` corriendo en el puerto 3307)
php scripts/php_smoke_test.php 127.0.0.1 3307 access secreta
```

## Bases de prueba

`testdb/` no se versiona (está en `.gitignore`). Para probar, descargá la base de ejemplo **Northwind**
(`.accdb` y/o `.mdb`) y dejala en `testdb/`. En uso real, cada usuario apunta a su propia base.

## Capturas del README

Las del menú de la terminal se generan desde los propios tests, con color real, y se convierten a SVG:

```bash
scripts/screenshots.sh        # escribe docs/img/tui-*.svg (requiere testdb/Northwind.accdb y Python 3)
```

Las de la interfaz web (`docs/img/gui-*.jpg`) son capturas del navegador con la base de ejemplo abierta
desde una carpeta neutra (para no mostrar rutas personales).

## Nota técnica: caché de archivos de mdbgo

`mdbgo` conserva en RAM una copia completa de cada `.accdb` que abre (hasta 512 MB) y no la libera al
cerrar. Como los datos ya se copian al motor SQL, eso sería memoria duplicada permanente (medido: 20
aperturas de un archivo de 3,7 MB dejaban ~70 MB retenidos). `internal/access/nocache.go` lo desactiva
con `//go:linkname`, porque la variable está en un paquete `internal` de `mdbgo`.

Es frágil: si una versión futura de `mdbgo` la renombra, el programa dejará de compilar (falla
ruidosamente). Lo ideal es pedir en `mdbgo` una opción pública.

## Dos trampas del motor SQL (para no volver a caer)

- `LOAD_FILE` y compañía leen `secure_file_priv` de la **sesión**, que copia el valor global cuando se
  crea. El motor recrea su sesión interna *después* de fijar la variable (`Engine.Lockdown`). Un test
  parte de la variable vacía a propósito, porque el valor global queda entre tests y puede ocultar el
  error.
- `AddEphemeralSuperUser` solo agrega un usuario que no existe; **nunca actualiza la contraseña**. Por
  eso el servidor borra su usuario antes de registrarlo y al detenerse.

## Contribuir

Los cambios son bienvenidos. Antes de enviar uno: `gofmt -l .`, `go vet ./...` y `go test ./...` deben
pasar. Si tocás algo que expone datos o abre puertos, revisá [seguridad](seguridad.md) y agregá una
prueba que falle sin el cambio.
