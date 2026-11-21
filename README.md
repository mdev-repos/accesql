# access2mysql_golang

Herramienta de línea de comandos para leer bases Microsoft Access
(`.accdb` / `.mdb`) y exportarlas a JSON. Ejecutable único, sin JVM ni ODBC.
Pensada para que sistemas legacy en Access puedan alimentar a otras
aplicaciones (PHP, etc.) con JSON.

> Estado: MVP. Comandos: `dump`, `query`, `serve`, `ui` (interfaz de terminal) y
> `web` (interfaz gráfica en el navegador, ya conectada a todas las funciones).
> Ejecutado **sin argumentos** abre un menú de terminal que por ahora es solo una
> **maqueta** (aspecto y navegación, sin funcionalidad): se conectará a la misma
> capa que usa la web.

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

### Interfaz gráfica en el navegador (`web`)

```bash
access-tool web --open                 # elegís el archivo desde la página
access-tool web -f datos.accdb --open  # o lo dejás preseleccionado
```

Levanta una página local (**Accesql**: gris, azul y dorado; también funciona en
el celular) para quien prefiera no usar la terminal. Va **dentro del
ejecutable**: HTML, CSS y JavaScript sin librerías, sin CDN y sin internet.
Al iniciar imprime un enlace del estilo `http://127.0.0.1:8080/#t=<token>`;
`--open` lo abre solo. Es una sola pantalla con bloques que se habilitan en
orden: solo el primero está activo hasta generar la imagen.

1. **Base de datos:** explorador de carpetas propio (muestra solo `.accdb` y
   `.mdb`), nombre de la base MySQL (por defecto, el del archivo) y **Generar
   imagen MySQL** con barra de progreso y botón Cancelar. Se puede descargar toda
   la base como un JSON.
2. **Servidor MySQL** *(opcional)*: expone la imagen por el protocolo MySQL con
   dirección, puerto, usuario y contraseña (obligatoria), y se inicia y detiene
   desde la página. Muestra el comando `mysql` y el DSN de PHP para copiar.
3. **Consultas SQL** *(opcional)*: editor (`Ctrl+Enter` ejecuta), lista de
   tablas y resultado en tabla. Exporta lo consultado a **JSON, CSV** (separado
   por `;` y con BOM, para que Excel en español lo abra bien) o **SQL** (`INSERT`).
   Las exportaciones traen todas las filas (hasta 1.000.000), no solo las visibles.
4. **Convertir a MySQL** *(opcional)*: genera un script `.sql`.
   - *Crear una base nueva:* `CREATE DATABASE`, tablas con tipos de MySQL, datos
     y, si se pide, claves primarias y `FOREIGN KEY`.
   - *Agregar datos a tablas que ya existen:* solo `INSERT`, con otro nombre de
     tabla si hace falta, qué hacer con los registros repetidos (omitir,
     actualizar o reemplazar) y transacción opcional. Si la tabla de MySQL tiene
     menos columnas, se pegan las que tiene y solo se copian las que coinciden.
   - El script se ve en pantalla (vista previa de hasta 128 KB) y se descarga
     completo.
5. **Diagrama de relaciones** *(opcional)*: tablas, columnas y las **relaciones
   reales definidas en Access** (las lee de la tabla interna `MSysRelationships`),
   con las claves marcadas. Un clic en una tabla resalta las suyas.

Cómo se arman los scripts:

- **Claves primarias:** Access no las expone por esta vía. Se deducen de las
  columnas que otra tabla referencia en una relación, o de una columna llamada
  `ID` / `<tabla> ID`, y **solo se declaran si los datos las confirman** (sin
  vacíos ni repetidos, comparando como MySQL: sin distinguir mayúsculas ni
  acentos).
- **Claves foráneas:** solo las relaciones para las que Access exigía integridad
  referencial y cuyas columnas tienen el mismo tipo. El resto se avisa en el
  encabezado del script (`-- AVISO:`). Se crean con las comprobaciones
  desactivadas durante la carga, para que datos viejos con registros huérfanos
  no hagan fallar el script.
- **Tipos (Access → MySQL):** Booleano → `TINYINT(1)`, Byte → `TINYINT UNSIGNED`,
  Entero → `SMALLINT`, Entero largo → `INT`, Complex → `BIGINT`, Simple → `FLOAT`,
  Doble → `DOUBLE`, Moneda → `DECIMAL(19,4)`, Numérico → `DECIMAL(38,s)` (con los
  decimales que muestran los datos), Fecha/hora → `DATETIME`, Binario y OLE →
  `LONGBLOB`, Id. de réplica → `CHAR(38)`, Texto(n) → `VARCHAR(n)` (el largo
  declarado en Access) y Memo/Hipervínculo → `LONGTEXT`. Si una tabla tiene tantas
  columnas de texto que no entra en una fila de MySQL (65.535 bytes), las más
  anchas pasan a `TEXT`. Una "Autonumeración" sale como `INT` común (Access no la
  distingue por esta vía).
- **Texto:** los valores se escapan (comillas, barras, saltos de línea) y las
  fechas salen sin zona horaria.

Seguridad (una página que ejecuta SQL, explora carpetas y abre un puerto es una
superficie de ataque):

- Escucha solo en `127.0.0.1`. La imagen es de solo lectura, con las funciones
  de archivos del motor deshabilitadas (`LOAD_FILE`, `INTO OUTFILE`, `LOAD DATA`).
- Todas las rutas `/api/` exigen un **token aleatorio** de 128 bits. Va en el
  fragmento `#t=` del enlace, que el navegador no envía al servidor ni incluye
  en el `Referer`, y la página lo saca de la barra de direcciones al cargar. Sin
  el token, la API responde 401. Quien lo tiene puede explorar las carpetas del
  equipo y abrir un puerto MySQL: tratarlo como una contraseña.
- Se valida la cabecera `Host` (defensa contra *DNS rebinding*) y el `Origin`,
  y una política CSP impide cargar scripts o estilos externos.
- Los CSV neutralizan la *inyección de fórmulas*: un texto que empieza con
  `=`, `+`, `-` o `@` se exporta con una comilla delante.
- El servidor MySQL integrado exige contraseña, y al detenerlo se borra su
  usuario (reiniciarlo con otra clave no deja valer la anterior).
- `--host 0.0.0.0` expone la interfaz a la red **sin cifrado** (HTTP): usar solo
  en redes de confianza o por un túnel SSH/VPN.

Limitaciones: la pantalla muestra los números como números de JavaScript (pierden
precisión a partir de ~15 dígitos significativos; las exportaciones son exactas);
el nombre de la base MySQL se elige al generar la imagen y no se puede cambiar
después; las consultas se ejecutan de a una por vez y no se pueden cancelar; el
servidor MySQL integrado no tiene TLS; los scripts se probaron contra MariaDB 10.11
(no contra MySQL 8) y con bases chicas: el tiempo de deducir claves en tablas de
millones de filas está sin medir. La opción `--reload-every` pertenece a `serve`;
en la página, para refrescar los datos se vuelve a **Generar imagen**.

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
go test ./...                                   # sesión, scripts MySQL, exportaciones, web (token, host, origen, aislamiento) y TUI
ACCESQL_MYSQL_ARGS="--socket=/tmp/x.sock -uroot" go test ./internal/session -run RealMySQL -v   # ejecuta los scripts en un MySQL/MariaDB real
python3 -I scripts/compare_mdbtools.py bin/access-tool testdb/Northwind.accdb
php scripts/php_smoke_test.php 127.0.0.1 3307 access secreta   # con `serve` corriendo
```

## Estructura del proyecto

```
cmd/access-tool/     punto de entrada (main), sin lógica
internal/cli/        comandos cobra (root, dump, query, serve, ui, web)
internal/access/     lectura de Access (envuelve mdbgo)
internal/engine/     motor SQL en memoria (go-mysql-server) cargado desde Access
internal/session/    estado y operaciones compartidas por las interfaces (abrir, generar, consultar, servidor, esquema, exportar, scripts, explorador)
internal/export/     resultado → JSON / CSV / INSERT
internal/migrate/    base Access → script MySQL (base nueva / agregar a tablas existentes)
internal/version/    versión de la aplicación
internal/tui/        interfaz de terminal (bubbletea); app/ es el menú principal (maqueta)
internal/web/        servidor HTTP + interfaz gráfica embebida (static/)
internal/jsonout/    JSON con orden de columnas conservado
scripts/             verificación (mdbtools, PHP)
testdb/              bases de prueba locales (Northwind); NO se versiona
```
