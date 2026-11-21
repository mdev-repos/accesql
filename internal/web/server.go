// Package web sirve la interfaz gráfica en el navegador (HTML/CSS/JS sin
// dependencias, embebida en el ejecutable) y la API JSON que usa.
//
// La API es una capa fina sobre session.Session: cada ruta llama a un método de
// la sesión, la misma que usa la interfaz de terminal.
//
// Una página que ejecuta SQL, abre archivos y levanta un servidor es una
// superficie de ataque, así que:
//   - todas las rutas /api/ exigen un token aleatorio (cabecera Authorization);
//   - si se escucha en loopback, se valida la cabecera Host (defensa contra
//     "DNS rebinding": una web ajena que apunta su dominio a 127.0.0.1) y, si el
//     navegador manda Origin, debe ser el propio servidor (defensa contra CSRF);
//   - la imagen de la sesión ya sale blindada (solo lectura y sin acceso a
//     archivos del equipo; ver engine.Lockdown);
//   - los cuerpos de las peticiones y los resultados tienen tope de tamaño;
//   - la política CSP impide cargar scripts o estilos de otros orígenes;
//   - un pánico en un manejador se convierte en un error 500, sin tumbar el
//     servidor.
package web

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"access2mysql/internal/migrate"
	"access2mysql/internal/session"
	"access2mysql/internal/version"
)

//go:embed static
var staticFiles embed.FS

// Límites de la API.
const (
	maxBodyBytes  = 1 << 20 // cuerpo de petición
	maxSQLChars   = 200_000
	maxPathChars  = 4096
	maxTables     = 5000
	defaultLimit  = 1000
	maxLimit      = 100_000   // filas máximas que se devuelven a la pantalla
	maxExportRows = 1_000_000 // filas máximas de una exportación
	previewBytes  = 128 << 10 // tamaño máximo de la vista previa de un script
)

// Options configura el servidor.
type Options struct {
	Token string // token que deben presentar los clientes (obligatorio)
	// Port y StrictHost: si StrictHost, solo se aceptan peticiones cuyo Host
	// sea localhost / 127.0.0.1 / [::1] con ese puerto.
	Port       int
	StrictHost bool
}

type server struct {
	sess *session.Session
	opts Options
}

// New devuelve el manejador HTTP completo (estáticos + API) sobre la sesión.
func New(sess *session.Session, opts Options) (http.Handler, error) {
	if opts.Token == "" {
		return nil, errors.New("web: se requiere un token")
	}
	if sess == nil {
		return nil, errors.New("web: se requiere una sesión")
	}
	s := &server{sess: sess, opts: opts}

	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))

	api := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, s.auth(h)) }
	// Cualquier otra ruta (o método) bajo /api/ recibe un error JSON claro en
	// lugar del 404 genérico del servidor de archivos.
	mux.HandleFunc("/api/", s.auth(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "esa ruta no existe o no admite el método "+r.Method)
	}))
	api("GET /api/state", s.handleState)
	api("GET /api/files", s.handleFiles)
	api("POST /api/open", s.handleOpen)
	api("POST /api/generate", s.handleGenerate)
	api("POST /api/generate/cancel", s.handleGenerateCancel)
	api("GET /api/tables", s.handleTables)
	api("GET /api/schema", s.handleSchema)
	api("POST /api/query", s.handleQuery)
	api("POST /api/export", s.handleExport)
	api("GET /api/dump", s.handleDump)
	api("POST /api/server/start", s.handleServerStart)
	api("POST /api/server/stop", s.handleServerStop)
	api("POST /api/script/new", s.handleScriptNew)
	api("POST /api/script/add", s.handleScriptAdd)
	return s.secure(mux), nil
}

// secure aplica las cabeceras de seguridad, la validación del Host y la
// recuperación de pánicos.
func (s *server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opts.StrictHost && !s.hostAllowed(r.Host) {
			http.Error(w, "host no permitido", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cache-Control", "no-store")
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec) // es la forma de cortar la conexión a propósito: net/http la maneja sin ruido
				}
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("error interno: %v", rec))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *server) hostAllowed(host string) bool {
	host = strings.ToLower(host)
	for _, h := range []string{"localhost", "127.0.0.1", "[::1]"} {
		if host == fmt.Sprintf("%s:%d", h, s.opts.Port) {
			return true
		}
	}
	return false
}

// auth exige el token en la cabecera "Authorization: Bearer <token>".
func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.opts.Token)) != 1 {
			writeError(w, http.StatusUnauthorized, "no autorizado: falta o es incorrecto el token (abrí el enlace que imprime el programa al iniciar)")
			return
		}
		// Si el navegador manda Origin, debe ser el propio servidor.
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			writeError(w, http.StatusForbidden, "origen no permitido")
			return
		}
		next(w, r)
	}
}

// --- Utilidades ---------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail responde con el código HTTP que corresponde al error: los de estado de
// la sesión (falta abrir, falta generar, ocupada) son 409; el resto, 400.
func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, session.ErrBusy), errors.Is(err, session.ErrNoFile),
		errors.Is(err, session.ErrNoImage), errors.Is(err, session.ErrServerRunning):
		status = http.StatusConflict
	case errors.Is(err, session.ErrTooManyRows):
		status = http.StatusRequestEntityTooLarge
	}
	writeError(w, status, err.Error())
}

// decode lee el cuerpo JSON con tope de tamaño.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "petición inválida: "+err.Error())
		return false
	}
	return true
}

func setDownload(w http.ResponseWriter, contentType, filename string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeFilename(filename)+`"`)
}

// safeFilename deja solo caracteres seguros para la cabecera Content-Disposition.
func safeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 128 && (r == '.' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// baseNameNoExt devuelve el nombre del archivo abierto sin carpeta ni extensión.
func (s *server) baseNameNoExt() string {
	f := s.sess.State().File
	if f == "" {
		return "base"
	}
	return strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
}

// --- Estado y archivos --------------------------------------------------

type stateResponse struct {
	session.State
	Version string `json:"version"`
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, stateResponse{State: s.sess.State(), Version: version.Version})
}

func (s *server) handleFiles(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if len(path) > maxPathChars {
		writeError(w, http.StatusBadRequest, "ruta demasiado larga")
		return
	}
	l, err := session.ListDir(path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Path == "" || len(req.Path) > maxPathChars {
		writeError(w, http.StatusBadRequest, "elegí un archivo")
		return
	}
	if err := s.sess.Open(req.Path); err != nil {
		fail(w, err)
		return
	}
	s.handleState(w, r)
}

// handleGenerate arranca la generación en segundo plano y responde enseguida;
// la página consulta /api/state para ver el avance.
func (s *server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Database string `json:"database"`
	}
	if !decode(w, r, &req) {
		return
	}
	st := s.sess.State()
	switch {
	case st.File == "":
		fail(w, session.ErrNoFile)
		return
	case st.Generating:
		fail(w, session.ErrBusy)
		return
	}
	name := strings.TrimSpace(req.Database)
	if name == "" {
		name = st.Suggested
	}
	if !session.ValidDatabaseName(name) {
		writeError(w, http.StatusBadRequest, "el nombre de la base solo puede tener letras, números, _ y - (hasta 64 caracteres)")
		return
	}
	// El resultado (o el error) queda en el estado de la sesión.
	go s.sess.Generate(context.Background(), session.GenerateOptions{Database: name}, nil)
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": true})
}

func (s *server) handleGenerateCancel(w http.ResponseWriter, r *http.Request) {
	s.sess.CancelGenerate()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Tablas, esquema y consultas ---------------------------------------

func (s *server) handleTables(w http.ResponseWriter, r *http.Request) {
	infos, err := s.sess.Tables()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, infos)
}

func (s *server) handleSchema(w http.ResponseWriter, r *http.Request) {
	sc, err := s.sess.Schema()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

type queryRequest struct {
	SQL    string `json:"sql"`
	Limit  int    `json:"limit"`
	Format string `json:"format"` // solo /api/export: json | csv | csv-excel | sql
	Table  string `json:"table"`  // solo /api/export con format sql
}

type queryResponse struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated"`
	Count     int      `json:"count"`
	Ms        int64    `json:"ms"`
}

// readQuery decodifica y valida el cuerpo de una petición con SQL.
func readQuery(w http.ResponseWriter, r *http.Request) (queryRequest, bool) {
	var req queryRequest
	if !decode(w, r, &req) {
		return req, false
	}
	req.SQL = strings.TrimSpace(req.SQL)
	switch {
	case req.SQL == "":
		writeError(w, http.StatusBadRequest, "escribí una consulta SQL")
		return req, false
	case len(req.SQL) > maxSQLChars:
		writeError(w, http.StatusBadRequest, "la consulta es demasiado larga")
		return req, false
	}
	return req, true
}

func (s *server) handleQuery(w http.ResponseWriter, r *http.Request) {
	req, ok := readQuery(w, r)
	if !ok {
		return
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)

	start := time.Now()
	res, truncated, err := s.sess.Query(req.SQL, limit)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, queryResponse{
		Columns: res.Columns, Rows: res.Rows, Truncated: truncated,
		Count: len(res.Rows), Ms: time.Since(start).Milliseconds(),
	})
}

// handleExport devuelve el resultado completo (hasta maxExportRows) como un
// archivo descargable.
func (s *server) handleExport(w http.ResponseWriter, r *http.Request) {
	req, ok := readQuery(w, r)
	if !ok {
		return
	}
	f := session.Format(req.Format)
	if f == "" {
		f = session.FormatJSON
	}
	if len(req.Table) > 128 {
		writeError(w, http.StatusBadRequest, "el nombre de tabla es demasiado largo")
		return
	}
	// Se arma en memoria para poder responder con el código de error correcto
	// si algo falla antes de empezar a escribir.
	var buf bytes.Buffer
	if err := s.sess.ExportQuery(req.SQL, f, req.Table, maxExportRows, &buf); err != nil {
		if errors.Is(err, session.ErrTooManyRows) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("el resultado supera %d filas: acotá la consulta con LIMIT o WHERE", maxExportRows))
			return
		}
		fail(w, err)
		return
	}
	ct := map[session.Format]string{
		session.FormatJSON: "application/json; charset=utf-8", session.FormatSQL: "application/sql; charset=utf-8",
		session.FormatCSV: "text/csv; charset=utf-8", session.FormatCSVExcel: "text/csv; charset=utf-8",
	}[f]
	setDownload(w, ct, "resultado-"+time.Now().Format("20060102-150405")+f.Extension())
	w.Write(buf.Bytes())
}

// handleDump devuelve toda la base como un único JSON {tabla: {columns, rows}}.
func (s *server) handleDump(w http.ResponseWriter, r *http.Request) {
	if st := s.sess.State(); !st.ImageReady {
		fail(w, session.ErrNoImage)
		return
	}
	setDownload(w, "application/json; charset=utf-8", s.baseNameNoExt()+"-"+time.Now().Format("20060102")+".json")
	if err := s.sess.WriteDump(r.Context(), w); err != nil {
		// Ya se empezó a escribir: lo único posible es cortar la conexión.
		panic(http.ErrAbortHandler)
	}
}

// --- Servidor MySQL -----------------------------------------------------

func (s *server) handleServerStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	err := s.sess.StartServer(session.ServerOptions{Host: req.Host, Port: req.Port, User: req.User, Password: req.Password})
	if err != nil {
		fail(w, err)
		return
	}
	s.handleState(w, r)
}

func (s *server) handleServerStop(w http.ResponseWriter, r *http.Request) {
	s.sess.StopServer()
	s.handleState(w, r)
}

// --- Scripts MySQL ------------------------------------------------------

// lazyDownload fija los encabezados de descarga recién al primer byte: si el
// script falla antes de escribir algo (por ejemplo, un nombre inválido), todavía
// se puede responder con un error normal.
type lazyDownload struct {
	w        http.ResponseWriter
	filename string
	wrote    bool
}

func (l *lazyDownload) Write(b []byte) (int, error) {
	if !l.wrote {
		setDownload(l.w, "application/sql; charset=utf-8", l.filename)
		l.wrote = true
	}
	return l.w.Write(b)
}

// serveScript genera el script con gen. Con preview=true devuelve un JSON con
// el comienzo del script; si no, lo descarga completo.
func (s *server) serveScript(w http.ResponseWriter, r *http.Request, preview bool, filename string, gen func(ctx context.Context, w io.Writer) error) {
	if preview {
		text, truncated, err := session.Preview(previewBytes, func(out io.Writer) error { return gen(r.Context(), out) })
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"script": text, "truncated": truncated})
		return
	}
	ld := &lazyDownload{w: w, filename: filename}
	if err := gen(r.Context(), ld); err != nil {
		if !ld.wrote {
			fail(w, err)
			return
		}
		panic(http.ErrAbortHandler) // ya se envió parte del archivo: cortar
	}
	if !ld.wrote {
		writeError(w, http.StatusBadRequest, "el script quedó vacío")
	}
}

func (s *server) handleScriptNew(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Database    string `json:"database"`
		Data        bool   `json:"data"`
		Drop        bool   `json:"drop"`
		ForeignKeys bool   `json:"foreignKeys"`
		Preview     bool   `json:"preview"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Database)
	if !migrate.ValidDatabaseName(name) {
		writeError(w, http.StatusBadRequest, "el nombre de la base solo puede tener letras, números, _ o $ (hasta 64 caracteres)")
		return
	}
	s.serveScript(w, r, req.Preview, name+".sql", func(ctx context.Context, out io.Writer) error {
		_, err := s.sess.ScriptNew(ctx, out, migrate.NewOptions{
			Database: name, Data: req.Data, DropDatabase: req.Drop, ForeignKeys: req.ForeignKeys,
		})
		return err
	})
}

func (s *server) handleScriptAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tables []struct {
			Source  string   `json:"source"`
			Target  string   `json:"target"`
			Columns []string `json:"columns"`
		} `json:"tables"`
		OnDuplicate string `json:"onDuplicate"` // ignore | update | replace
		Transaction bool   `json:"transaction"`
		Preview     bool   `json:"preview"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Tables) == 0 || len(req.Tables) > maxTables {
		writeError(w, http.StatusBadRequest, "elegí al menos una tabla para migrar")
		return
	}
	dup, ok := map[string]migrate.Duplicate{"": migrate.DupIgnore, "ignore": migrate.DupIgnore, "update": migrate.DupUpdate, "replace": migrate.DupReplace}[req.OnDuplicate]
	if !ok {
		writeError(w, http.StatusBadRequest, "valor desconocido para qué hacer con los registros repetidos: "+req.OnDuplicate)
		return
	}
	pairs := make([]migrate.TablePair, len(req.Tables))
	for i, t := range req.Tables {
		pairs[i] = migrate.TablePair{Source: t.Source, Target: t.Target, TargetColumns: t.Columns}
	}
	s.serveScript(w, r, req.Preview, s.baseNameNoExt()+"-insert.sql", func(ctx context.Context, out io.Writer) error {
		_, err := s.sess.ScriptAdd(ctx, out, migrate.AddOptions{Tables: pairs, OnDuplicate: dup, Transaction: req.Transaction})
		return err
	})
}
