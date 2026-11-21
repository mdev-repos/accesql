package app

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Datos de ejemplo de la maqueta (los mismos que la GUI web).

type mockTable struct {
	name string
	rows int
}

var mockTables = []mockTable{
	{"Customers", 29}, {"Employee Privileges", 12}, {"Employees", 9}, {"Inventory Transaction Types", 4},
	{"Inventory Transactions", 102}, {"Invoices", 35}, {"Order Details", 58}, {"Order Details Status", 4},
	{"Orders", 48}, {"Orders Status", 3}, {"Orders Tax Status", 2}, {"Privileges", 1}, {"Products", 45},
	{"Purchase Order Details", 55}, {"Purchase Order Status", 3}, {"Purchase Orders", 28},
	{"Sales Reports", 7}, {"Shippers", 3}, {"Strings", 50}, {"Suppliers", 10},
}

func mockTotalRows() int {
	n := 0
	for _, t := range mockTables {
		n += t.rows
	}
	return n
}

// Sistema de archivos falso para el explorador.
type mockDir struct {
	dirs, files []string
}

var mockFS = map[string]mockDir{
	"/":                             {dirs: []string{"home", "srv"}},
	"/home":                         {dirs: []string{"usuario"}},
	"/home/usuario":                 {dirs: []string{"Datos", "Descargas"}},
	"/home/usuario/Datos":           {dirs: []string{"Respaldos"}, files: []string{"Northwind.accdb", "Clientes.mdb", "notas.txt"}},
	"/home/usuario/Datos/Respaldos": {files: []string{"Northwind_2023.accdb"}},
	"/home/usuario/Descargas":       {files: []string{"Inventario.accdb"}},
	"/srv":                          {dirs: []string{"compartido"}},
	"/srv/compartido":               {files: []string{"Contabilidad.mdb"}},
}

var accessFile = regexp.MustCompile(`(?i)\.(accdb|mdb)$`)

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func joinPath(dir, name string) string {
	if dir == "/" {
		return "/" + name
	}
	return dir + "/" + name
}

func parentPath(dir string) string {
	i := strings.LastIndex(dir, "/")
	if i <= 0 {
		return "/"
	}
	return dir[:i]
}

func baseName(path string) string {
	return path[strings.LastIndex(path, "/")+1:]
}

// Resultado de ejemplo para las consultas.
var (
	sampleCols = []string{"ID", "Company", "Last Name", "First Name", "City", "Credit Limit"}
	sampleRows = [][]any{
		{1, "Company A", "Bedecs", "Anna", "Seattle", 5000.5},
		{2, "Company B", "Gratacos Solsona", "Antonio", "Boston", nil},
		{3, "Company C", "Axen", "Thomas", "Los Angeles", 2500},
		{4, "Company D", "Lee", "Christina", "New York", nil},
		{5, "Company E", "O'Donnell", "Martin", "Minneapolis", 12000},
	}
)

// Diagrama de ejemplo: tablas con campos y relaciones (FK → tabla.campo).
type erField struct {
	name, typ, key string // key: "pk", "fk" o ""
}

type erTable struct {
	name   string
	fields []erField
}

var erTables = []erTable{
	{"Customers", []erField{{"ID", "INT", "pk"}, {"Company", "TEXT", ""}, {"Last Name", "TEXT", ""}, {"City", "TEXT", ""}}},
	{"Employees", []erField{{"ID", "INT", "pk"}, {"First Name", "TEXT", ""}, {"Last Name", "TEXT", ""}, {"Job Title", "TEXT", ""}}},
	{"Order Details", []erField{{"ID", "INT", "pk"}, {"Order ID", "INT", "fk"}, {"Product ID", "INT", "fk"}, {"Quantity", "DECIMAL", ""}}},
	{"Orders", []erField{{"ID", "INT", "pk"}, {"Customer ID", "INT", "fk"}, {"Employee ID", "INT", "fk"}, {"Order Date", "DATETIME", ""}}},
	{"Products", []erField{{"ID", "INT", "pk"}, {"Product Name", "TEXT", ""}, {"Supplier ID", "INT", "fk"}, {"List Price", "DECIMAL", ""}}},
	{"Suppliers", []erField{{"ID", "INT", "pk"}, {"Company", "TEXT", ""}, {"City", "TEXT", ""}, {"Phone", "TEXT", ""}}},
}

// erRels: desde tabla.campo (clave foránea) hacia tabla.campo.
var erRels = [][4]string{
	{"Orders", "Customer ID", "Customers", "ID"},
	{"Orders", "Employee ID", "Employees", "ID"},
	{"Order Details", "Order ID", "Orders", "ID"},
	{"Order Details", "Product ID", "Products", "ID"},
	{"Products", "Supplier ID", "Suppliers", "ID"},
}

// --- Generación de scripts SQL de ejemplo ---------------------------------

func quoteID(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }

var dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_$]+$`)

// mockScriptNew arma un script de ejemplo para "crear una base nueva".
func mockScriptNew(file, name string, withData, drop bool) string {
	L := []string{"-- Accesql " + Version + " · script de EJEMPLO (maqueta)", "-- Origen: " + file, ""}
	if drop {
		L = append(L, "DROP DATABASE IF EXISTS "+quoteID(name)+";")
	}
	L = append(L,
		"CREATE DATABASE "+quoteID(name)+" DEFAULT CHARACTER SET utf8mb4;",
		"USE "+quoteID(name)+";", "",
		"CREATE TABLE `Customers` (",
		"  `ID` INT NOT NULL,",
		"  `Company` VARCHAR(255),",
		"  `Last Name` VARCHAR(255),",
		"  PRIMARY KEY (`ID`)",
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;", "")
	if withData {
		L = append(L,
			"INSERT INTO `Customers` (`ID`, `Company`, `Last Name`) VALUES",
			"  (1, 'Company A', 'Bedecs'),",
			"  (2, 'Company B', 'Gratacos Solsona');", "")
	}
	L = append(L, "-- … y así con las demás tablas")
	return strings.Join(L, "\n")
}

var dupModes = []string{"Omitirlo (no cambia nada)", "Actualizarlo con los datos de Access", "Reemplazarlo por completo"}

// mockScriptAdd arma un script de ejemplo para "agregar a tablas existentes".
func mockScriptAdd(file string, src, dst []string, dup int, tx bool) string {
	verb := []string{"INSERT IGNORE INTO", "INSERT INTO", "REPLACE INTO"}[dup]
	L := []string{
		"-- Accesql " + Version + " · script de EJEMPLO (maqueta)",
		"-- Origen: " + file,
		fmt.Sprintf("-- %d tabla(s) seleccionada(s); el ejemplo muestra las primeras dos.", len(src)), ""}
	if tx {
		L = append(L, "START TRANSACTION;", "")
	}
	for i := 0; i < len(src) && i < 2; i++ {
		d := dst[i]
		if strings.TrimSpace(d) == "" {
			d = src[i]
		}
		L = append(L, "-- "+src[i]+" → "+d, verb+" "+quoteID(d)+" (`ID`, `Company`) VALUES", "  (1, 'Company A'),")
		if dup == 1 {
			L = append(L, "  (2, 'Company B')", "ON DUPLICATE KEY UPDATE `Company` = VALUES(`Company`);", "")
		} else {
			L = append(L, "  (2, 'Company B');", "")
		}
	}
	if tx {
		L = append(L, "COMMIT;")
	}
	return strings.Join(L, "\n")
}
