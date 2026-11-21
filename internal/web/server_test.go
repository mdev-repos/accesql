package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"access2mysql/internal/session"

	"github.com/dolthub/go-mysql-server/sql"
)

const (
	testToken = "0123456789abcdef0123456789abcdef"
	northwind = "../../testdb/Northwind.accdb"
)

// newServer arma el manejador sobre una sesión vacía.
func newServer(t *testing.T, strict bool) (http.Handler, *session.Session) {
	t.Helper()
	sess := session.New()
	t.Cleanup(sess.Close)
	h, err := New(sess, Options{Token: testToken, Port: 8080, StrictHost: strict})
	if err != nil {
		t.Fatal(err)
	}
	return h, sess
}

// readyServer devuelve un servidor con Northwind abierto y la imagen generada.
func readyServer(t *testing.T) (http.Handler, *session.Session) {
	t.Helper()
	if _, err := os.Stat(northwind); err != nil {
		t.Skip("falta testdb/Northwind.accdb")
	}
	h, sess := newServer(t, true)
	if err := sess.Open(northwind); err != nil {
		t.Fatal(err)
	}
	if err := sess.Generate(context.Background(), session.GenerateOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	return h, sess
}

// do hace una petición con el token (salvo que auth sea "").
func do(h http.Handler, method, path, body, auth string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	return do(h, method, path, body, testToken, nil)
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("la respuesta no es JSON (%d): %v\n%.300s", rec.Code, err, rec.Body.String())
	}
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	decodeBody(t, rec, &e)
	return e.Error
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// --- Seguridad ----------------------------------------------------------

// Todas las rutas de la API, para comprobar que ninguna queda sin token.
var apiRoutes = []struct{ method, path, body string }{
	{"GET", "/api/state", ""},
	{"GET", "/api/files", ""},
	{"POST", "/api/open", `{"path":"x.accdb"}`},
	{"POST", "/api/generate", `{}`},
	{"POST", "/api/generate/cancel", ``},
	{"GET", "/api/tables", ""},
	{"GET", "/api/schema", ""},
	{"POST", "/api/query", `{"sql":"SELECT 1"}`},
	{"POST", "/api/export", `{"sql":"SELECT 1"}`},
	{"GET", "/api/dump", ""},
	{"POST", "/api/server/start", `{}`},
	{"POST", "/api/server/stop", ``},
	{"POST", "/api/script/new", `{"database":"x"}`},
	{"POST", "/api/script/add", `{"tables":[{"source":"a"}]}`},
}

func TestNewRequiresTokenAndSession(t *testing.T) {
	if _, err := New(session.New(), Options{}); err == nil {
		t.Error("sin token debería fallar")
	}
	if _, err := New(nil, Options{Token: "x"}); err == nil {
		t.Error("sin sesión debería fallar")
	}
}

func TestEveryAPIRouteRequiresToken(t *testing.T) {
	h, _ := newServer(t, true)
	for _, r := range apiRoutes {
		if rec := do(h, r.method, r.path, r.body, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s sin token: %d, se esperaba 401", r.method, r.path, rec.Code)
		}
		if rec := do(h, r.method, r.path, r.body, "token-equivocado", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s con token malo: %d, se esperaba 401", r.method, r.path, rec.Code)
		}
		if rec := do(h, r.method, r.path, r.body, testToken[:len(testToken)-1], nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s con token incompleto: %d, se esperaba 401", r.method, r.path, rec.Code)
		}
	}
	if rec := do(h, "GET", "/api/state", "", testToken, nil); rec.Code != 200 {
		t.Errorf("con token correcto: %d", rec.Code)
	}
}

func TestStaticIsPublicAndHasSecurityHeaders(t *testing.T) {
	h, _ := newServer(t, true)
	rec := do(h, "GET", "/", "", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Accesql") {
		t.Fatalf("la página principal debería servirse sin token: %d", rec.Code)
	}
	for _, f := range []string{"/app.js", "/app.css", "/logo.svg"} {
		if r := do(h, "GET", f, "", "", nil); r.Code != 200 {
			t.Errorf("%s: %d", f, r.Code)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP inesperada: %q", csp)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Cache-Control") != "no-store" ||
		rec.Header().Get("X-Frame-Options") != "DENY" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("faltan cabeceras de seguridad")
	}
	// La página no debe depender de nada externo ni llevar código en línea
	// (la CSP lo bloquearía igual).
	idx := rec.Body.String()
	for _, bad := range []string{"http://", "https://", "//cdn", "<script>", " onclick=", " style="} {
		if strings.Contains(idx, bad) {
			t.Errorf("index.html contiene %q: debe ser autosuficiente y sin código en línea", bad)
		}
	}
	js := do(h, "GET", "/app.js", "", "", nil).Body.String()
	js = strings.ReplaceAll(js, "http://www.w3.org/2000/svg", "") // espacio de nombres obligatorio de SVG
	for _, bad := range []string{"innerHTML", "eval(", "document.write", "http://", "https://"} {
		if strings.Contains(js, bad) {
			t.Errorf("app.js usa %q: el contenido se arma con textContent y nodos del DOM", bad)
		}
	}
}

func TestHostValidation(t *testing.T) {
	h, _ := newServer(t, true)
	for host, want := range map[string]int{
		"127.0.0.1:8080": 200, "localhost:8080": 200, "LOCALHOST:8080": 200, "[::1]:8080": 200,
		"evil.example.com:8080": 403, "127.0.0.1:9999": 403, "evil.example.com": 403, "127.0.0.1.evil.com:8080": 403,
	} {
		req := httptest.NewRequest("GET", "/api/state", nil)
		req.Host = host
		req.Header.Set("Authorization", "Bearer "+testToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %q: %d, se esperaba %d", host, rec.Code, want)
		}
	}
	// Sin StrictHost (escucha en otra interfaz) el Host no se restringe: manda el token.
	h2, _ := newServer(t, false)
	req := httptest.NewRequest("GET", "/api/state", nil)
	req.Host = "192.168.1.20:8080"
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("sin StrictHost: %d", rec.Code)
	}
}

func TestOriginMustMatch(t *testing.T) {
	h, _ := newServer(t, true)
	for _, r := range apiRoutes {
		rec := do(h, r.method, r.path, r.body, testToken, map[string]string{"Origin": "http://evil.example.com"})
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s con Origin ajeno: %d, se esperaba 403", r.method, r.path, rec.Code)
		}
	}
	if rec := do(h, "GET", "/api/state", "", testToken, map[string]string{"Origin": "http://127.0.0.1:8080"}); rec.Code != 200 {
		t.Errorf("Origin propio: %d", rec.Code)
	}
}

func TestWrongMethodOrUnknownRoute(t *testing.T) {
	h, _ := newServer(t, true)
	for _, r := range []struct{ method, path string }{
		{"GET", "/api/query"}, {"POST", "/api/state"}, {"DELETE", "/api/open"}, {"GET", "/api/no-existe"},
	} {
		rec := call(h, r.method, r.path, "{}")
		if rec.Code != http.StatusNotFound || errorOf(t, rec) == "" {
			t.Errorf("%s %s: %d, se esperaba 404 con error JSON", r.method, r.path, rec.Code)
		}
		// Y sin token tampoco se revela nada.
		if rec := do(h, r.method, r.path, "{}", "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s sin token: %d", r.method, r.path, rec.Code)
		}
	}
}

func TestPanicBecomesErrorButAbortIsKept(t *testing.T) {
	s := &server{opts: Options{Token: testToken}}
	boom := s.secure(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("falla inesperada") }))
	rec := httptest.NewRecorder()
	boom.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "falla inesperada") {
		t.Errorf("un pánico debe devolver 500 con el mensaje: %d %s", rec.Code, rec.Body.String())
	}

	// ErrAbortHandler es la forma de cortar la conexión a propósito: no se traga.
	abort := s.secure(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Errorf("ErrAbortHandler debe seguir propagándose, vino %v", r)
		}
	}()
	abort.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

// --- Estado, archivos y generación -------------------------------------

func TestStateStartsEmpty(t *testing.T) {
	h, _ := newServer(t, true)
	var st struct {
		File       string `json:"file"`
		ImageReady bool   `json:"imageReady"`
		Version    string `json:"version"`
	}
	decodeBody(t, call(h, "GET", "/api/state", ""), &st)
	if st.File != "" || st.ImageReady || st.Version == "" {
		t.Errorf("estado inicial: %+v", st)
	}
}

func TestOpenAndGenerateFlow(t *testing.T) {
	if _, err := os.Stat(northwind); err != nil {
		t.Skip("falta testdb/Northwind.accdb")
	}
	h, _ := newServer(t, true)

	// Sin archivo no se puede generar.
	if rec := call(h, "POST", "/api/generate", `{}`); rec.Code != http.StatusConflict {
		t.Errorf("generar sin archivo: %d", rec.Code)
	}
	// Archivos inválidos.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notas.txt"), []byte("x"), 0o644)
	for name, body := range map[string]string{
		"sin ruta":        `{"path":""}`,
		"no existe":       `{"path":"` + filepath.Join(dir, "nada.accdb") + `"}`,
		"extensión ajena": `{"path":"` + filepath.Join(dir, "notas.txt") + `"}`,
		"json inválido":   `{`,
		"ruta enorme":     `{"path":"` + strings.Repeat("a", maxPathChars+1) + `"}`,
	} {
		if rec := call(h, "POST", "/api/open", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, se esperaba 400", name, rec.Code)
		}
	}

	abs, _ := filepath.Abs(northwind)
	rec := call(h, "POST", "/api/open", fmt.Sprintf(`{"path":%q}`, abs))
	var st struct {
		File      string `json:"file"`
		Suggested string `json:"suggestedDatabase"`
	}
	decodeBody(t, rec, &st)
	if rec.Code != 200 || st.File != abs || st.Suggested != "northwind" {
		t.Fatalf("open: %d %+v", rec.Code, st)
	}

	// Nombre inválido.
	if rec := call(h, "POST", "/api/generate", `{"database":"mala base"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("nombre inválido: %d", rec.Code)
	}

	// Generar: responde enseguida y se sigue por el estado.
	if rec := call(h, "POST", "/api/generate", `{}`); rec.Code != http.StatusAccepted {
		t.Fatalf("generate: %d %s", rec.Code, rec.Body.String())
	}
	var final struct {
		Generating bool   `json:"generating"`
		ImageReady bool   `json:"imageReady"`
		Database   string `json:"database"`
		Tables     int    `json:"tables"`
		Rows       int    `json:"rows"`
		GenError   string `json:"generateError"`
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		decodeBody(t, call(h, "GET", "/api/state", ""), &final)
		if final.ImageReady || final.GenError != "" || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !final.ImageReady || final.Generating || final.Database != "northwind" || final.Tables != 20 || final.Rows < 100 {
		t.Fatalf("estado final: %+v", final)
	}
}

func TestGenerateCancelWithoutRunningIsHarmless(t *testing.T) {
	h, _ := newServer(t, true)
	if rec := call(h, "POST", "/api/generate/cancel", ``); rec.Code != 200 {
		t.Errorf("cancelar sin generación: %d", rec.Code)
	}
}

func TestFilesListing(t *testing.T) {
	h, _ := newServer(t, true)
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "a.accdb"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x"), 0o644)

	rec := call(h, "GET", "/api/files?path="+dir, "")
	var l struct {
		Path      string `json:"path"`
		HasParent bool   `json:"hasParent"`
		Entries   []struct {
			Name  string `json:"name"`
			IsDir bool   `json:"isDir"`
		} `json:"entries"`
	}
	decodeBody(t, rec, &l)
	if rec.Code != 200 || l.Path != dir || !l.HasParent || len(l.Entries) != 2 || l.Entries[0].Name != "sub" || !l.Entries[0].IsDir || l.Entries[1].Name != "a.accdb" {
		t.Errorf("listado: %d %+v", rec.Code, l)
	}
	if rec := call(h, "GET", "/api/files?path="+filepath.Join(dir, "no-existe"), ""); rec.Code != http.StatusBadRequest {
		t.Errorf("carpeta inexistente: %d", rec.Code)
	}
	if rec := call(h, "GET", "/api/files?path="+strings.Repeat("a", maxPathChars+1), ""); rec.Code != http.StatusBadRequest {
		t.Errorf("ruta enorme: %d", rec.Code)
	}
	if rec := call(h, "GET", "/api/files", ""); rec.Code != 200 {
		t.Errorf("sin ruta debe abrir la carpeta por defecto: %d", rec.Code)
	}
}

func TestEverythingNeedsAnImage(t *testing.T) {
	h, _ := newServer(t, true)
	for _, r := range []struct{ method, path, body string }{
		{"GET", "/api/tables", ""}, {"GET", "/api/schema", ""}, {"GET", "/api/dump", ""},
		{"POST", "/api/query", `{"sql":"SELECT 1"}`}, {"POST", "/api/export", `{"sql":"SELECT 1"}`},
		{"POST", "/api/server/start", `{"host":"127.0.0.1","port":3306,"user":"u","password":"p"}`},
		{"POST", "/api/script/new", `{"database":"x"}`}, {"POST", "/api/script/add", `{"tables":[{"source":"a"}]}`},
	} {
		if rec := call(h, r.method, r.path, r.body); rec.Code != http.StatusConflict {
			t.Errorf("%s %s sin imagen: %d, se esperaba 409 (%s)", r.method, r.path, rec.Code, rec.Body.String())
		}
	}
}

// --- Tablas, esquema y consultas ---------------------------------------

func TestTablesAndSchema(t *testing.T) {
	h, _ := readyServer(t)
	var tables []struct {
		Name    string `json:"name"`
		Rows    int    `json:"rows"`
		Columns []struct {
			Name       string `json:"name"`
			AccessType string `json:"accessType"`
			Size       int    `json:"size"`
		} `json:"columns"`
	}
	decodeBody(t, call(h, "GET", "/api/tables", ""), &tables)
	if len(tables) != 20 {
		t.Fatalf("tablas: %d", len(tables))
	}
	for _, tb := range tables {
		if tb.Name == "Customers" && (tb.Rows != 29 || tb.Columns[0].Name != "ID") {
			t.Errorf("Customers: %+v", tb)
		}
	}

	var sc struct {
		Tables []struct {
			Name    string `json:"name"`
			Columns []struct {
				Name string `json:"name"`
				PK   bool   `json:"pk"`
				FK   bool   `json:"fk"`
			} `json:"columns"`
		} `json:"tables"`
		Relations []struct {
			Table      string   `json:"table"`
			Columns    []string `json:"columns"`
			RefTable   string   `json:"refTable"`
			RefColumns []string `json:"refColumns"`
			Enforced   bool     `json:"enforced"`
		} `json:"relations"`
	}
	decodeBody(t, call(h, "GET", "/api/schema", ""), &sc)
	if len(sc.Tables) != 20 || len(sc.Relations) < 5 {
		t.Fatalf("esquema: %d tablas, %d relaciones", len(sc.Tables), len(sc.Relations))
	}
	var found bool
	for _, r := range sc.Relations {
		if r.Table == "Orders" && r.RefTable == "Customers" && r.Columns[0] == "Customer ID" {
			found = true
		}
	}
	if !found {
		t.Error("falta la relación Orders → Customers")
	}
}

func TestQuery(t *testing.T) {
	h, _ := readyServer(t)
	var res struct {
		Columns   []string `json:"columns"`
		Rows      [][]any  `json:"rows"`
		Truncated bool     `json:"truncated"`
		Count     int      `json:"count"`
	}
	rec := call(h, "POST", "/api/query", `{"sql":"SELECT ID, Company FROM Customers ORDER BY ID","limit":5}`)
	decodeBody(t, rec, &res)
	if rec.Code != 200 || res.Count != 5 || !res.Truncated || res.Columns[1] != "Company" || res.Rows[0][1] != "Company A" {
		t.Errorf("consulta con límite: %d %+v", rec.Code, res)
	}

	// Sin límite explícito usa el de defecto.
	rec = call(h, "POST", "/api/query", `{"sql":"SELECT * FROM Customers"}`)
	decodeBody(t, rec, &res)
	if res.Count != 29 || res.Truncated {
		t.Errorf("límite por defecto: %+v", res.Count)
	}

	// Cada consulta reemplaza a la anterior.
	rec = call(h, "POST", "/api/query", `{"sql":"SELECT COUNT(*) AS n FROM Orders"}`)
	decodeBody(t, rec, &res)
	if len(res.Columns) != 1 || res.Columns[0] != "n" {
		t.Errorf("segunda consulta: %+v", res)
	}
}

func TestQueryValidationAndErrors(t *testing.T) {
	h, _ := readyServer(t)
	huge := `{"sql":"` + strings.Repeat("a", maxSQLChars+1) + `"}`
	for name, body := range map[string]string{
		"vacía":             `{"sql":"   "}`,
		"json inválido":     `{"sql":`,
		"demasiado larga":   huge,
		"cuerpo enorme":     `{"sql":"` + strings.Repeat("a", maxBodyBytes+10) + `"}`,
		"sintaxis":          `{"sql":"SELEC nada"}`,
		"tabla inexistente": `{"sql":"SELECT * FROM NoExiste"}`,
		"escritura":         `{"sql":"DELETE FROM Customers"}`,
	} {
		rec := call(h, "POST", "/api/query", body)
		if rec.Code != http.StatusBadRequest || errorOf(t, rec) == "" {
			t.Errorf("%s: %d, se esperaba 400 con mensaje", name, rec.Code)
		}
	}
	var res struct{ Count int }
	decodeBody(t, call(h, "POST", "/api/query", `{"sql":"SELECT COUNT(*) FROM Customers"}`), &res)
	if res.Count != 1 {
		t.Error("tras los errores el servidor debe seguir funcionando")
	}
}

// El blindaje debe valer también a través de HTTP y en un proceso recién
// iniciado (la variable global arranca vacía).
func TestHTTPQueriesCannotTouchFiles(t *testing.T) {
	if err := sql.SystemVariables.AssignValues(map[string]any{"secure_file_priv": ""}); err != nil {
		t.Fatal(err)
	}
	h, _ := readyServer(t)
	secret := filepath.Join(t.TempDir(), "secreto.txt")
	os.WriteFile(secret, []byte("contenido-secreto"), 0o644)

	q := func(sqlText string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"sql": sqlText})
		return call(h, "POST", "/api/query", string(b))
	}
	var res struct{ Rows [][]any }
	decodeBody(t, q("SELECT LOAD_FILE('"+secret+"')"), &res)
	if len(res.Rows) == 1 && res.Rows[0][0] != nil {
		t.Errorf("LOAD_FILE leyó un archivo por HTTP: %v", res.Rows[0][0])
	}
	out := filepath.Join(t.TempDir(), "fuga.txt")
	q("SELECT * FROM Customers INTO OUTFILE '" + out + "'")
	if _, err := os.Stat(out); err == nil {
		t.Error("INTO OUTFILE creó un archivo por HTTP")
	}
}

// --- Exportaciones ------------------------------------------------------

func TestExportFormats(t *testing.T) {
	h, _ := readyServer(t)
	q := `SELECT ID, Company FROM Customers ORDER BY ID LIMIT 3`
	body := func(format, table string) string {
		b, _ := json.Marshal(map[string]string{"sql": q, "format": format, "table": table})
		return string(b)
	}

	rec := call(h, "POST", "/api/export", body("json", ""))
	var rows []map[string]any
	decodeBody(t, rec, &rows)
	if rec.Code != 200 || len(rows) != 3 || !strings.HasSuffix(rec.Header().Get("Content-Disposition"), `.json"`) {
		t.Errorf("json: %d %v %s", rec.Code, rows, rec.Header().Get("Content-Disposition"))
	}

	rec = call(h, "POST", "/api/export", body("csv", ""))
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "ID,Company\n1,Company A\n") || !strings.HasSuffix(rec.Header().Get("Content-Disposition"), `.csv"`) {
		t.Errorf("csv: %d %q", rec.Code, rec.Body.String())
	}
	rec = call(h, "POST", "/api/export", body("csv-excel", ""))
	if !strings.HasPrefix(rec.Body.String(), "\xEF\xBB\xBFID;Company\n") {
		t.Errorf("csv-excel debe llevar BOM y ';': %q", rec.Body.String())
	}
	rec = call(h, "POST", "/api/export", body("sql", "mis clientes"))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "INSERT INTO `mis clientes` (`ID`, `Company`) VALUES") || !strings.HasSuffix(rec.Header().Get("Content-Disposition"), `.sql"`) {
		t.Errorf("sql: %d %q", rec.Code, rec.Body.String())
	}
	if rec := call(h, "POST", "/api/export", body("sql", "")); !strings.Contains(rec.Body.String(), "`resultado`") {
		t.Error("sin nombre de tabla debe usar 'resultado'")
	}
	if rec := call(h, "POST", "/api/export", body("xml", "")); rec.Code != http.StatusBadRequest {
		t.Errorf("formato desconocido: %d", rec.Code)
	}
	if rec := call(h, "POST", "/api/export", body("sql", strings.Repeat("t", 200))); rec.Code != http.StatusBadRequest {
		t.Errorf("nombre de tabla enorme: %d", rec.Code)
	}
	if rec := call(h, "POST", "/api/export", `{"sql":"SELEC x","format":"json"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("consulta inválida: %d", rec.Code)
	}
}

func TestDump(t *testing.T) {
	h, _ := readyServer(t)
	rec := call(h, "GET", "/api/dump", "")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "Northwind-") {
		t.Fatalf("dump: %d %s", rec.Code, rec.Header().Get("Content-Disposition"))
	}
	var all map[string][]map[string]any
	decodeBody(t, rec, &all)
	if len(all) != 20 || len(all["Customers"]) != 29 {
		t.Errorf("volcado: %d tablas", len(all))
	}
}

// --- Servidor MySQL -----------------------------------------------------

func TestServerStartStop(t *testing.T) {
	h, _ := readyServer(t)
	port := freePort(t)
	start := func(user, pass string, p int) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]any{"host": "127.0.0.1", "port": p, "user": user, "password": pass})
		return call(h, "POST", "/api/server/start", string(b))
	}

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"sin contraseña":    start("app", "", port),
		"puerto inválido":   start("app", "x", 0),
		"sin usuario":       start("", "x", port),
		"usuario reservado": start("accesql-internal", "x", port),
	} {
		if rec.Code != http.StatusBadRequest || errorOf(t, rec) == "" {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}

	rec := start("app", "clave-secreta", port)
	var st struct {
		Server struct {
			Running  bool   `json:"running"`
			Address  string `json:"address"`
			User     string `json:"user"`
			Database string `json:"database"`
		} `json:"server"`
	}
	decodeBody(t, rec, &st)
	if rec.Code != 200 || !st.Server.Running || st.Server.Address != "127.0.0.1:"+strconv.Itoa(port) || st.Server.Database != "access" {
		t.Fatalf("start: %d %+v", rec.Code, st)
	}
	if strings.Contains(rec.Body.String(), "clave-secreta") {
		t.Error("la contraseña no debe volver en la respuesta")
	}
	if got := call(h, "GET", "/api/state", "").Body.String(); strings.Contains(got, "clave-secreta") {
		t.Error("la contraseña no debe aparecer en el estado")
	}
	if rec := start("app", "otra", port); rec.Code != http.StatusConflict {
		t.Errorf("segundo start: %d", rec.Code)
	}

	// Mientras el servidor está activo, las consultas de la página siguen funcionando.
	var res struct{ Count int }
	decodeBody(t, call(h, "POST", "/api/query", `{"sql":"SELECT * FROM Customers"}`), &res)
	if res.Count != 29 {
		t.Errorf("consulta con el servidor activo: %d filas", res.Count)
	}

	decodeBody(t, call(h, "POST", "/api/server/stop", ``), &st)
	if st.Server.Running {
		t.Error("stop debe detenerlo")
	}
	if rec := call(h, "POST", "/api/server/stop", ``); rec.Code != 200 {
		t.Errorf("stop repetido: %d", rec.Code)
	}
}

func TestServerPortInUse(t *testing.T) {
	h, _ := readyServer(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	rec := call(h, "POST", "/api/server/start", fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"user":"u","password":"p"}`, port))
	if rec.Code != http.StatusBadRequest || !strings.Contains(errorOf(t, rec), "puerto") {
		t.Errorf("puerto ocupado: %d %s", rec.Code, rec.Body.String())
	}
}

// --- Scripts MySQL ------------------------------------------------------

func TestScriptNew(t *testing.T) {
	h, _ := readyServer(t)
	var pv struct {
		Script    string `json:"script"`
		Truncated bool   `json:"truncated"`
	}
	rec := call(h, "POST", "/api/script/new", `{"database":"northwind","data":true,"foreignKeys":true,"preview":true}`)
	decodeBody(t, rec, &pv)
	if rec.Code != 200 || pv.Truncated || !strings.HasPrefix(pv.Script, "-- Accesql ") ||
		!strings.Contains(pv.Script, "FOREIGN KEY") || !strings.Contains(pv.Script, "CREATE DATABASE `northwind`") {
		t.Errorf("vista previa: %d truncated=%v %.120s", rec.Code, pv.Truncated, pv.Script)
	}

	rec = call(h, "POST", "/api/script/new", `{"database":"northwind","data":true,"foreignKeys":true,"preview":false}`)
	if rec.Code != 200 || rec.Header().Get("Content-Disposition") != `attachment; filename="northwind.sql"` ||
		rec.Body.String() != pv.Script {
		t.Errorf("descarga: %d %s (¿igual a la vista previa? %v)", rec.Code, rec.Header().Get("Content-Disposition"), rec.Body.String() == pv.Script)
	}

	for name, body := range map[string]string{
		"nombre inválido": `{"database":"mala base","preview":true}`,
		"sin nombre":      `{"preview":true}`,
		"json inválido":   `{`,
	} {
		if rec := call(h, "POST", "/api/script/new", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	// Un fallo antes de escribir debe ser un error normal, no un archivo roto.
	rec = call(h, "POST", "/api/script/new", `{"database":"x y","preview":false}`)
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Disposition") != "" {
		t.Errorf("fallo en descarga: %d %q", rec.Code, rec.Header().Get("Content-Disposition"))
	}
}

func TestScriptAdd(t *testing.T) {
	h, _ := readyServer(t)
	var pv struct {
		Script string `json:"script"`
	}
	body := `{"tables":[{"source":"Customers","target":"CLIENTE","columns":["id","company","extra"]}],"onDuplicate":"update","transaction":true,"preview":true}`
	rec := call(h, "POST", "/api/script/add", body)
	decodeBody(t, rec, &pv)
	for _, want := range []string{"START TRANSACTION;", "INSERT INTO `CLIENTE` (`ID`, `Company`) VALUES", "ON DUPLICATE KEY UPDATE `Company` = VALUES(`Company`);", "COMMIT;"} {
		if !strings.Contains(pv.Script, want) {
			t.Errorf("falta %q en:\n%.400s", want, pv.Script)
		}
	}

	rec = call(h, "POST", "/api/script/add", strings.Replace(body, `"preview":true`, `"preview":false`, 1))
	if rec.Code != 200 || rec.Header().Get("Content-Disposition") != `attachment; filename="Northwind-insert.sql"` {
		t.Errorf("descarga: %d %s", rec.Code, rec.Header().Get("Content-Disposition"))
	}

	for name, b := range map[string]string{
		"sin tablas":          `{"tables":[],"preview":true}`,
		"duplicados inválido": `{"tables":[{"source":"Customers"}],"onDuplicate":"borrar","preview":true}`,
		"tabla inexistente":   `{"tables":[{"source":"Nada"}],"preview":true}`,
		"sin coincidencias":   `{"tables":[{"source":"Customers","columns":["zzz"]}],"preview":true}`,
	} {
		if rec := call(h, "POST", "/api/script/add", b); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestPreviewBufferCutsAtLineBoundary(t *testing.T) {
	var p previewBuf
	line := strings.Repeat("x", 99) + "\n"
	var err error
	for i := 0; i < 2000 && err == nil; i++ {
		_, err = p.Write([]byte(line))
	}
	if !errors.Is(err, errPreviewFull) || !p.full {
		t.Fatalf("debería llenarse y cortar: err=%v full=%v", err, p.full)
	}
	txt := p.text()
	if len(txt) > previewBytes || !strings.HasSuffix(txt, "\n") || strings.Contains(txt, "xx\n"+strings.Repeat("x", 10)+"\n"+"y") {
		t.Errorf("la vista previa debe terminar en un salto de línea completo (%d bytes)", len(txt))
	}
	for _, l := range strings.Split(strings.TrimSuffix(txt, "\n"), "\n") {
		if len(l) != 99 {
			t.Fatalf("hay una línea cortada a la mitad: %d caracteres", len(l))
		}
	}

	var small previewBuf
	small.Write([]byte("poco\n"))
	if small.full || small.text() != "poco\n" {
		t.Errorf("un script chico debe salir completo: %+v", small)
	}
}

func TestLazyDownloadSetsHeadersOnFirstWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	ld := &lazyDownload{w: rec, filename: "mi base.sql"}
	if rec.Header().Get("Content-Disposition") != "" {
		t.Error("no debe fijar encabezados antes de escribir")
	}
	ld.Write([]byte("x"))
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="mi_base.sql"` {
		t.Errorf("Content-Disposition = %q (el nombre debe quedar saneado)", got)
	}
}

func TestSafeFilename(t *testing.T) {
	for in, want := range map[string]string{
		"ok-1_2.sql":       "ok-1_2.sql",
		`a"b\c/d` + "\r\n": "a_b_c_d__",
		"ñandú.json":       "_and_.json",
	} {
		if got := safeFilename(in); got != want {
			t.Errorf("safeFilename(%q) = %q, quiero %q", in, got, want)
		}
	}
}
