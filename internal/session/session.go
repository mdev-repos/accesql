// Package session es el "cerebro" compartido de la interfaz web y la de
// terminal: guarda el estado (qué base está abierta, si la imagen MySQL está
// generada, si el servidor está activo) y ofrece cada operación como un método.
//
// Las dos interfaces solo hablan con Session; ninguna toca el motor ni el
// lector de Access. Así se comportan igual y cualquier arreglo o mejora se
// hace una sola vez. Todos los métodos son seguros para llamar desde varias
// goroutines.
package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"access2mysql/internal/access"
	"access2mysql/internal/engine"
)

// dbNameRe valida el nombre de la base que ven los clientes MySQL.
var dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_$-]{1,64}$`)

// Errores que las interfaces pueden mostrar tal cual.
var (
	ErrNoFile        = errors.New("primero abrí una base de datos")
	ErrNoImage       = errors.New("primero generá la imagen MySQL")
	ErrBusy          = errors.New("hay una generación en curso, esperá a que termine")
	ErrServerRunning = errors.New("el servidor ya está activo")
)

// Progress informa el avance de la generación de la imagen.
type Progress struct {
	Table  string `json:"table"`  // tabla que se está leyendo
	Number int    `json:"number"` // número de esa tabla (desde 1)
	Total  int    `json:"total"`  // total de tablas
}

// GenerateOptions configura Generate.
type GenerateOptions struct {
	// Database es el nombre de la base dentro del motor: es el que usarán los
	// clientes MySQL ("dbname=..."). Vacío = "access". No se puede cambiar
	// después de generar la imagen.
	Database string
}

// ServerOptions configura StartServer.
type ServerOptions struct {
	Host     string // "127.0.0.1" = solo esta computadora, "0.0.0.0" = toda la red
	Port     int
	User     string
	Password string // obligatoria
}

// ServerState describe el servidor MySQL integrado. Nunca incluye la contraseña.
type ServerState struct {
	Running  bool   `json:"running"`
	Address  string `json:"address,omitempty"` // "host:puerto"
	User     string `json:"user,omitempty"`
	Database string `json:"database,omitempty"`
	Error    string `json:"error,omitempty"` // por qué se detuvo solo, si pasó
}

// State es una foto del estado de la sesión, lista para mostrar.
type State struct {
	File       string      `json:"file"` // ruta de la base abierta ("" = ninguna)
	Generating bool        `json:"generating"`
	Progress   *Progress   `json:"progress,omitempty"`      // avance mientras Generating
	GenError   string      `json:"generateError,omitempty"` // por qué falló la última generación
	Suggested  string      `json:"suggestedDatabase,omitempty"`
	ImageReady bool        `json:"imageReady"`
	Database   string      `json:"database,omitempty"`
	Tables     int         `json:"tables"`
	Rows       int64       `json:"rows"`
	LoadedAt   time.Time   `json:"loadedAt,omitempty"`
	Server     ServerState `json:"server"`
}

// Session es el estado y las operaciones de una base abierta.
type Session struct {
	mu         sync.Mutex // protege los campos de abajo
	file       string
	eng        *engine.Engine
	database   string
	tables     int
	rows       int64
	loadedAt   time.Time
	server     *engine.Server
	srv        ServerState
	schema     *Schema // cache del diagrama; se descarta con la imagen
	generating bool
	progress   *Progress
	genErr     string
	cancelGen  context.CancelFunc

	// qmu serializa el uso del motor desde las interfaces (consultas, lecturas
	// para exportar): el motor comparte una sesión SQL interna y no admite
	// varias a la vez. El servidor MySQL no pasa por acá: cada conexión tiene
	// la suya.
	qmu sync.Mutex
}

// New crea una sesión vacía.
func New() *Session { return &Session{} }

// State devuelve el estado actual.
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := State{
		File: s.file, Generating: s.generating, ImageReady: s.eng != nil, GenError: s.genErr,
		Database: s.database, Tables: s.tables, Rows: s.rows, LoadedAt: s.loadedAt, Server: s.srv,
	}
	if s.progress != nil {
		p := *s.progress
		st.Progress = &p
	}
	if s.file != "" {
		st.Suggested = suggestName(s.file)
	}
	return st
}

// ValidDatabaseName dice si es un nombre válido para la base que ven los
// clientes MySQL.
func ValidDatabaseName(name string) bool { return dbNameRe.MatchString(name) }

// CancelGenerate pide cancelar la generación en curso (no hace nada si no hay).
func (s *Session) CancelGenerate() {
	s.mu.Lock()
	cancel := s.cancelGen
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// IsAccessFile dice si el nombre tiene extensión de base de Access.
func IsAccessFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".accdb", ".mdb":
		return true
	}
	return false
}

// Open elige la base de Access a usar. Comprueba que se pueda leer, pero no
// carga los datos: eso lo hace Generate. Si ya había una imagen (u otro
// servidor activo), se descartan: pertenecían a la base anterior.
func (s *Session) Open(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if !IsAccessFile(abs) {
		return fmt.Errorf("%q no es una base de Access (.accdb o .mdb)", filepath.Base(abs))
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("no se puede abrir %q: %w", filepath.Base(abs), unwrapPathErr(err))
	}
	if fi.IsDir() {
		return fmt.Errorf("%q es una carpeta, no un archivo", filepath.Base(abs))
	}

	s.mu.Lock()
	if s.generating {
		s.mu.Unlock()
		return ErrBusy
	}
	s.mu.Unlock()

	// Comprobar que el archivo se pueda leer antes de descartar lo anterior.
	db, err := safeOpen(abs)
	if err != nil {
		return err
	}
	db.Close()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generating {
		return ErrBusy
	}
	s.dropImageLocked()
	s.file, s.genErr = abs, ""
	return nil
}

// Generate lee la base abierta y arma la "imagen MySQL" en memoria. Informa el
// avance tabla por tabla y se puede cancelar con ctx. La imagen queda
// blindada: solo lectura y sin acceso a archivos del equipo.
func (s *Session) Generate(ctx context.Context, opts GenerateOptions, onProgress func(Progress)) (err error) {
	s.mu.Lock()
	if s.file == "" {
		s.mu.Unlock()
		return ErrNoFile
	}
	if s.generating {
		s.mu.Unlock()
		return ErrBusy
	}
	ctx, cancel := context.WithCancel(ctx)
	s.generating, s.progress, s.genErr, s.cancelGen = true, nil, "", cancel
	file := s.file
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.generating, s.progress, s.cancelGen = false, nil, nil
		if err != nil && !errors.Is(err, context.Canceled) {
			s.genErr = err.Error() // la interfaz lo muestra aunque nadie esté esperando el retorno
		}
		s.mu.Unlock()
	}()

	name := strings.TrimSpace(opts.Database)
	if name == "" {
		name = engine.DefaultDatabase
	}
	if !dbNameRe.MatchString(name) {
		return fmt.Errorf("nombre de base inválido %q: usá letras, números, _ o -", name)
	}

	// Un archivo corrupto puede hacer fallar al lector con un pánico: se
	// convierte en error para no tumbar la aplicación.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("no se pudo leer el archivo (¿está corrupto?): %v", r)
		}
	}()

	src, err := access.Open(file)
	if err != nil {
		return err
	}
	defer src.Close()

	cb := func(table string, n, total int) {
		p := Progress{Table: table, Number: n, Total: total}
		s.mu.Lock()
		s.progress = &p
		s.mu.Unlock()
		if onProgress != nil {
			onProgress(p)
		}
	}
	eng, err := engine.NewWithProgress(ctx, src, name, cb)
	if err != nil {
		return err
	}
	if _, err := eng.Lockdown(); err != nil { // la limpieza la hace eng.Close
		return err
	}

	infos, err := eng.TableInfos()
	if err != nil {
		eng.Close()
		return err
	}
	var rows int64
	for _, t := range infos {
		rows += t.Rows
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != file { // se abrió otra base mientras tanto (no debería: Open lo impide)
		eng.Close()
		return errors.New("la base abierta cambió durante la generación")
	}
	s.dropImageLocked()
	s.eng, s.database, s.tables, s.rows, s.loadedAt = eng, name, len(infos), rows, time.Now()
	return nil
}

// dropImageLocked descarta la imagen y detiene el servidor. s.mu debe estar tomado.
func (s *Session) dropImageLocked() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
	s.srv = ServerState{}
	if s.eng != nil {
		s.eng.Close()
		s.eng = nil
	}
	s.database, s.tables, s.rows, s.loadedAt, s.schema = "", 0, 0, time.Time{}, nil
}

// Close libera todo: detiene el servidor y descarta la imagen.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropImageLocked()
}

// engine devuelve el motor, o ErrNoImage.
func (s *Session) engine() (*engine.Engine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eng == nil {
		if s.file == "" {
			return nil, ErrNoFile
		}
		return nil, ErrNoImage
	}
	return s.eng, nil
}

// Tables devuelve las tablas de la imagen con sus columnas y cantidad de filas.
func (s *Session) Tables() ([]engine.TableInfo, error) {
	e, err := s.engine()
	if err != nil {
		return nil, err
	}
	s.qmu.Lock()
	defer s.qmu.Unlock()
	return guard(func() ([]engine.TableInfo, error) { return e.TableInfos() })
}

// Query ejecuta SQL (dialecto MySQL) sobre la imagen y devuelve como máximo max
// filas (max <= 0 = sin límite). truncated indica que había más.
func (s *Session) Query(sqlText string, max int) (*engine.Result, bool, error) {
	if strings.TrimSpace(sqlText) == "" {
		return nil, false, errors.New("escribí una consulta")
	}
	e, err := s.engine()
	if err != nil {
		return nil, false, err
	}
	s.qmu.Lock()
	defer s.qmu.Unlock()
	type out struct {
		res *engine.Result
		tr  bool
	}
	o, err := guard(func() (out, error) {
		r, tr, err := e.QueryN(sqlText, max)
		return out{r, tr}, err
	})
	return o.res, o.tr, err
}

// guard ejecuta f convirtiendo un pánico del motor en un error normal.
func guard[T any](f func() (T, error)) (v T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("error interno del motor SQL: %v", r)
		}
	}()
	return f()
}

func safeOpen(path string) (db *access.DB, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("no se pudo leer %q (¿está corrupto?): %v", filepath.Base(path), r)
		}
	}()
	return access.Open(path)
}

func unwrapPathErr(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// ---------------------------------------------------------------------------
// Servidor MySQL
// ---------------------------------------------------------------------------

// ValidateServer revisa las opciones del servidor y devuelve el primer problema
// en un mensaje claro ("" si están bien). Las interfaces lo usan para avisar
// antes de intentar iniciarlo.
func ValidateServer(o ServerOptions) string {
	switch {
	case strings.TrimSpace(o.Host) == "":
		return "Escribí la dirección."
	case o.Port < 1 || o.Port > 65535:
		return "El puerto debe ser un número entre 1 y 65535."
	case strings.TrimSpace(o.User) == "":
		return "Escribí el usuario."
	case strings.TrimSpace(o.User) == engine.InternalUser:
		return "Ese nombre de usuario está reservado, elegí otro."
	case o.Password == "":
		return "La contraseña es obligatoria: sin ella cualquiera podría leer los datos."
	}
	return ""
}

// StartServer expone la imagen como servidor MySQL. Falla enseguida si el
// puerto está ocupado.
func (s *Session) StartServer(o ServerOptions) error {
	if msg := ValidateServer(o); msg != "" {
		return errors.New(msg)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eng == nil {
		if s.file == "" {
			return ErrNoFile
		}
		return ErrNoImage
	}
	if s.server != nil {
		return ErrServerRunning
	}
	addr := net.JoinHostPort(strings.TrimSpace(o.Host), strconv.Itoa(o.Port))
	srv, err := s.eng.StartServer(engine.ServeOptions{Address: addr, User: strings.TrimSpace(o.User), Password: o.Password})
	if err != nil {
		return fmt.Errorf("no se pudo iniciar el servidor en %s: %w", addr, unwrapListenErr(err))
	}
	s.server = srv
	s.srv = ServerState{Running: true, Address: addr, User: strings.TrimSpace(o.User), Database: s.database}

	// Si el servidor se cae solo, reflejarlo en el estado.
	go func() {
		<-srv.Done()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.server == srv {
			s.server = nil
			s.srv = ServerState{}
			if err := srv.Err(); err != nil {
				s.srv.Error = err.Error()
			}
		}
	}()
	return nil
}

// StopServer detiene el servidor (no hace nada si no estaba activo).
func (s *Session) StopServer() {
	s.mu.Lock()
	srv := s.server
	s.server = nil
	s.srv = ServerState{}
	s.mu.Unlock()
	if srv != nil {
		srv.Close()
	}
}

func unwrapListenErr(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "address already in use") || strings.Contains(msg, "Only one usage of each socket") {
		return errors.New("el puerto ya está en uso por otro programa")
	}
	return err
}
