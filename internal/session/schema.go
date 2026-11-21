package session

import (
	"context"
	"strings"

	"access2mysql/internal/access"
	"access2mysql/internal/migrate"
)

// SchemaColumn es una columna del diagrama, con sus marcas de clave.
type SchemaColumn struct {
	Name       string `json:"name"`
	Type       string `json:"type"`       // tipo SQL en la imagen
	AccessType string `json:"accessType"` // tipo original en Access
	PK         bool   `json:"pk"`         // parte de la clave primaria (deducida y verificada con los datos)
	FK         bool   `json:"fk"`         // participa de una relación como clave foránea
}

// SchemaTable es una tabla del diagrama.
type SchemaTable struct {
	Name    string         `json:"name"`
	Rows    int64          `json:"rows"`
	Columns []SchemaColumn `json:"columns"`
}

// Schema es la estructura completa: tablas, columnas y las relaciones reales
// definidas en Access. Es lo que dibujan los diagramas de ambas interfaces.
type Schema struct {
	Tables    []SchemaTable     `json:"tables"`
	Relations []access.Relation `json:"relations"`
}

// Schema devuelve la estructura de la imagen. Calcular las claves primarias
// recorre las tablas, por eso el resultado se guarda hasta que cambie la imagen.
func (s *Session) Schema() (*Schema, error) {
	e, err := s.engine()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.schema != nil {
		sc := s.schema
		s.mu.Unlock()
		return sc, nil
	}
	s.mu.Unlock()

	s.qmu.Lock()
	infos, err := guard(func() ([]tableInfos, error) { return e.TableInfos() })
	var keys map[string][]string
	if err == nil {
		keys, err = guard(func() (map[string][]string, error) { return migrate.PrimaryKeys(context.Background(), e) })
	}
	s.qmu.Unlock()
	if err != nil {
		return nil, err
	}

	rels := e.Relations()
	fk := map[string]bool{} // "tabla\x00columna" en minúsculas
	for _, r := range rels {
		for _, c := range r.Columns {
			fk[strings.ToLower(r.Table)+"\x00"+strings.ToLower(c)] = true
		}
	}

	sc := &Schema{Relations: rels}
	if sc.Relations == nil {
		sc.Relations = []access.Relation{}
	}
	for _, t := range infos {
		pk := map[string]bool{}
		for _, c := range keys[t.Name] {
			pk[strings.ToLower(c)] = true
		}
		st := SchemaTable{Name: t.Name, Rows: t.Rows}
		for _, c := range t.Columns {
			l := strings.ToLower(c.Name)
			st.Columns = append(st.Columns, SchemaColumn{
				Name: c.Name, Type: c.Type, AccessType: c.AccessType,
				PK: pk[l], FK: fk[strings.ToLower(t.Name)+"\x00"+l],
			})
		}
		sc.Tables = append(sc.Tables, st)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eng == e { // la imagen sigue siendo la misma
		s.schema = sc
	}
	return sc, nil
}
