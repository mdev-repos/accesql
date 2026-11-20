package engine

import (
	"fmt"
	"time"

	"access2mysql/internal/access"

	"github.com/dolthub/go-mysql-server/sql"
	gtypes "github.com/dolthub/go-mysql-server/sql/types"
)

// sqlType traduce el tipo de una columna de Access al tipo SQL equivalente.
// Recibe la tabla completa porque para Numeric hace falta mirar los datos:
// el driver no informa precisión ni escala.
func sqlType(t *access.Table, col int) sql.Type {
	switch t.Columns[col].Type {
	case "Boolean":
		return gtypes.Int8 // como MySQL: BOOLEAN es TINYINT, o sea 0/1
	case "Byte":
		return gtypes.Uint8
	case "Integer":
		return gtypes.Int16
	case "Long Integer":
		return gtypes.Int32
	case "Complex":
		return gtypes.Int64
	case "Single":
		return gtypes.Float32
	case "Double":
		return gtypes.Float64
	case "Currency":
		return gtypes.MustCreateDecimalType(19, 4)
	case "Numeric":
		return gtypes.MustCreateDecimalType(38, numericScale(t, col))
	case "DateTime":
		return gtypes.Datetime
	case "Binary", "OLE":
		return gtypes.LongBlob
	default: // Text, Memo/Hyperlink, Replication ID y tipos desconocidos
		return textType
	}
}

// textType es el tipo de las columnas de texto. La collation decide cómo se
// comparan y ordenan: Access NO distingue mayúsculas de minúsculas, y
// utf8mb4_0900_ai_ci (la de MySQL 8 por defecto) tampoco, así que 'abc' = 'ABC'
// funciona como las apps esperan. Además ignora acentos ('é' = 'e'); si se
// prefiere distinguirlos, usar sql.Collation_utf8mb4_0900_as_ci.
var textType = gtypes.CreateLongText(sql.Collation_utf8mb4_0900_ai_ci)

// numericScale calcula cuántos decimales usar para una columna Numeric: el
// máximo que aparece en los datos (tope 30, el máximo de MySQL).
func numericScale(t *access.Table, col int) uint8 {
	var scale int
	for _, row := range t.Rows {
		s, ok := row[col].(string)
		if !ok {
			continue
		}
		for i := len(s) - 1; i >= 0; i-- {
			if s[i] == '.' {
				if n := len(s) - i - 1; n > scale {
					scale = n
				}
				break
			}
		}
	}
	return uint8(min(scale, 30))
}

// toSQLValue adapta un valor crudo de Access al que espera el tipo SQL.
func toSQLValue(ctx *sql.Context, typ sql.Type, col access.Column, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case bool:
		if x {
			return int8(1), nil
		}
		return int8(0), nil
	case time.Time:
		// Access guarda fechas sin zona; el driver las trae en hora local.
		// Conservamos la hora "de reloj" y la fijamos en UTC para que el
		// resultado no dependa de la zona de la máquina.
		return time.Date(x.Year(), x.Month(), x.Day(), x.Hour(), x.Minute(), x.Second(), x.Nanosecond(), time.UTC), nil
	}
	converted, _, err := typ.Convert(ctx, v)
	if err != nil {
		return nil, fmt.Errorf("columna %q (%s): valor %v: %w", col.Name, col.Type, v, err)
	}
	return converted, nil
}
