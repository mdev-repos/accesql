# access2mysql_golang

Herramienta de línea de comandos para leer bases Microsoft Access
(`.accdb` / `.mdb`) y exportarlas a JSON. Ejecutable único, sin JVM ni ODBC.
Pensada para que sistemas legacy en Access puedan alimentar a otras
aplicaciones (PHP, etc.) con JSON.

> Estado: MVP. Comandos: `dump`, `query` y `serve`. Roadmap: `ui` (TUI).

## Compilar

Requiere Go >= 1.26 (lo exige la librería `mdbgo`; con Go 1.21+ la toolchain
se descarga sola).

```bash
CGO_ENABLED=0 go build -o bin/access-tool ./cmd/access-tool
# Windows, desde Linux:
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/access-tool.exe ./cmd/access-tool
```

## Uso

```bash
# Una tabla por stdout
access-tool dump --file testdb/Northwind.accdb --table Products

# Todas las tablas: un <tabla>.json por tabla en la carpeta indicada
access-tool dump --file testdb/Northwind.accdb --out salida/
```

Los mensajes informativos van a stderr; el JSON, a stdout.

### Consultas SQL

`query` carga el archivo en un motor SQL en memoria
([go-mysql-server](https://github.com/dolthub/go-mysql-server)) y ejecuta la
consulta con dialecto MySQL: JOIN, GROUP BY, subconsultas, etc. (algo que el
SQL propio del lector de Access no soporta). Las columnas salen en el orden
del `SELECT`.

```bash
access-tool query -f testdb/Northwind.accdb -q \
  'SELECT c.Company, COUNT(*) AS pedidos
     FROM Customers c JOIN Orders o ON o.`Customer ID` = c.ID
    GROUP BY c.Company ORDER BY pedidos DESC LIMIT 3'
```

Nombres con espacios o símbolos (`Order Details`, `Country/Region`) van entre
comillas invertidas. Los datos son una foto de solo lectura: cualquier
escritura SQL afecta solo la copia en memoria, nunca el `.accdb`. En `query`
los Sí/No salen como `0`/`1` (como en MySQL); `dump` los emite `true`/`false`.

### Servidor MySQL (`serve`)

`serve` expone el Access como si fuera un servidor MySQL, de modo que las
aplicaciones (PHP con PDO/mysqli, etc.) se conectan con su driver de siempre.
Es de **solo lectura**: INSERT/UPDATE/DELETE devuelven un error claro.

```bash
export ACCESS_TOOL_PASSWORD='una-clave-larga'
access-tool serve --file datos.accdb --port 3306
# opciones: --host (127.0.0.1), --user (access), --database (access)
```

Desde PHP:

```php
$pdo = new PDO('mysql:host=127.0.0.1;port=3306;dbname=access;charset=utf8mb4',
               'access', getenv('ACCESS_TOOL_PASSWORD'));
$filas = $pdo->query('SELECT * FROM Clientes LIMIT 10')->fetchAll(PDO::FETCH_ASSOC);
echo json_encode($filas);
```

Seguridad:

- Escucha solo en `127.0.0.1` por defecto y exige usuario y contraseña (se
  pasa por la variable `ACCESS_TOOL_PASSWORD`, mejor que `--password`, que
  queda visible en la lista de procesos). Sin contraseña no arranca.
- **No hay cifrado (TLS).** Si las apps están en otra máquina, no usar
  `--host 0.0.0.0` a través de una red no confiable: conectar por un túnel SSH
  o una VPN.
- Las funciones de archivos del motor (`LOAD_FILE`, `INTO OUTFILE`,
  `LOAD DATA`) están deshabilitadas: de lo contrario, un cliente autenticado
  (o una inyección SQL en una app) podría leer o crear archivos del servidor.
- Es una foto: si el `.accdb` cambia, reiniciar el servidor para recargarlo.
  Se apaga limpiamente con Ctrl+C o SIGTERM.

Nota para PHP: igual que con un MySQL real, los `DECIMAL` (columnas Numeric y
Currency de Access) llegan a PHP como **string** (`"32.38"`), para no perder
precisión; los enteros llegan como `int` con preparadas nativas. Quien necesite
número hace el cast (`(float)`) en la aplicación.

## Add-ons posibles (no implementados)

- **Endpoint HTTP que devuelva JSON** (`GET /query?sql=...`), para aplicaciones
  que prefieran no usar un driver MySQL. Reutilizaría el mismo motor. Implica
  otra superficie de seguridad (autenticación, límites, solo lectura), por lo
  que se hará solo si hace falta.

## Conversión de tipos

| Access | JSON |
|---|---|
| Texto, Memo | string |
| Entero, Largo, Byte | número |
| Doble, Simple, Moneda | número |
| Numeric (decimal) | número, con los decimales exactos (`100.0000`) |
| Sí/No | `true` / `false` |
| Fecha/Hora | string sin zona horaria: `2006-01-15T00:00:00` |
| Nulo | `null` |

Access guarda las fechas sin zona horaria, por eso no se les agrega una.

En `query` y `serve` el texto se compara **sin distinguir mayúsculas** (como
Access y como el MySQL 8 por defecto: `utf8mb4_0900_ai_ci`), por lo que
`WHERE Nombre = 'perez'` encuentra `Pérez`... y también `Perez`, porque esa
collation ignora los acentos. Si se prefiere distinguir acentos, cambiar a
`utf8mb4_0900_as_ci` en `internal/engine/types.go`.

## Limitaciones conocidas

### Columnas OLE / binarias (imágenes, objetos incrustados)

Actualmente las columnas de tipo **OLE** (por ejemplo `Categories.Picture` y
`Employees.Photo` en `Northwind.MDB`) **no se exportan correctamente**: el
driver `mdbgo` (v0.0.12) las declara como binarias pero entrega apenas 2 bytes
sin sentido en lugar del contenido. `mdb-json` (mdbtools) sí devuelve el
contenido completo, por lo que el defecto está en `mdbgo` y no en los datos.
Las demás columnas del archivo no se ven afectadas.

De ser necesario, se puede resolver de estas formas:

- **A. Reportar el problema a `mdbgo`** y actualizar cuando se corrija. Es la
  opción más barata, pero depende de terceros.
- **B. Usar el driver CGo de `mdbgo` (`cmdb`)**, basado en el código C original
  de mdbtools, solo para leer esas columnas. Requiere un compilador C
  (`zig cc` es lo recomendado) al compilar, y el binario deja de ser Go puro.
- **C. Corregir el manejo de OLE en `mdbgo`** (fork o contribución upstream).
  Máximo control, pero es el esfuerzo mayor.

### Otras

- **Campos "Complex" de Access** (adjuntos, listas multivalor): se exporta
  solo un entero (por ejemplo `Attachments: 3`), no el contenido.
- **Sin índices ni claves en `query`:** no se cargan los índices de Access, por
  lo que los JOIN sobre tablas muy grandes pueden ser lentos.
- **Probado con** el cliente `mysql` (MariaDB), el driver Go `go-sql-driver` y
  **PHP 8.3 real** (PDO con preparadas emuladas y nativas, y mysqli):
  `scripts/php_smoke_test.php` (35 comprobaciones: tipos, acentos, solo
  lectura, autenticación, aislamiento de archivos, conexiones repetidas).
- **Un solo usuario**, sin TLS, y la base completa se recarga solo al reiniciar.
- **Memoria:** `query` y `serve` copian toda la base a RAM al arrancar y el binario pesa
  ~57 MB. Consumo y tiempos con bases grandes: sin medir.
- **Solo lectura** y como foto del archivo en el momento de la lectura.

## Verificación contra mdbtools

`scripts/compare_mdbtools.py` compara la salida de `dump` contra `mdb-json`
fila por fila (requiere `mdbtools` y Python 3):

```bash
python3 -I scripts/compare_mdbtools.py bin/access-tool testdb/Northwind.accdb
```

Resultado actual: el `.accdb` coincide en todas sus 511 filas; el `.MDB`
coincide salvo las columnas OLE descritas arriba.

## Bases de prueba

`testdb/` no se versiona (está en `.gitignore`). Para probar, descargá la base
de ejemplo Northwind (`.accdb` y/o `.mdb`) y dejala en `testdb/`. En uso real,
cada usuario apunta `--file` a su propia base Access.

## Estructura del proyecto

```
cmd/access-tool/     punto de entrada (main), sin lógica
internal/cli/        comandos cobra (root, dump, query, serve)
internal/access/     lectura de Access (envuelve mdbgo)
internal/engine/     motor SQL en memoria (go-mysql-server) cargado desde Access
internal/jsonout/    JSON con orden de columnas conservado
scripts/             verificación (mdbtools, PHP)
testdb/              bases de prueba locales (Northwind); NO se versiona
```
