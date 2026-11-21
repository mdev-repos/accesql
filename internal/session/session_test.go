package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"access2mysql/internal/migrate"

	"github.com/dolthub/go-mysql-server/sql"
)

const northwind = "../../testdb/Northwind.accdb"

func needDB(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat(northwind); err != nil {
		t.Skip("falta testdb/Northwind.accdb")
	}
	return northwind
}

// ready devuelve una sesión con la imagen ya generada.
func ready(t *testing.T) *Session {
	t.Helper()
	s := New()
	t.Cleanup(s.Close)
	if err := s.Open(needDB(t)); err != nil {
		t.Fatal(err)
	}
	if err := s.Generate(context.Background(), GenerateOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOpenValidation(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "roto.accdb")
	os.WriteFile(bad, []byte("esto no es una base de Access"), 0o644)
	txt := filepath.Join(dir, "notas.txt")
	os.WriteFile(txt, []byte("x"), 0o644)

	s := New()
	defer s.Close()
	for name, path := range map[string]string{
		"no existe":        filepath.Join(dir, "nada.accdb"),
		"extensión ajena":  txt,
		"es una carpeta":   dir,
		"archivo corrupto": bad,
	} {
		if err := s.Open(path); err == nil {
			t.Errorf("%s: debería fallar", name)
		}
		if st := s.State(); st.File != "" {
			t.Errorf("%s: un Open fallido no debe dejar archivo elegido (File=%q)", name, st.File)
		}
	}

	if err := s.Open(needDB(t)); err != nil {
		t.Fatal(err)
	}
	st := s.State()
	if !filepath.IsAbs(st.File) || !strings.HasSuffix(st.File, "Northwind.accdb") || st.ImageReady {
		t.Errorf("estado tras Open: %+v", st)
	}
}

func TestOperationsRequireFileAndImage(t *testing.T) {
	s := New()
	if err := s.Generate(context.Background(), GenerateOptions{}, nil); !errors.Is(err, ErrNoFile) {
		t.Errorf("Generate sin archivo: %v", err)
	}
	if _, _, err := s.Query("SELECT 1", 10); !errors.Is(err, ErrNoFile) {
		t.Errorf("Query sin archivo: %v", err)
	}
	if err := s.Open(needDB(t)); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.Query("SELECT 1", 10); !errors.Is(err, ErrNoImage) {
		t.Errorf("Query sin imagen: %v", err)
	}
	if _, err := s.Tables(); !errors.Is(err, ErrNoImage) {
		t.Errorf("Tables sin imagen: %v", err)
	}
	if err := s.StartServer(ServerOptions{Host: "127.0.0.1", Port: 3306, User: "a", Password: "b"}); !errors.Is(err, ErrNoImage) {
		t.Errorf("StartServer sin imagen: %v", err)
	}
	if _, err := s.Schema(); !errors.Is(err, ErrNoImage) {
		t.Errorf("Schema sin imagen: %v", err)
	}
}

func TestGenerateReportsProgress(t *testing.T) {
	s := New()
	defer s.Close()
	if err := s.Open(needDB(t)); err != nil {
		t.Fatal(err)
	}
	var got []Progress
	if err := s.Generate(context.Background(), GenerateOptions{}, func(p Progress) { got = append(got, p) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 {
		t.Fatalf("esperaba 20 avisos de progreso, hubo %d", len(got))
	}
	for i, p := range got {
		if p.Number != i+1 || p.Total != 20 || p.Table == "" {
			t.Errorf("progreso %d: %+v", i, p)
		}
	}
	st := s.State()
	if !st.ImageReady || st.Tables != 20 || st.Rows < 100 || st.Database != "access" || st.Generating || st.LoadedAt.IsZero() {
		t.Errorf("estado tras generar: %+v", st)
	}
	tables, err := s.Tables()
	if err != nil || len(tables) != 20 {
		t.Fatalf("Tables: %d, %v", len(tables), err)
	}
}

func TestGenerateCustomDatabaseNameAndValidation(t *testing.T) {
	s := New()
	defer s.Close()
	s.Open(needDB(t))
	if err := s.Generate(context.Background(), GenerateOptions{Database: "mala base"}, nil); err == nil {
		t.Error("un nombre con espacios debe rechazarse")
	}
	if err := s.Generate(context.Background(), GenerateOptions{Database: "norte"}, nil); err != nil {
		t.Fatal(err)
	}
	if s.State().Database != "norte" {
		t.Errorf("Database = %q", s.State().Database)
	}
	// El nombre elegido es el que existe para las consultas calificadas.
	res, _, err := s.Query("SELECT COUNT(*) AS n FROM norte.Customers", 10)
	if err != nil || res.Rows[0][0] != int64(29) {
		t.Errorf("consulta con la base calificada: %v %v", res, err)
	}
}

func TestGenerateCancelKeepsPreviousImage(t *testing.T) {
	s := ready(t)
	ctx, cancel := context.WithCancel(context.Background())
	err := s.Generate(ctx, GenerateOptions{}, func(p Progress) {
		if p.Number == 3 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("esperaba context.Canceled, hubo %v", err)
	}
	if st := s.State(); !st.ImageReady || st.Generating {
		t.Errorf("cancelar no debe perder la imagen anterior: %+v", st)
	}
	if res, _, err := s.Query("SELECT COUNT(*) FROM Customers", 10); err != nil || res.Rows[0][0] != int64(29) {
		t.Errorf("la imagen anterior debe seguir funcionando: %v %v", res, err)
	}
}

func TestBusyWhileGenerating(t *testing.T) {
	s := New()
	defer s.Close()
	s.Open(needDB(t))

	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)
	go func() {
		done <- s.Generate(context.Background(), GenerateOptions{}, func(Progress) {
			once.Do(func() { close(started) })
			<-release
		})
	}()
	<-started

	if !s.State().Generating {
		t.Error("State debe mostrar Generating")
	}
	if err := s.Generate(context.Background(), GenerateOptions{}, nil); !errors.Is(err, ErrBusy) {
		t.Errorf("segundo Generate: %v", err)
	}
	if err := s.Open(needDB(t)); !errors.Is(err, ErrBusy) {
		t.Errorf("Open durante la generación: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.State().Generating || !s.State().ImageReady {
		t.Errorf("estado final: %+v", s.State())
	}
}

func TestReopenDropsImage(t *testing.T) {
	s := ready(t)
	if err := s.Open(needDB(t)); err != nil {
		t.Fatal(err)
	}
	if st := s.State(); st.ImageReady || st.Tables != 0 {
		t.Errorf("al abrir una base hay que volver a generar la imagen: %+v", st)
	}
	if _, _, err := s.Query("SELECT 1", 10); !errors.Is(err, ErrNoImage) {
		t.Errorf("Query: %v", err)
	}
}

func TestQuery(t *testing.T) {
	s := ready(t)

	res, trunc, err := s.Query("SELECT ID, Company FROM Customers ORDER BY ID", 5)
	if err != nil || !trunc || len(res.Rows) != 5 || res.Columns[0] != "ID" {
		t.Fatalf("Query con límite: rows=%d trunc=%v err=%v", len(res.Rows), trunc, err)
	}
	// Cada consulta reemplaza a la anterior (el defecto visto en la maqueta).
	res2, _, _ := s.Query("SELECT COUNT(*) AS n FROM Customers", 10)
	if res2.Columns[0] != "n" || res2.Rows[0][0] != int64(29) {
		t.Errorf("segunda consulta: %+v", res2)
	}

	for _, bad := range []string{"", "   ", "SELEC nada", "SELECT * FROM NoExiste"} {
		if _, _, err := s.Query(bad, 10); err == nil {
			t.Errorf("%q debería fallar", bad)
		}
	}
	// Solo lectura.
	if _, _, err := s.Query("DELETE FROM Customers", 10); err == nil {
		t.Error("el motor debe ser de solo lectura")
	}
	if res, _, _ := s.Query("SELECT COUNT(*) FROM Customers", 10); res.Rows[0][0] != int64(29) {
		t.Error("un DELETE rechazado no debe tocar datos")
	}
}

// El blindaje no puede depender del orden de los tests ni del estado global que
// dejó otro motor: cada caso parte de la variable vacía, como en un proceso
// recién iniciado (que es donde apareció una fuga real: la sesión interna
// copiaba el valor ANTES del blindaje y LOAD_FILE leía cualquier archivo).
func TestEngineIsLockedDown(t *testing.T) {
	for _, withServer := range []bool{false, true} {
		name := "sin servidor"
		if withServer {
			name = "con servidor MySQL activo"
		}
		t.Run(name, func(t *testing.T) {
			if err := sql.SystemVariables.AssignValues(map[string]any{"secure_file_priv": ""}); err != nil {
				t.Fatal(err)
			}
			s := ready(t)
			if withServer {
				if err := s.StartServer(ServerOptions{Host: "127.0.0.1", Port: freePort(t), User: "app", Password: "p"}); err != nil {
					t.Fatal(err)
				}
			}

			// La sesión interna debe apuntar al directorio aislado.
			res, _, err := s.Query("SELECT @@secure_file_priv", 10)
			if err != nil || !strings.Contains(fmt.Sprint(res.Rows[0][0]), "access-tool-sandbox") {
				t.Fatalf("secure_file_priv de la sesión interna = %v (%v)", res, err)
			}

			secret := filepath.Join(t.TempDir(), "secreto.txt")
			os.WriteFile(secret, []byte("contenido-secreto"), 0o644)
			for _, path := range []string{secret, "/etc/passwd"} {
				if res, _, err := s.Query("SELECT LOAD_FILE('"+path+"')", 10); err == nil && res.Rows[0][0] != nil {
					t.Errorf("LOAD_FILE leyó %s: %q", path, res.Rows[0][0])
				}
			}

			out := filepath.Join(t.TempDir(), "fuga.txt")
			for _, q := range []string{
				"SELECT * FROM Customers INTO OUTFILE '" + out + "'",
				"SELECT 'x' INTO DUMPFILE '" + out + "'",
				"LOAD DATA INFILE '" + secret + "' INTO TABLE Customers",
			} {
				s.Query(q, 10)
			}
			if _, err := os.Stat(out); err == nil {
				t.Error("se creó un archivo fuera del directorio aislado")
			}

			// Intentos de aflojar el blindaje o de crear cuentas.
			for _, q := range []string{
				"SET GLOBAL secure_file_priv = ''",
				"SET SESSION secure_file_priv = ''",
				"CREATE USER hacker IDENTIFIED BY 'x'",
				"GRANT ALL ON *.* TO hacker",
			} {
				s.Query(q, 10)
			}
			if res, _, err := s.Query("SELECT @@secure_file_priv", 10); err != nil || !strings.Contains(fmt.Sprint(res.Rows[0][0]), "access-tool-sandbox") {
				t.Errorf("secure_file_priv cambió: %v (%v)", res, err)
			}
			if res, _, err := s.Query("SELECT COUNT(*) FROM mysql.user WHERE User = 'hacker'", 10); err == nil && res.Rows[0][0] != int64(0) {
				t.Errorf("se creó una cuenta: %v", res.Rows[0][0])
			}
			if res, _, _ := s.Query("SELECT LOAD_FILE('/etc/passwd')", 10); res != nil && res.Rows[0][0] != nil {
				t.Error("tras los intentos, LOAD_FILE leyó un archivo")
			}
		})
	}
}

func TestSchema(t *testing.T) {
	s := ready(t)
	sc, err := s.Schema()
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Tables) != 20 {
		t.Fatalf("tablas: %d", len(sc.Tables))
	}
	if len(sc.Relations) < 5 {
		t.Fatalf("Northwind define relaciones reales, vinieron %d", len(sc.Relations))
	}
	var found bool
	for _, r := range sc.Relations {
		if strings.HasPrefix(r.Table, "MSys") || strings.HasPrefix(r.RefTable, "MSys") {
			t.Errorf("las relaciones del sistema no deben aparecer: %+v", r)
		}
		if r.Table == "Orders" && r.RefTable == "Customers" && r.Columns[0] == "Customer ID" && r.RefColumns[0] == "ID" {
			found = true
		}
	}
	if !found {
		t.Error("falta la relación Orders.Customer ID → Customers.ID")
	}

	col := func(table, name string) SchemaColumn {
		for _, tb := range sc.Tables {
			if tb.Name == table {
				for _, c := range tb.Columns {
					if c.Name == name {
						return c
					}
				}
			}
		}
		t.Fatalf("no existe %s.%s", table, name)
		return SchemaColumn{}
	}
	if !col("Customers", "ID").PK {
		t.Error("Customers.ID debería ser clave primaria")
	}
	if !col("Orders", "Customer ID").FK {
		t.Error("Orders.Customer ID debería marcarse como clave foránea")
	}
	if col("Customers", "Company").PK || col("Customers", "Company").FK {
		t.Error("Customers.Company no es clave")
	}

	again, _ := s.Schema()
	if again != sc {
		t.Error("el esquema debe guardarse mientras la imagen no cambie")
	}
}

func TestExports(t *testing.T) {
	s := ready(t)
	q := "SELECT ID, Company FROM Customers ORDER BY ID LIMIT 3"

	var j bytes.Buffer
	if err := s.ExportQuery(q, FormatJSON, "", 100, &j); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(j.Bytes(), &rows); err != nil || len(rows) != 3 {
		t.Errorf("JSON: %d filas, %v", len(rows), err)
	}

	var c bytes.Buffer
	if err := s.ExportQuery(q, FormatCSVExcel, "", 100, &c); err != nil || !strings.HasPrefix(c.String(), "\xEF\xBB\xBFID;Company\n") {
		t.Errorf("CSV Excel: %v %q", err, c.String())
	}

	var sqlb bytes.Buffer
	if err := s.ExportQuery(q, FormatSQL, "", 100, &sqlb); err != nil || !strings.Contains(sqlb.String(), "INSERT INTO `resultado` (`ID`, `Company`) VALUES") {
		t.Errorf("SQL: %v %q", err, sqlb.String())
	}

	var big bytes.Buffer
	err := s.ExportQuery("SELECT * FROM Customers", FormatJSON, "", 5, &big)
	if !errors.Is(err, ErrTooManyRows) || big.Len() != 0 {
		t.Errorf("demasiadas filas: err=%v, escrito=%d bytes (no debe exportarse un resultado cortado)", err, big.Len())
	}
	if err := s.ExportQuery(q, Format("xml"), "", 100, &big); err == nil {
		t.Error("formato desconocido")
	}
	if FormatSQL.Extension() != ".sql" || FormatCSVExcel.Extension() != ".csv" || FormatJSON.Extension() != ".json" {
		t.Error("extensiones")
	}
}

func TestWriteDump(t *testing.T) {
	s := ready(t)
	var b bytes.Buffer
	if err := s.WriteDump(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	var dump map[string][]map[string]any
	if err := json.Unmarshal(b.Bytes(), &dump); err != nil {
		t.Fatalf("el volcado no es JSON válido: %v", err)
	}
	if len(dump) != 20 || len(dump["Customers"]) != 29 || dump["Customers"][0]["ID"] != float64(1) {
		t.Errorf("volcado: %d tablas, Customers=%d filas", len(dump), len(dump["Customers"]))
	}
	// Mismo formato que el comando `dump`: las columnas van en el orden de Access.
	if !strings.Contains(b.String(), `{"ID": 1, "Company": "Company A", "Last Name":`) {
		t.Errorf("las columnas deben salir en el orden de Access:\n%.200s", b.String())
	}
}

func TestScripts(t *testing.T) {
	s := ready(t)
	if got := s.State().Suggested; got != "northwind" {
		t.Errorf("Suggested = %q", got)
	}

	var b bytes.Buffer
	st, err := s.ScriptNew(context.Background(), &b, migrate.NewOptions{Database: "northwind", Data: true, ForeignKeys: true})
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if st.Tables != 20 || !strings.Contains(out, "-- Accesql 0.1.0 · Northwind.accdb → MySQL") ||
		!strings.Contains(out, "FOREIGN KEY") || !strings.Contains(out, "INSERT INTO `Customers`") {
		t.Errorf("script nuevo incompleto (tablas=%d, %d bytes)", st.Tables, len(out))
	}

	var a bytes.Buffer
	st2, err := s.ScriptAdd(context.Background(), &a, migrate.AddOptions{
		Tables:      []migrate.TablePair{{Source: "Customers", Target: "CLIENTE"}},
		OnDuplicate: migrate.DupUpdate, Transaction: true,
	})
	if err != nil || st2.Rows != 29 || !strings.Contains(a.String(), "INSERT INTO `CLIENTE`") {
		t.Errorf("script agregar: %+v %v", st2, err)
	}
}

// ---------------------------------------------------------------------------
// Servidor MySQL
// ---------------------------------------------------------------------------

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func canDial(port int) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// mysqlCLI ejecuta una consulta con el cliente `mysql` real; ok=false si falló.
func mysqlCLI(t *testing.T, port int, user, pass, query string) (out string, ok bool) {
	t.Helper()
	cmd := exec.Command("mysql", "--protocol=tcp", "-h127.0.0.1", "-P"+strconv.Itoa(port), "-u"+user,
		"--connect-timeout=5", "-N", "-B", "-e", query, "access")
	cmd.Env = append(os.Environ(), "MYSQL_PWD="+pass)
	b, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(b)), err == nil
}

func TestServerValidation(t *testing.T) {
	cases := map[string]ServerOptions{
		"dirección":   {Host: " ", Port: 3306, User: "u", Password: "p"},
		"puerto 0":    {Host: "h", Port: 0, User: "u", Password: "p"},
		"puerto alto": {Host: "h", Port: 70000, User: "u", Password: "p"},
		"usuario":     {Host: "h", Port: 3306, User: "", Password: "p"},
		"contraseña":  {Host: "h", Port: 3306, User: "u", Password: ""},
	}
	for name, o := range cases {
		if ValidateServer(o) == "" {
			t.Errorf("%s: debería rechazarse", name)
		}
	}
	if msg := ValidateServer(ServerOptions{Host: "127.0.0.1", Port: 3306, User: "u", Password: "p"}); msg != "" {
		t.Errorf("opciones válidas rechazadas: %s", msg)
	}
	s := ready(t)
	if err := s.StartServer(ServerOptions{Host: "127.0.0.1", Port: 3306, User: "u"}); err == nil || !strings.Contains(err.Error(), "contraseña") {
		t.Errorf("sin contraseña: %v", err)
	}
	if s.State().Server.Running {
		t.Error("un inicio rechazado no debe dejar el servidor activo")
	}
}

func TestServerLifecycle(t *testing.T) {
	s := ready(t)
	port := freePort(t)
	opts := ServerOptions{Host: "127.0.0.1", Port: port, User: "app", Password: "clave-uno"}

	if err := s.StartServer(opts); err != nil {
		t.Fatal(err)
	}
	st := s.State().Server
	if !st.Running || st.Address != net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) || st.User != "app" || st.Database != "access" {
		t.Errorf("estado del servidor: %+v", st)
	}
	if strings.Contains(strings.ToLower(mustJSON(s.State())), "clave-uno") {
		t.Error("la contraseña no debe aparecer en el estado")
	}
	if !canDial(port) {
		t.Fatal("el puerto debería aceptar conexiones")
	}
	if err := s.StartServer(opts); !errors.Is(err, ErrServerRunning) {
		t.Errorf("segundo StartServer: %v", err)
	}

	if _, err := exec.LookPath("mysql"); err == nil {
		out, ok := mysqlCLI(t, port, "app", "clave-uno", "SELECT COUNT(*) FROM Customers")
		if !ok || out != "29" {
			t.Errorf("consulta con el cliente mysql: ok=%v %q", ok, out)
		}
		if _, ok := mysqlCLI(t, port, "app", "mala", "SELECT 1"); ok {
			t.Error("una contraseña incorrecta debe rechazarse")
		}
		if out, ok := mysqlCLI(t, port, "app", "clave-uno", "DELETE FROM Customers"); ok {
			t.Errorf("el servidor debe ser de solo lectura: %q", out)
		}
	} else {
		t.Log("no hay cliente mysql: se omite la parte de protocolo")
	}

	s.StopServer()
	if s.State().Server.Running || canDial(port) {
		t.Error("tras detenerlo, el puerto debe cerrarse")
	}
	s.StopServer() // idempotente

	// Reiniciar con OTRA contraseña: la anterior no puede seguir valiendo.
	opts.Password = "clave-dos"
	if err := s.StartServer(opts); err != nil {
		t.Fatalf("reiniciar: %v", err)
	}
	if _, err := exec.LookPath("mysql"); err == nil {
		if _, ok := mysqlCLI(t, port, "app", "clave-uno", "SELECT 1"); ok {
			t.Error("la contraseña vieja sigue funcionando tras reiniciar con otra")
		}
		if out, ok := mysqlCLI(t, port, "app", "clave-dos", "SELECT COUNT(*) FROM Customers"); !ok || out != "29" {
			t.Errorf("con la contraseña nueva: ok=%v %q", ok, out)
		}
	}
}

func TestServerPortInUse(t *testing.T) {
	s := ready(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	err = s.StartServer(ServerOptions{Host: "127.0.0.1", Port: port, User: "u", Password: "p"})
	if err == nil || !strings.Contains(err.Error(), "puerto") {
		t.Errorf("un puerto ocupado debe fallar con un mensaje claro, hubo: %v", err)
	}
	if s.State().Server.Running {
		t.Error("no debe quedar marcado como activo")
	}
}

func TestServerStopsWithNewImageOrClose(t *testing.T) {
	s := ready(t)
	port := freePort(t)
	if err := s.StartServer(ServerOptions{Host: "127.0.0.1", Port: port, User: "u", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	// Regenerar la imagen cambia los datos: el servidor viejo no puede seguir.
	if err := s.Generate(context.Background(), GenerateOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if s.State().Server.Running || canDial(port) {
		t.Error("al generar una imagen nueva el servidor debe detenerse")
	}

	if err := s.StartServer(ServerOptions{Host: "127.0.0.1", Port: port, User: "u", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if canDial(port) {
		t.Error("Close debe detener el servidor")
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---------------------------------------------------------------------------
// Explorador de archivos
// ---------------------------------------------------------------------------

func TestListDir(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "Zeta"), 0o755)
	os.Mkdir(filepath.Join(root, "alfa"), 0o755)
	os.Mkdir(filepath.Join(root, ".oculta"), 0o755)
	for _, f := range []string{"b.ACCDB", "A.mdb", "notas.txt", "x.accdb.bak", ".secreta.mdb"} {
		os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644)
	}
	os.Symlink(filepath.Join(root, "alfa"), filepath.Join(root, "enlace"))

	l, err := ListDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range l.Entries {
		names = append(names, e.Name)
	}
	want := "alfa,enlace,Zeta,A.mdb,b.ACCDB"
	if strings.Join(names, ",") != want {
		t.Errorf("entradas = %v, quiero %s (carpetas primero, solo bases de Access, sin ocultos)", names, want)
	}
	if l.Parent != filepath.Dir(root) || l.Path != root {
		t.Errorf("Path/Parent = %q / %q", l.Path, l.Parent)
	}
	for _, e := range l.Entries {
		if e.Name == "enlace" && !e.IsDir {
			t.Error("un enlace a una carpeta debe verse como carpeta")
		}
		if !e.IsDir && (e.Size == 0 || e.Modified.IsZero()) {
			t.Errorf("faltan datos del archivo %s", e.Name)
		}
	}

	// Si se pasa un archivo, se lista su carpeta.
	l2, err := ListDir(filepath.Join(root, "A.mdb"))
	if err != nil || l2.Path != root {
		t.Errorf("ListDir(archivo): %v %q", err, l2.Path)
	}
	if _, err := ListDir(filepath.Join(root, "no-existe")); err == nil {
		t.Error("una carpeta inexistente debe fallar")
	}
	if l, err := ListDir(""); err != nil || l.Path == "" {
		t.Errorf("ListDir(\"\") debe abrir la carpeta por defecto: %v", err)
	}
}

func TestListDirRootHasNoParent(t *testing.T) {
	l, err := ListDir(string(filepath.Separator))
	if err != nil {
		t.Skip("no se puede leer la raíz")
	}
	if l.Parent != "" {
		t.Errorf("la raíz no tiene carpeta superior, tiene %q", l.Parent)
	}
}

func TestIsAccessFile(t *testing.T) {
	for name, want := range map[string]bool{"a.accdb": true, "A.MDB": true, "a.mdb": true, "a.txt": false, "accdb": false, "a.accdb.bak": false} {
		if IsAccessFile(name) != want {
			t.Errorf("IsAccessFile(%q) != %v", name, want)
		}
	}
}

// Al iniciar el servidor MySQL el motor activa la autenticación para todas las
// sesiones. Las consultas propias (web, TUI, exportaciones) deben seguir
// funcionando mientras el servidor está activo y después de detenerlo.
func TestInternalQueriesWorkWhileServerRuns(t *testing.T) {
	s := ready(t)
	check := func(when string) {
		t.Helper()
		res, _, err := s.Query("SELECT COUNT(*) FROM Customers", 10)
		if err != nil || res.Rows[0][0] != int64(29) {
			t.Fatalf("%s: Query falló: %v", when, err)
		}
		if tb, err := s.Tables(); err != nil || len(tb) != 20 {
			t.Fatalf("%s: Tables falló: %v", when, err)
		}
		var b bytes.Buffer
		if err := s.ExportQuery("SELECT ID FROM Customers LIMIT 2", FormatCSV, "", 10, &b); err != nil {
			t.Fatalf("%s: ExportQuery falló: %v", when, err)
		}
		if err := s.WriteDump(context.Background(), &bytes.Buffer{}); err != nil {
			t.Fatalf("%s: WriteDump falló: %v", when, err)
		}
	}
	check("antes de iniciar")

	port := freePort(t)
	opts := ServerOptions{Host: "127.0.0.1", Port: port, User: "app", Password: "clave"}
	if err := s.StartServer(opts); err != nil {
		t.Fatal(err)
	}
	check("con el servidor activo")
	s.StopServer()
	check("después de detenerlo")

	opts.Password = "otra"
	if err := s.StartServer(opts); err != nil {
		t.Fatal(err)
	}
	check("tras reiniciar")
}

func TestInternalUserNameIsReserved(t *testing.T) {
	s := ready(t)
	err := s.StartServer(ServerOptions{Host: "127.0.0.1", Port: freePort(t), User: "accesql-internal", Password: "x"})
	if err == nil || !strings.Contains(err.Error(), "reservado") {
		t.Errorf("el usuario interno no se puede usar para el servidor: %v", err)
	}
	if _, _, err := s.Query("SELECT 1", 10); err != nil {
		t.Errorf("rechazar el nombre no debe romper las consultas: %v", err)
	}
}

func TestGenerateStateExposesProgressAndErrors(t *testing.T) {
	s := New()
	defer s.Close()
	if err := s.Open(needDB(t)); err != nil {
		t.Fatal(err)
	}
	if got := s.State().Suggested; got != "northwind" {
		t.Errorf("Suggested = %q", got)
	}

	// Mientras se genera, el estado muestra el avance.
	seen := make(chan Progress, 1)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Generate(context.Background(), GenerateOptions{}, func(p Progress) {
			if p.Number == 2 {
				seen <- p
				<-release
			}
		})
	}()
	<-seen
	st := s.State()
	if !st.Generating || st.Progress == nil || st.Progress.Number != 2 || st.Progress.Total != 20 || st.Progress.Table == "" {
		t.Errorf("estado durante la generación: %+v", st)
	}
	s.CancelGenerate()
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("cancelar debe devolver context.Canceled: %v", err)
	}
	if st := s.State(); st.Generating || st.Progress != nil || st.GenError != "" || st.ImageReady {
		t.Errorf("tras cancelar: %+v (cancelar no es un error)", st)
	}

	// Un fallo real queda guardado en el estado hasta abrir otra base.
	if err := s.Generate(context.Background(), GenerateOptions{Database: "mala base"}, nil); err == nil {
		t.Fatal("el nombre inválido debería fallar")
	}
	if got := s.State().GenError; !strings.Contains(got, "nombre de base inválido") {
		t.Errorf("GenError = %q", got)
	}
	if err := s.Open(needDB(t)); err != nil {
		t.Fatal(err)
	}
	if got := s.State().GenError; got != "" {
		t.Errorf("abrir un archivo debe limpiar el error: %q", got)
	}
	s.CancelGenerate() // sin generación en curso: no hace nada ni se cuelga
}
