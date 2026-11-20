package engine

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
)

// Result es el resultado de una consulta: columnas en orden y filas.
type Result struct {
	Columns []string
	Rows    [][]any
}

// MarshalJSON hace que Result se serialice como un array de objetos
// conservando el ORDEN de las columnas del SELECT. Un map de Go no puede
// hacerlo (encoding/json ordena sus claves alfabéticamente), por eso armamos
// el JSON a mano. Al implementar este método, Result cumple la interfaz
// json.Marshaler y json.Marshal/MarshalIndent lo usan automáticamente.
//
// Si el SELECT repite un nombre de columna (a.ID, b.ID), la clave se repite en
// el objeto: usar alias (AS) para distinguirlas.
func (r *Result) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, row := range r.Rows {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteByte('{')
		for j, v := range row {
			if j > 0 {
				buf.WriteByte(',')
			}
			key, err := json.Marshal(r.Columns[j])
			if err != nil {
				return nil, err
			}
			val, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			buf.Write(key)
			buf.WriteByte(':')
			buf.Write(val)
		}
		buf.WriteByte('}')
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

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
