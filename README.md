# access2mysql_golang

Herramienta de línea de comandos para leer bases Microsoft Access
(`.accdb` / `.mdb`) y exportarlas a JSON. Ejecutable único, sin JVM ni ODBC.
Pensada para que sistemas legacy en Access puedan alimentar a otras
aplicaciones (PHP, etc.) con JSON.

> Estado: MVP. Comandos: `dump`, `query`, `serve` y `ui` (interfaz de terminal).

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

### Interfaz interactiva (`ui`)

```bash
access-tool ui --file datos.accdb
```

Abre una interfaz de terminal (TUI) para no tener que tipear comandos: lista de
tablas a la izquierda, editor SQL arriba y grilla de resultados abajo.

| Tecla | Acción |
|---|---|
| `↑` `↓` + `Enter` (en Tablas) | ver las primeras 100 filas de la tabla |
| `s` (en Tablas) | ver la estructura (`DESCRIBE`) |
| `Tab` / `Shift+Tab` | cambiar de panel |
| `Ctrl+R` | ejecutar el SQL del editor |
| `←` `→` `↑` `↓` `PgUp` `PgDn` `Home` `End` (en la grilla) | recorrer el resultado |
| `Ctrl+S` | guardar el resultado como `resultado-AAAAMMDD-HHMMSS.json` |
| `Ctrl+C` | salir |

Muestra hasta 1.000 filas por consulta (avisa si hay más) para que un
`SELECT *` sobre una tabla enorme no cuelgue la pantalla. Trabaja sobre una
foto de solo lectura, igual que `query`. Una consulta en curso no se puede
cancelar. Necesita una terminal de al menos 60x16.

### Servidor MySQL (`serve`)

`serve` expone el Access como si fuera un servidor MySQL, de modo que las
aplicaciones (PHP con PDO/mysqli, etc.) se conectan con su driver de siempre.
Es de **solo lectura**: INSERT/UPDATE/DELETE devuelven un error claro.

```bash
export ACCESS_TOOL_PASSWORD='una-clave-larga'
access-tool serve --file datos.accdb --port 3306
# opciones: --host (127.0.0.1), --user (access), --database (access),
#           --reload-every 5m (recarga el archivo si cambió)
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
- Es una foto del archivo. Con `--reload-every 5m` el servidor revisa su fecha
  y tamaño cada 5 minutos y, si cambió (y lleva más de 2 s sin tocarse, por si
  Access lo está escribiendo), lo recarga. **Durante la recarga las consultas se
  pausan** (duran decenas de milisegundos con Northwind; con bases grandes será
  proporcional) y luego siguen con los datos nuevos, incluidas las sentencias
  preparadas abiertas. Para no duplicar la memoria se reemplaza tabla por tabla
  (el pico extra es el de la tabla más grande). Si el archivo está corrupto, el
  error se registra, el servidor sigue con los datos anteriores y reintenta en
  el próximo ciclo; si falla a mitad, pueden quedar unas tablas nuevas y otras
  viejas hasta el reintento. Sin `--reload-every`, hay que reiniciar para
  recargar. Se apaga limpiamente con Ctrl+C o SIGTERM.

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
- **Un solo usuario** y sin TLS.
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

## Nota técnica: caché de archivos de mdbgo

`mdbgo` conserva en RAM una copia completa de cada `.accdb` que abre (hasta
512 MB) y no la libera al cerrar. Como los datos ya se copian al motor SQL,
eso sería memoria duplicada permanente (medido: 20 aperturas de un archivo de
3,7 MB dejaban ~70 MB retenidos). `internal/access/nocache.go` lo desactiva
con `//go:linkname`, porque la variable está en un paquete `internal` de
`mdbgo`. Es frágil: si una versión futura de `mdbgo` la renombra, el programa
dejará de compilar (falla ruidosamente). Lo ideal es pedir en `mdbgo` una
opción pública.

## Tests

```bash
go test ./...                                   # TUI: teclas, resultados, geometría en varios tamaños
python3 -I scripts/compare_mdbtools.py bin/access-tool testdb/Northwind.accdb
php scripts/php_smoke_test.php 127.0.0.1 3307 access secreta   # con `serve` corriendo
```

## Estructura del proyecto

```
cmd/access-tool/     punto de entrada (main), sin lógica
internal/cli/        comandos cobra (root, dump, query, serve, ui)
internal/access/     lectura de Access (envuelve mdbgo)
internal/engine/     motor SQL en memoria (go-mysql-server) cargado desde Access
internal/tui/        interfaz de terminal (bubbletea)
internal/jsonout/    JSON con orden de columnas conservado
scripts/             verificación (mdbtools, PHP)
testdb/              bases de prueba locales (Northwind); NO se versiona
```
