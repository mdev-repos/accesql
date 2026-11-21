package session

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"accesql/internal/migrate"
)

// Esta prueba ejecuta los scripts generados contra un servidor MySQL/MariaDB
// REAL y compara los resultados. Se activa con ACCESQL_MYSQL_ARGS, que son los
// argumentos del cliente `mysql` para conectarse, por ejemplo:
//
//	ACCESQL_MYSQL_ARGS="--socket=/tmp/x.sock -uroot" go test ./internal/session -run RealMySQL -v
//
// Crea y borra las bases accesql_t1, accesql_t2.
func mysqlArgs(t *testing.T) []string {
	t.Helper()
	a := os.Getenv("ACCESQL_MYSQL_ARGS")
	if a == "" {
		t.Skip("definí ACCESQL_MYSQL_ARGS para probar contra un MySQL real")
	}
	return strings.Fields(a)
}

func runSQL(t *testing.T, args []string, script string) string {
	t.Helper()
	cmd := exec.Command("mysql", append(append([]string{}, args...), "--default-character-set=utf8mb4", "-N", "-B")...)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		head := script
		if len(head) > 300 {
			head = head[:300] + "…"
		}
		t.Fatalf("mysql falló: %v\n%s\n--- script:\n%s", err, out, head)
	}
	return strings.TrimSpace(string(out))
}

func scalar(t *testing.T, args []string, q string) string {
	t.Helper()
	return runSQL(t, args, q)
}

func TestRealMySQLCreateNewDatabase(t *testing.T) {
	args := mysqlArgs(t)
	s := ready(t)
	runSQL(t, args, "DROP DATABASE IF EXISTS accesql_t1;")
	t.Cleanup(func() { runSQL(t, args, "DROP DATABASE IF EXISTS accesql_t1;") })

	var b bytes.Buffer
	st, err := s.ScriptNew(context.Background(), &b, migrate.NewOptions{Database: "accesql_t1", Data: true, ForeignKeys: true, DropDatabase: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range st.Notes {
		t.Log("aviso:", n)
	}
	runSQL(t, args, b.String())

	// 1. Misma cantidad de filas en cada tabla.
	tables, _ := s.Tables()
	for _, tb := range tables {
		got := scalar(t, args, "SELECT COUNT(*) FROM accesql_t1."+"`"+tb.Name+"`")
		if got != strconv.FormatInt(tb.Rows, 10) {
			t.Errorf("tabla %s: MySQL tiene %s filas, la imagen %d", tb.Name, got, tb.Rows)
		}
	}

	// 2. Los mismos datos (texto, números y fechas), no solo la misma cantidad.
	for _, q := range []string{
		"SELECT GROUP_CONCAT(Company ORDER BY ID SEPARATOR '|') FROM %s`Customers`",
		"SELECT GROUP_CONCAT(COALESCE(`First Name`,'~') ORDER BY ID SEPARATOR '|') FROM %s`Employees`",
		"SELECT ROUND(SUM(`List Price`),4) FROM %s`Products`",
		"SELECT ROUND(SUM(`Standard Cost`),4) FROM %s`Products`",
		"SELECT COUNT(`Order Date`), MIN(`Order Date`), MAX(`Order Date`) FROM %s`Orders`",
		"SELECT COUNT(*) FROM %s`Orders` WHERE `Shipped Date` IS NULL",
	} {
		mine, _, err := s.Query(fmt.Sprintf(q, ""), 10)
		if err != nil {
			t.Fatalf("la imagen no pudo ejecutar %q: %v", q, err)
		}
		var parts []string
		for _, v := range mine.Rows[0] {
			parts = append(parts, fmt.Sprint(v))
		}
		got := scalar(t, args, fmt.Sprintf(q, "accesql_t1."))
		got = strings.ReplaceAll(got, "\t", "|")
		want := strings.Join(parts, "|")
		// Fechas: la imagen las da como 2006-01-02T15:04:05 y MySQL como "2006-01-02 15:04:05".
		want = strings.ReplaceAll(want, "T", " ")
		got = strings.ReplaceAll(got, "T", " ")
		if !sameValues(want, got) {
			t.Errorf("datos distintos en %q\n  imagen: %.200s\n  MySQL:  %.200s", q, want, got)
		}
	}

	// 3. Claves y relaciones realmente creadas.
	pks := scalar(t, args, "SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS WHERE TABLE_SCHEMA='accesql_t1' AND CONSTRAINT_TYPE='PRIMARY KEY'")
	fks := scalar(t, args, "SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS WHERE TABLE_SCHEMA='accesql_t1' AND CONSTRAINT_TYPE='FOREIGN KEY'")
	t.Logf("claves primarias: %s · claves foráneas: %s", pks, fks)
	if pks == "0" || fks == "0" {
		t.Errorf("deberían haberse creado claves (PK=%s FK=%s)", pks, fks)
	}
	ref := scalar(t, args, "SELECT CONCAT(TABLE_NAME,'.',COLUMN_NAME,'->',REFERENCED_TABLE_NAME,'.',REFERENCED_COLUMN_NAME) FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA='accesql_t1' AND TABLE_NAME='Orders' AND COLUMN_NAME='Customer ID' AND REFERENCED_TABLE_NAME IS NOT NULL")
	if ref != "Orders.Customer ID->Customers.ID" {
		t.Errorf("falta la relación Orders→Customers en MySQL: %q", ref)
	}

	// 4. Codificación: el servidor de prueba usa latin1 por defecto; la base nueva debe ser utf8mb4.
	if cs := scalar(t, args, "SELECT DEFAULT_CHARACTER_SET_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='accesql_t1'"); cs != "utf8mb4" {
		t.Errorf("la base debe crearse en utf8mb4, está en %s", cs)
	}
}

func TestRealMySQLAddToExistingTables(t *testing.T) {
	args := mysqlArgs(t)
	s := ready(t)
	runSQL(t, args, "DROP DATABASE IF EXISTS accesql_t2;")
	t.Cleanup(func() { runSQL(t, args, "DROP DATABASE IF EXISTS accesql_t2;") })

	// Base destino: solo la estructura, sin datos ni relaciones.
	var ddl bytes.Buffer
	if _, err := s.ScriptNew(context.Background(), &ddl, migrate.NewOptions{Database: "accesql_t2", Data: false}); err != nil {
		t.Fatal(err)
	}
	runSQL(t, args, ddl.String())

	add := func(o migrate.AddOptions) {
		t.Helper()
		var b bytes.Buffer
		if _, err := s.ScriptAdd(context.Background(), &b, o); err != nil {
			t.Fatal(err)
		}
		runSQL(t, args, "USE accesql_t2;\n"+b.String())
	}
	count := func(table string) string {
		return scalar(t, args, "SELECT COUNT(*) FROM accesql_t2.`"+table+"`")
	}
	company := func() string {
		return scalar(t, args, "SELECT Company FROM accesql_t2.`Customers` WHERE ID=1")
	}
	cust := []migrate.TablePair{{Source: "Customers", Target: "Customers"}}

	add(migrate.AddOptions{Tables: cust, OnDuplicate: migrate.DupIgnore, Transaction: true})
	if n := count("Customers"); n != "29" {
		t.Fatalf("tras agregar hay %s clientes, esperaba 29", n)
	}
	orig := company()

	// Alguien cambió un dato en MySQL. Cada modo debe tratarlo distinto.
	runSQL(t, args, "UPDATE accesql_t2.`Customers` SET Company='CAMBIADO' WHERE ID=1;")

	add(migrate.AddOptions{Tables: cust, OnDuplicate: migrate.DupIgnore})
	if c := company(); c != "CAMBIADO" || count("Customers") != "29" {
		t.Errorf("omitir no debe tocar lo existente: %q (%s filas)", c, count("Customers"))
	}
	add(migrate.AddOptions{Tables: cust, OnDuplicate: migrate.DupUpdate})
	if c := company(); c != orig || count("Customers") != "29" {
		t.Errorf("actualizar debe restaurar el dato de Access: %q (esperaba %q)", c, orig)
	}
	runSQL(t, args, "UPDATE accesql_t2.`Customers` SET Company='CAMBIADO' WHERE ID=1;")
	add(migrate.AddOptions{Tables: cust, OnDuplicate: migrate.DupReplace})
	if c := company(); c != orig || count("Customers") != "29" {
		t.Errorf("reemplazar debe restaurar el dato de Access: %q", c)
	}

	// Destino con otro nombre y solo algunas columnas.
	runSQL(t, args, "CREATE TABLE accesql_t2.`Clientes2` (id INT PRIMARY KEY, company VARCHAR(50), extra INT DEFAULT 7);")
	add(migrate.AddOptions{
		Tables:      []migrate.TablePair{{Source: "Customers", Target: "Clientes2", TargetColumns: []string{"id", "company", "extra"}}},
		OnDuplicate: migrate.DupIgnore,
	})
	if n := count("Clientes2"); n != "29" {
		t.Errorf("Clientes2 tiene %s filas, esperaba 29", n)
	}
	if got := scalar(t, args, "SELECT CONCAT(id,'|',company,'|',extra) FROM accesql_t2.`Clientes2` WHERE id=1"); got != "1|"+orig+"|7" {
		t.Errorf("la columna que no se copia debe quedar con su valor por defecto: %q", got)
	}
}

// sameValues compara dos listas separadas por | tolerando que los números se
// escriban distinto (713.06 y 713.0600 son el mismo valor).
func sameValues(a, b string) bool {
	if a == b {
		return true
	}
	pa, pb := strings.Split(a, "|"), strings.Split(b, "|")
	if len(pa) != len(pb) {
		return false
	}
	for i := range pa {
		if pa[i] == pb[i] {
			continue
		}
		x, e1 := strconv.ParseFloat(pa[i], 64)
		y, e2 := strconv.ParseFloat(pb[i], 64)
		if e1 != nil || e2 != nil || x != y {
			return false
		}
	}
	return true
}
