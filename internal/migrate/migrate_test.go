package migrate

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"accesql/internal/access"
	"accesql/internal/engine"
)

type fakeSrc struct {
	infos []engine.TableInfo
	rels  []access.Relation
	rows  map[string][][]any
}

func (f *fakeSrc) TableInfos() ([]engine.TableInfo, error) { return f.infos, nil }
func (f *fakeSrc) Relations() []access.Relation            { return f.rels }
func (f *fakeSrc) Scan(table string, fn func([]any) error) error {
	for _, r := range f.rows[table] {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

func col(name, accessType string, size int) engine.ColumnInfo {
	return engine.ColumnInfo{Name: name, AccessType: accessType, Size: size, Type: "x"}
}

func table(name string, cols ...engine.ColumnInfo) engine.TableInfo {
	return engine.TableInfo{Name: name, Columns: cols}
}

// fixture arma una base pequeña pero con todos los casos difíciles.
func fixture() *fakeSrc {
	f := &fakeSrc{rows: map[string][][]any{}}
	add := func(t engine.TableInfo, rows [][]any) {
		t.Rows = int64(len(rows))
		f.infos = append(f.infos, t)
		f.rows[t.Name] = rows
	}
	add(table("Customers", col("ID", "Long Integer", 4), col("Name", "Text", 50), col("Photo", "OLE", 0)),
		[][]any{{1, "Ana", nil}, {2, "O'Hara\nJr", []byte{1, 2}}, {3, nil, nil}})
	add(table("Orders", col("OrderID", "Long Integer", 4), col("CustomerID", "Long Integer", 4), col("Total", "Currency", 8), col("Notes", "Memo/Hyperlink", 0)),
		[][]any{{10, 1, "5.5000", "nota"}, {11, 2, "7.0000", nil}})
	// Códigos que, comparados como MySQL (sin acentos ni mayúsculas), se repiten.
	add(table("Codes", col("Code", "Text", 10)), [][]any{{"José"}, {"Jose"}})
	add(table("Items", col("ID", "Long Integer", 4), col("Code", "Text", 10)), [][]any{{1, "José"}})
	add(table("Small", col("ID", "Integer", 2), col("CustID", "Integer", 2)), [][]any{{1, 1}})
	add(table("Loose", col("A", "Long Integer", 4), col("CustID", "Long Integer", 4)), [][]any{{1, 1}})

	wide := table("Wide")
	row := []any{}
	for i := 0; i < 300; i++ {
		wide.Columns = append(wide.Columns, col(fmt.Sprintf("c%03d", i), "Text", 255))
		row = append(row, "x")
	}
	add(wide, [][]any{row})

	f.rels = []access.Relation{
		{Name: "CustOrders", Table: "Orders", Columns: []string{"CustomerID"}, RefTable: "Customers", RefColumns: []string{"ID"}, Enforced: true, CascadeDelete: true},
		{Name: "ItemCode", Table: "Items", Columns: []string{"Code"}, RefTable: "Codes", RefColumns: []string{"Code"}, Enforced: true, CascadeUpdate: true},
		{Name: "Mismatch", Table: "Small", Columns: []string{"CustID"}, RefTable: "Customers", RefColumns: []string{"ID"}, Enforced: true},
		{Name: "Weak", Table: "Loose", Columns: []string{"CustID"}, RefTable: "Customers", RefColumns: []string{"ID"}, Enforced: false},
		{Name: "Ghost", Table: "Nope", Columns: []string{"x"}, RefTable: "Customers", RefColumns: []string{"ID"}, Enforced: true},
	}
	return f
}

func newScript(t *testing.T, o NewOptions) (string, Stats) {
	t.Helper()
	var b bytes.Buffer
	st, err := WriteNew(context.Background(), &b, fixture(), o)
	if err != nil {
		t.Fatal(err)
	}
	return b.String(), st
}

// section devuelve el CREATE TABLE de una tabla.
func section(t *testing.T, script, table string) string {
	t.Helper()
	i := strings.Index(script, "CREATE TABLE `"+table+"` (")
	if i < 0 {
		t.Fatalf("no hay CREATE TABLE de %s", table)
	}
	j := strings.Index(script[i:], "ENGINE=InnoDB")
	return script[i : i+j]
}

func TestMySQLTypes(t *testing.T) {
	cases := map[string]engine.ColumnInfo{
		"TINYINT(1)":       {AccessType: "Boolean"},
		"TINYINT UNSIGNED": {AccessType: "Byte"},
		"SMALLINT":         {AccessType: "Integer"},
		"INT":              {AccessType: "Long Integer"},
		"BIGINT":           {AccessType: "Complex"},
		"FLOAT":            {AccessType: "Single"},
		"DOUBLE":           {AccessType: "Double"},
		"DECIMAL(19,4)":    {AccessType: "Currency"},
		"DECIMAL(38,3)":    {AccessType: "Numeric", Type: "decimal(38,3)"},
		"DATETIME":         {AccessType: "DateTime"},
		"LONGBLOB":         {AccessType: "OLE"},
		"CHAR(38)":         {AccessType: "Replication ID"},
		"VARCHAR(50)":      {AccessType: "Text", Size: 50},
		"VARCHAR(255)":     {AccessType: "Text", Size: 0},
		"LONGTEXT":         {AccessType: "Memo/Hyperlink"},
	}
	for want, c := range cases {
		if got := mysqlType(c); got != want {
			t.Errorf("%s → %s, quiero %s", c.AccessType, got, want)
		}
	}
}

func TestWriteNewStructure(t *testing.T) {
	script, st := newScript(t, NewOptions{Database: "demo", Data: true, ForeignKeys: true, Source: "demo.accdb", Version: "9.9"})

	for _, want := range []string{
		"-- Accesql 9.9 · demo.accdb → MySQL",
		"SET NAMES utf8mb4;", "SET FOREIGN_KEY_CHECKS = 0;", "SET FOREIGN_KEY_CHECKS = 1;",
		"CREATE DATABASE `demo` DEFAULT CHARACTER SET utf8mb4;", "USE `demo`;",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("falta %q", want)
		}
	}
	if strings.Contains(script, "DROP DATABASE") {
		t.Error("no se pidió DROP DATABASE")
	}
	if st.Tables != 7 {
		t.Errorf("Tables = %d, quiero 7", st.Tables)
	}

	cust := section(t, script, "Customers")
	for _, want := range []string{"`ID` INT NOT NULL", "`Name` VARCHAR(50)", "`Photo` LONGBLOB", "PRIMARY KEY (`ID`)"} {
		if !strings.Contains(cust, want) {
			t.Errorf("Customers sin %q:\n%s", want, cust)
		}
	}

	// La clave referenciada se acepta porque los datos la confirman.
	orders := section(t, script, "Orders")
	if !strings.Contains(orders, "CONSTRAINT `fk_CustOrders` FOREIGN KEY (`CustomerID`) REFERENCES `Customers` (`ID`) ON DELETE CASCADE") {
		t.Errorf("Orders sin la FOREIGN KEY esperada:\n%s", orders)
	}
	if strings.Contains(orders, "ON UPDATE") {
		t.Error("CustOrders no tiene cascada de actualización")
	}
}

func TestPrimaryKeyNeedsConfirmationFromData(t *testing.T) {
	script, _ := newScript(t, NewOptions{Database: "demo", ForeignKeys: true})

	// "José" y "Jose" son iguales para MySQL: Code NO puede ser clave primaria.
	codes := section(t, script, "Codes")
	if strings.Contains(codes, "PRIMARY KEY") {
		t.Errorf("Codes tiene valores repetidos para MySQL, no debe llevar PRIMARY KEY:\n%s", codes)
	}
	// Pero la FOREIGN KEY de Items la necesita indexada: se agrega un KEY.
	if !strings.Contains(codes, "KEY `ix_ref_1` (`Code`)") {
		t.Errorf("Codes debería tener un KEY para la relación:\n%s", codes)
	}
	items := section(t, script, "Items")
	if !strings.Contains(items, "FOREIGN KEY (`Code`) REFERENCES `Codes` (`Code`) ON UPDATE CASCADE") {
		t.Errorf("Items sin su FOREIGN KEY:\n%s", items)
	}
	// Customers.ID es único pero tiene un NULL en Name (otra columna): sigue siendo clave.
	if !strings.Contains(section(t, script, "Customers"), "PRIMARY KEY (`ID`)") {
		t.Error("Customers.ID debería ser clave primaria")
	}
}

func TestForeignKeysSkippedWithNotes(t *testing.T) {
	script, st := newScript(t, NewOptions{Database: "demo", ForeignKeys: true})
	notes := strings.Join(st.Notes, "\n")
	for _, want := range []string{"Mismatch", "tipos distintos", "Weak", "no exigía integridad", "Ghost"} {
		if want == "Ghost" {
			if strings.Contains(notes, want) {
				t.Error("una relación con una tabla inexistente se ignora sin ruido")
			}
			continue
		}
		if !strings.Contains(notes, want) {
			t.Errorf("faltan avisos con %q:\n%s", want, notes)
		}
	}
	if strings.Contains(section(t, script, "Small"), "FOREIGN KEY") || strings.Contains(section(t, script, "Loose"), "FOREIGN KEY") {
		t.Error("las relaciones omitidas no deben generar FOREIGN KEY")
	}
	if !strings.Contains(script, "-- AVISO: ") {
		t.Error("los avisos deben quedar también en el script")
	}

	// Sin la opción, no hay ninguna FOREIGN KEY ni aviso de relaciones.
	plain, st2 := newScript(t, NewOptions{Database: "demo"})
	if strings.Contains(plain, "FOREIGN KEY") || strings.Contains(strings.Join(st2.Notes, ""), "Weak") {
		t.Error("sin ForeignKeys no debe haber relaciones")
	}
}

func TestWideTableFitsMySQLRowLimit(t *testing.T) {
	script, st := newScript(t, NewOptions{Database: "demo"})
	wide := section(t, script, "Wide")
	if !strings.Contains(wide, "TEXT") {
		t.Error("300 columnas VARCHAR(255) no entran en una fila de MySQL: deberían pasar a TEXT")
	}
	if !strings.Contains(strings.Join(st.Notes, "\n"), `Tabla "Wide"`) {
		t.Error("debe avisar que cambió tipos")
	}
}

func TestWriteNewData(t *testing.T) {
	with, _ := newScript(t, NewOptions{Database: "demo", Data: true})
	if !strings.Contains(with, `(2, 'O''Hara\nJr', 0x0102)`) {
		t.Errorf("fila con comilla, salto de línea y binario mal escrita:\n%s", with)
	}
	if !strings.Contains(with, "(3, NULL, NULL)") {
		t.Error("los NULL deben salir como NULL")
	}
	if !strings.Contains(with, "(10, 1, '5.5000', 'nota')") {
		t.Errorf("fila de Orders mal escrita")
	}
	without, _ := newScript(t, NewOptions{Database: "demo", Data: false})
	if strings.Contains(without, "INSERT INTO") {
		t.Error("sin Data no debe haber INSERT")
	}
}

func TestWriteNewDropAndNameValidation(t *testing.T) {
	script, _ := newScript(t, NewOptions{Database: "demo", DropDatabase: true})
	if !strings.Contains(script, "DROP DATABASE IF EXISTS `demo`;") {
		t.Error("falta el DROP DATABASE pedido")
	}
	for _, bad := range []string{"", "mala base", "a;b", "x`y", strings.Repeat("a", 65)} {
		var b bytes.Buffer
		if _, err := WriteNew(context.Background(), &b, fixture(), NewOptions{Database: bad}); err == nil {
			t.Errorf("el nombre %q debería rechazarse", bad)
		}
		if b.Len() != 0 {
			t.Errorf("con un nombre inválido no debe escribirse nada (%q)", bad)
		}
	}
}

func TestWriteNewCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var b bytes.Buffer
	if _, err := WriteNew(ctx, &b, fixture(), NewOptions{Database: "demo", Data: true}); err == nil {
		t.Error("con el contexto cancelado debe fallar")
	}
}

func TestWriteAddModes(t *testing.T) {
	run := func(o AddOptions) string {
		t.Helper()
		var b bytes.Buffer
		if _, err := WriteAdd(context.Background(), &b, fixture(), o); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	pairs := []TablePair{{Source: "customers", Target: "CLIENTE"}}

	ignore := run(AddOptions{Tables: pairs, OnDuplicate: DupIgnore})
	if !strings.Contains(ignore, "INSERT IGNORE INTO `CLIENTE` (`ID`, `Name`, `Photo`) VALUES") {
		t.Errorf("modo omitir mal:\n%s", ignore)
	}
	if strings.Contains(ignore, "CREATE") || strings.Contains(ignore, "START TRANSACTION") {
		t.Error("agregar no crea nada, y sin Transaction no abre una")
	}

	upd := run(AddOptions{Tables: pairs, OnDuplicate: DupUpdate, Transaction: true})
	if !strings.Contains(upd, "ON DUPLICATE KEY UPDATE `Name` = VALUES(`Name`), `Photo` = VALUES(`Photo`);") {
		t.Errorf("modo actualizar: no debe tocar la clave (ID) y sí el resto:\n%s", upd)
	}
	if !strings.Contains(upd, "START TRANSACTION;") || !strings.Contains(upd, "COMMIT;") {
		t.Error("falta la transacción")
	}

	rep := run(AddOptions{Tables: pairs, OnDuplicate: DupReplace})
	if !strings.Contains(rep, "REPLACE INTO `CLIENTE`") {
		t.Errorf("modo reemplazar mal:\n%s", rep)
	}
}

func TestWriteAddTargetColumnsAndErrors(t *testing.T) {
	var b bytes.Buffer
	st, err := WriteAdd(context.Background(), &b, fixture(), AddOptions{
		Tables:      []TablePair{{Source: "Customers", Target: "CLIENTE", TargetColumns: []string{"id", "NAME"}}},
		OnDuplicate: DupIgnore,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "(`ID`, `Name`) VALUES") || strings.Contains(out, "Photo`) VALUES") {
		t.Errorf("solo deben copiarse las columnas que existen en el destino:\n%s", out)
	}
	if !strings.Contains(out, "(1, 'Ana')") {
		t.Errorf("las filas deben traer solo esas columnas:\n%s", out)
	}
	if !strings.Contains(strings.Join(st.Notes, " "), "Photo") {
		t.Error("debe avisar de la columna que no se copia")
	}
	if st.Rows != 3 || st.Tables != 1 {
		t.Errorf("Stats = %+v", st)
	}

	for name, o := range map[string]AddOptions{
		"sin tablas":           {},
		"tabla inexistente":    {Tables: []TablePair{{Source: "Nada"}}},
		"ninguna coincidencia": {Tables: []TablePair{{Source: "Customers", TargetColumns: []string{"zzz"}}}},
	} {
		var b bytes.Buffer
		if _, err := WriteAdd(context.Background(), &b, fixture(), o); err == nil {
			t.Errorf("%s: debería fallar", name)
		}
	}
}

func TestWriteAddEmptyTargetKeepsName(t *testing.T) {
	var b bytes.Buffer
	if _, err := WriteAdd(context.Background(), &b, fixture(), AddOptions{Tables: []TablePair{{Source: "Orders"}}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "INSERT IGNORE INTO `Orders`") {
		t.Errorf("sin destino debe usar el mismo nombre:\n%s", b.String())
	}
}

func TestPrimaryKeys(t *testing.T) {
	keys, err := PrimaryKeys(context.Background(), fixture())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(keys["Customers"], ","); got != "ID" {
		t.Errorf("Customers → %q", got)
	}
	if _, ok := keys["Codes"]; ok {
		t.Error("Codes no tiene clave confiable")
	}
	// Items.ID: no la referencia nadie, pero se llama ID y los datos la confirman.
	if got := strings.Join(keys["Items"], ","); got != "ID" {
		t.Errorf("Items → %q (clave deducida por nombre)", got)
	}
}
