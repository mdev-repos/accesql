# Convertir a MySQL

Accesql genera **scripts SQL de MySQL** a partir de la base de Access, desde la interfaz web o desde el
menú de la terminal (*Convertir a MySQL*). El script se muestra en pantalla (vista previa de hasta 128 KB)
y se descarga o guarda **completo**.

## Dos modos

### 1. Crear una base de datos nueva

Genera `CREATE DATABASE`, un `CREATE TABLE` por tabla y, si se pide, los `INSERT` con los datos y las
relaciones.

| Opción | Efecto |
|---|---|
| Nombre de la base | solo letras, números, `_` o `$` (hasta 64 caracteres) |
| Incluir los datos | si se desmarca, solo se crea la estructura |
| Incluir las relaciones | crea las `FOREIGN KEY` y las claves primarias que los datos confirman |
| Borrar la base si ya existe | agrega `DROP DATABASE IF EXISTS` (¡elimina lo que haya!) |

Para ejecutarlo en un servidor MySQL:

```bash
mysql -u root -p < northwind.sql
```

### 2. Agregar datos a tablas que ya existen

Genera solo `INSERT`, para sumar los registros de Access a tablas de una base MySQL que ya tiene su
estructura (por ejemplo, una versión más moderna de la misma tabla).

| Opción | Efecto |
|---|---|
| Tablas a migrar | se marcan las que se quieren copiar |
| Tabla de destino | cada tabla de Access puede copiarse a una de MySQL con **otro nombre** (`Customers` → `CLIENTE`) |
| Columnas | si la tabla de MySQL tiene **menos columnas**, se indican cuáles tiene y solo se copian las que coinciden por nombre (sin distinguir mayúsculas). Un `INSERT` con una columna inexistente fallaría. Vacío = copiar todas |
| Si el registro ya existe | **omitirlo** (`INSERT IGNORE`), **actualizarlo** (`ON DUPLICATE KEY UPDATE`, sin tocar la clave) o **reemplazarlo** (`REPLACE`) |
| Envolver en una transacción | `START TRANSACTION … COMMIT`: permite deshacer todo si algo falla (tablas InnoDB) |

```bash
mysql -u root -p mi_base < northwind-insert.sql
```

## Cómo se arman los scripts

### Tipos (Access → MySQL)

| Access | MySQL |
|---|---|
| Sí/No | `TINYINT(1)` |
| Byte | `TINYINT UNSIGNED` |
| Entero | `SMALLINT` |
| Entero largo | `INT` |
| Complex | `BIGINT` |
| Simple | `FLOAT` |
| Doble | `DOUBLE` |
| Moneda | `DECIMAL(19,4)` |
| Numérico | `DECIMAL(38,s)` (con los decimales que muestran los datos) |
| Fecha/hora | `DATETIME` |
| Binario, OLE | `LONGBLOB` |
| Id. de réplica | `CHAR(38)` |
| Texto(n) | `VARCHAR(n)` (el largo declarado en Access) |
| Memo / Hipervínculo | `LONGTEXT` |

Una **Autonumeración** sale como `INT` común: Access no la distingue de un entero por esta vía.

Si una tabla tiene tantas columnas de texto que no entra en una fila de MySQL (65.535 bytes), las más
anchas pasan a `TEXT` y el script lo avisa.

### Claves primarias

Access no las expone por esta vía, así que se **deducen** de dos pistas: las columnas que otra tabla
referencia en una relación, o una columna llamada `ID` / `<tabla> ID`. Una pista solo se acepta si los
**datos la confirman**: sin vacíos ni repetidos, comparando como MySQL (sin distinguir mayúsculas ni
acentos). Si no, no se declara clave.

### Relaciones (`FOREIGN KEY`)

Se leen de la tabla interna `MSysRelationships` de Access. Se crean solo las relaciones para las que
Access **exigía integridad referencial** y cuyas columnas tienen el mismo tipo; el resto se avisa en el
encabezado del script (`-- AVISO:`). Respetan las cascadas (`ON DELETE CASCADE`, `ON UPDATE CASCADE`).

Se crean con las comprobaciones de claves desactivadas durante la carga (`SET FOREIGN_KEY_CHECKS = 0`),
para que datos viejos con registros huérfanos no hagan fallar el script.

### Texto y fechas

Los valores se escapan (comillas, barras, saltos de línea) para que ninguna cadena pueda romper el script.
Las fechas salen sin zona horaria, como las guarda Access. El script fuerza `utf8mb4`.

## Compatibilidad y verificación

Los scripts se ejecutaron en un servidor **MariaDB 10.11** real y se comparó fila por fila el resultado
con la imagen (cantidades, texto, números, fechas, claves y relaciones). **Todavía no se validaron contra
MySQL 8**: son dialectos muy parecidos y el script no usa nada específico de ninguno, pero conviene
probarlo antes de depender de él. Para repetir la prueba contra un servidor propio:

```bash
ACCESQL_MYSQL_ARGS="--socket=/tmp/x.sock -uroot" \
  go test ./internal/session -run RealMySQL -v
```
