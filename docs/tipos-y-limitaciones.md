# Tipos y limitaciones conocidas

## Conversión de tipos a JSON

Es lo que producen `dump`, las exportaciones a JSON y las consultas de la interfaz web.

| Access | JSON |
|---|---|
| Texto, Memo | string |
| Entero, Largo, Byte | número |
| Doble, Simple, Moneda | número |
| Numeric (decimal) | número, con los decimales exactos (`100.0000`) |
| Sí/No | `true` / `false` |
| Fecha/Hora | string sin zona horaria: `2006-01-15T00:00:00` |
| Nulo | `null` |

Access guarda las fechas **sin zona horaria**, por eso no se les agrega una.

Para los scripts de MySQL, la correspondencia de tipos está en [convertir-a-mysql.md](convertir-a-mysql.md#tipos-access--mysql).

## Comparación de texto

En las consultas (`query`, `serve` y las interfaces) el texto se compara **sin distinguir mayúsculas**,
como Access y como el MySQL 8 por defecto (`utf8mb4_0900_ai_ci`). Por eso `WHERE Nombre = 'perez'`
encuentra `Pérez`… y también `Perez`, porque esa collation ignora los acentos. Si se prefiere distinguir
acentos, cambiar a `utf8mb4_0900_as_ci` en `internal/engine/types.go`.

## Limitaciones conocidas

### Columnas OLE / binarias (imágenes, objetos incrustados)

Actualmente las columnas de tipo **OLE** (por ejemplo `Categories.Picture` y `Employees.Photo` en
`Northwind.MDB`) **no se exportan correctamente**: el driver `mdbgo` (v0.0.12) las declara como binarias
pero entrega apenas 2 bytes sin sentido en lugar del contenido. `mdb-json` (mdbtools) sí devuelve el
contenido completo, por lo que el defecto está en `mdbgo` y no en los datos. Las demás columnas del
archivo no se ven afectadas.

De ser necesario, se puede resolver de estas formas:

- **A. Reportar el problema a `mdbgo`** y actualizar cuando se corrija. Es la opción más barata, pero
  depende de terceros.
- **B. Usar el driver CGo de `mdbgo` (`cmdb`)**, basado en el código C original de mdbtools, solo para
  leer esas columnas. Requiere un compilador C (`zig cc` es lo recomendado) al compilar, y el binario
  deja de ser Go puro.
- **C. Corregir el manejo de OLE en `mdbgo`** (fork o contribución upstream). Máximo control, pero es el
  esfuerzo mayor.

### Otras

- **Campos «Complex» de Access** (adjuntos, listas multivalor): se exporta solo un entero (por ejemplo
  `Attachments: 3`), no el contenido.
- **Autonumeración:** sale como un entero común; Access no la distingue por esta vía.
- **Sin índices en las consultas:** no se cargan los índices de Access, por lo que los `JOIN` sobre tablas
  muy grandes pueden ser lentos. Las claves primarias de los scripts se *deducen* y se confirman con los
  datos ([detalle](convertir-a-mysql.md#claves-primarias)).
- **Memoria:** el programa copia toda la base a RAM al generar la imagen (o al arrancar `query`/`serve`).
  El binario pesa unos 60 MB. **Consumo y tiempos con bases grandes: sin medir.**
- **Deducir las claves en tablas de millones de filas** recorre cada tabla; el tiempo no está medido.
- **Un solo usuario** en el servidor MySQL y **sin TLS** (ver [seguridad](seguridad.md)).
- **El nombre de la base MySQL** se elige al generar la imagen y no se puede cambiar después.
- **Las consultas se ejecutan de a una** y no se pueden cancelar.
- **La pantalla de la interfaz web** muestra los números como números de JavaScript (pierden precisión
  a partir de ~15 dígitos significativos); las exportaciones y los scripts son exactos.
- **Solo lectura** y como foto del archivo en el momento de la lectura.
- **Windows:** el ejecutable compila para Windows, pero todavía no se probó en una máquina Windows real
  (colores de la terminal, explorador de unidades).
- **MySQL 8:** los scripts se validaron contra MariaDB 10.11, no contra MySQL 8.

## Qué se probó

- El cliente `mysql` (MariaDB), el driver Go `go-sql-driver` y **PHP 8.3 real** (PDO con preparadas
  emuladas y nativas, y mysqli): `scripts/php_smoke_test.php` hace 35 comprobaciones (tipos, acentos, solo
  lectura, autenticación, aislamiento de archivos, conexiones repetidas).
- La salida de `dump` se compara contra `mdb-json` fila por fila
  ([`scripts/compare_mdbtools.py`](../scripts/compare_mdbtools.py)): el `.accdb` de prueba coincide en
  todas sus 511 filas y el `.MDB` coincide salvo las columnas OLE descritas arriba.
