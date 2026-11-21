package engine

import (
	"encoding/json"
	"time"

	"accesql/internal/jsonout"

	"github.com/shopspring/decimal"
)

// Result es el resultado de una consulta: columnas en orden y filas. Es un
// alias de jsonout.Records, que ya sabe serializarse a JSON respetando el
// orden de las columnas del SELECT.
type Result = jsonout.Records

// jsonValue adapta los tipos del motor a los mismos formatos que usa `dump`.
func jsonValue(v any) any {
	switch x := v.(type) {
	case time.Time:
		return x.Format("2006-01-02T15:04:05")
	case decimal.Decimal:
		return json.Number(x.String()) // número JSON sin comillas, decimales exactos
	}
	return v
}
