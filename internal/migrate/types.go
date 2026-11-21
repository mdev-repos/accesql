// Package migrate genera scripts SQL de MySQL a partir de una base Access ya
// cargada: para crear una base nueva con todo, o para agregar los datos a
// tablas que ya existen.
package migrate

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"accesql/internal/engine"
)

// mysqlType traduce el tipo de una columna de Access al tipo de MySQL con el
// que se crearía la tabla. (Access no distingue "Autonumeración" de un entero
// común por esta vía, así que ambos salen como INT.)
func mysqlType(c engine.ColumnInfo) string {
	switch c.AccessType {
	case "Boolean":
		return "TINYINT(1)"
	case "Byte":
		return "TINYINT UNSIGNED"
	case "Integer":
		return "SMALLINT"
	case "Long Integer":
		return "INT"
	case "Complex":
		return "BIGINT"
	case "Single":
		return "FLOAT"
	case "Double":
		return "DOUBLE"
	case "Currency":
		return "DECIMAL(19,4)"
	case "Numeric":
		return "DECIMAL(38," + strconv.Itoa(decimalScale(c.Type)) + ")"
	case "DateTime":
		return "DATETIME"
	case "Binary", "OLE":
		return "LONGBLOB"
	case "Replication ID":
		return "CHAR(38)"
	case "Text":
		n := c.Size
		if n <= 0 || n > 255 {
			n = 255
		}
		return "VARCHAR(" + strconv.Itoa(n) + ")"
	default: // Memo/Hyperlink y tipos desconocidos
		return "LONGTEXT"
	}
}

var decimalRe = regexp.MustCompile(`\((\d+),\s*(\d+)\)`)

// decimalScale saca los decimales de un tipo como "decimal(38,3)".
func decimalScale(sqlType string) int {
	if m := decimalRe.FindStringSubmatch(sqlType); m != nil {
		n, _ := strconv.Atoi(m[2])
		return min(n, 30)
	}
	return 4
}

// maxRowBytes es el tope de MySQL para el ancho de una fila (65.535 bytes).
// Se deja un margen para el resto de las columnas.
const maxRowBytes = 60000

// fitRowSize ajusta los tipos de una tabla para que quepa en una fila de MySQL.
// Cada VARCHAR(n) cuenta 4n+2 bytes (utf8mb4); si el total pasa el tope, se
// convierten a TEXT (que cuenta unos 10 bytes) las columnas más anchas hasta
// que entre. Devuelve true si tuvo que cambiar alguna.
func fitRowSize(types []string) (changed bool) {
	cost := func(t string) int {
		switch {
		case strings.HasPrefix(t, "VARCHAR("):
			n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(t, "VARCHAR("), ")"))
			return 4*n + 2
		case strings.HasPrefix(t, "CHAR("):
			return 4 * 38
		case strings.HasPrefix(t, "LONG"):
			return 10
		case strings.HasPrefix(t, "DECIMAL"):
			return 30
		}
		return 8
	}
	total := 0
	for _, t := range types {
		total += cost(t)
	}
	if total <= maxRowBytes {
		return false
	}
	order := make([]int, 0, len(types))
	for i, t := range types {
		if strings.HasPrefix(t, "VARCHAR(") {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return cost(types[order[a]]) > cost(types[order[b]]) })
	for _, i := range order {
		if total <= maxRowBytes {
			break
		}
		total += 10 - cost(types[i])
		types[i] = "TEXT"
		changed = true
	}
	return changed
}

// fkName arma un nombre de restricción válido (máx. 64 caracteres) a partir del
// nombre de la relación en Access.
func fkName(rel string, used map[string]bool) string {
	var b strings.Builder
	for _, r := range rel {
		if r < 128 && (r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	base := "fk_" + b.String()
	if len(base) > 60 {
		base = base[:60]
	}
	name := base
	for i := 2; used[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	used[strings.ToLower(name)] = true
	return name
}
