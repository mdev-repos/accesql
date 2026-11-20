// Package jsonout serializa datos a JSON conservando el ORDEN de columnas y
// claves. Un map de Go no puede: encoding/json ordena sus claves
// alfabéticamente. Los tipos de acá implementan json.Marshaler, de modo que
// json.Marshal, MarshalIndent y json.Encoder los usan automáticamente.
package jsonout

import (
	"bytes"
	"encoding/json"
)

// Records es un conjunto de filas con columnas ordenadas. Se serializa como
// un array de objetos: [{"col1":..., "col2":...}, ...]. Rows[i][j]
// corresponde a Columns[j].
//
// Si Columns repite un nombre (SELECT a.ID, b.ID), la clave se repite en el
// objeto: usar alias (AS) para distinguirlas.
type Records struct {
	Columns []string
	Rows    [][]any
}

// MarshalJSON implementa json.Marshaler.
func (r Records) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, row := range r.Rows {
		if i > 0 {
			buf.WriteByte(',')
		}
		fields := make(Object, len(row))
		for j, v := range row {
			fields[j] = Field{Key: r.Columns[j], Value: v}
		}
		b, err := fields.MarshalJSON()
		if err != nil {
			return nil, err
		}
		buf.Write(b)
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

// Field es un par clave/valor de un Object.
type Field struct {
	Key   string
	Value any
}

// Object es un objeto JSON con las claves en el orden dado.
type Object []Field

// MarshalJSON implementa json.Marshaler.
func (o Object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(f.Key)
		if err != nil {
			return nil, err
		}
		val, err := json.Marshal(f.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
