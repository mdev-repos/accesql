package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"

	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/server"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/mysql_db"
	"github.com/dolthub/vitess/go/mysql"
	"github.com/dolthub/vitess/go/sqltypes"
	querypb "github.com/dolthub/vitess/go/vt/proto/query"
)

// ServeOptions configura el servidor con protocolo MySQL.
type ServeOptions struct {
	Address  string // "host:puerto", ej. "127.0.0.1:3306"
	User     string
	Password string
}

// Server es un servidor MySQL en marcha. Se crea con StartServer y se detiene
// con Close.
type Server struct {
	e       *Engine
	srv     *server.Server
	addr    string
	user    string
	cleanup func()
	done    chan struct{} // se cierra cuando el servidor dejó de atender
	err     error         // por qué terminó (válido una vez cerrado done)
	once    sync.Once
}

// Address devuelve la dirección en la que escucha ("host:puerto").
func (s *Server) Address() string { return s.addr }

// Done se cierra cuando el servidor dejó de atender (por Close o por un error
// inesperado). Después de eso, Err dice si hubo un error.
func (s *Server) Done() <-chan struct{} { return s.done }

// Err devuelve el error que detuvo al servidor, o nil si fue por Close. Solo
// tiene sentido después de que Done se cerró.
func (s *Server) Err() error { return s.err }

// Close detiene el servidor, borra su usuario y espera a que termine. Se puede
// llamar más de una vez.
func (s *Server) Close() {
	s.once.Do(func() {
		s.srv.Close()
		<-s.done
		s.e.removeServerUser(s.user)
		s.cleanup()
	})
}

// StartServer abre el puerto y empieza a atender en segundo plano. Si el puerto
// está ocupado o la dirección es inválida, falla ACÁ (no después), así la
// interfaz puede mostrar el error al instante.
//
// El servidor es de SOLO LECTURA: la base es una foto del .accdb, y una
// escritura se perdería al reiniciar sin que nadie se entere. Es preferible
// que la app reciba un error claro.
func (e *Engine) StartServer(opts ServeOptions) (*Server, error) {
	cleanup, err := e.Lockdown()
	if err != nil {
		return nil, err
	}

	// Registramos el único usuario que puede conectarse. Al agregarlo, el
	// motor activa la autenticación: usuario o clave incorrectos se rechazan.
	if opts.User == InternalUser {
		cleanup()
		return nil, fmt.Errorf("el usuario %q está reservado, elegí otro nombre", InternalUser)
	}
	if err := e.setServerUser(opts.User, opts.Password); err != nil {
		cleanup()
		return nil, err
	}

	// Cada conexión nueva arranca con nuestra base seleccionada, para que una
	// app que no especifique base igual pueda hacer SELECT * FROM Tabla.
	base := memory.NewSessionBuilder(e.provider)
	sessionBuilder := func(c context.Context, conn *mysql.Conn, addr string) (sql.Session, error) {
		sess, err := base(c, conn, addr)
		if err != nil {
			return nil, err
		}
		sess.SetCurrentDatabase(e.dbName)
		return sess, nil
	}

	// Envolvemos el manejador de consultas para que cada una tome el candado
	// de lectura mientras se ejecuta (ver Engine.mu y Reload).
	wrap := func(h mysql.Handler) (mysql.Handler, error) {
		return &lockedHandler{Handler: h, mu: &e.mu}, nil
	}
	srv, err := server.NewServerWithHandler(
		server.Config{Protocol: "tcp", Address: opts.Address},
		e.eng, sql.NewContext, sessionBuilder, nil, wrap,
	)
	if err != nil {
		e.removeServerUser(opts.User)
		cleanup()
		return nil, fmt.Errorf("creando servidor: %w", err)
	}

	s := &Server{e: e, srv: srv, addr: opts.Address, user: opts.User, cleanup: cleanup, done: make(chan struct{})}
	// Start bloquea hasta que se cierre el servidor, por eso va en su goroutine.
	go func() {
		s.err = srv.Start()
		close(s.done)
	}()
	return s, nil
}

// Serve expone la base por el protocolo de MySQL hasta que ctx se cancele.
// Cualquier cliente MySQL (PDO, mysqli, el cliente `mysql`) puede conectarse.
// Es la versión bloqueante de StartServer, pensada para el comando `serve`.
func (e *Engine) Serve(ctx context.Context, opts ServeOptions) error {
	s, err := e.StartServer(opts)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		s.Close()
		return nil
	case <-s.Done():
		s.Close()
		return s.Err()
	}
}

// InternalUser es la cuenta que usan las consultas propias de Accesql (la web,
// la TUI, las exportaciones). Existe porque al agregar el usuario del servidor
// MySQL el motor activa la autenticación para TODAS las sesiones: sin una
// cuenta propia, esas consultas internas fallarían con "Access denied". Su
// contraseña es aleatoria y se descarta: ningún cliente de red puede usarla.
// El nombre está reservado: el servidor no acepta usarlo como usuario.
const InternalUser = "accesql-internal"

// ensureInternalUser crea la cuenta interna (una sola vez) y se la asigna a la
// sesión interna del motor. Debe llamarse con e.mu tomado para escritura.
func (e *Engine) ensureInternalUser() error {
	if e.internalReady {
		return nil
	}
	pw := make([]byte, 24)
	if _, err := rand.Read(pw); err != nil {
		return fmt.Errorf("creando la cuenta interna: %w", err)
	}
	mysqlDb := e.eng.Analyzer.Catalog.MySQLDb
	ed := mysqlDb.Editor()
	defer ed.Close()
	mysqlDb.AddEphemeralSuperUser(ed, InternalUser, "%", hex.EncodeToString(pw))
	e.ctx.Session.SetClient(sql.Client{User: InternalUser, Address: "localhost"})
	e.internalReady = true
	return nil
}

// setServerUser registra (o reemplaza) el usuario del servidor.
//
// OJO: AddEphemeralSuperUser SOLO agrega si el usuario no existe; si ya estaba
// no actualiza la clave. Sin borrarlo antes, parar el servidor y volver a
// iniciarlo con otra contraseña seguiría aceptando la vieja sin avisar.
func (e *Engine) setServerUser(user, password string) error {
	// El candado de escritura frena las consultas internas mientras cambia la
	// autenticación: ninguna debe correr a medias entre un estado y otro.
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensureInternalUser(); err != nil {
		return err
	}
	mysqlDb := e.eng.Analyzer.Catalog.MySQLDb
	ed := mysqlDb.Editor()
	defer ed.Close()
	ed.RemoveUser(mysql_db.UserPrimaryKey{Host: "%", User: user})
	mysqlDb.AddEphemeralSuperUser(ed, user, "%", password)
	return nil
}

// removeServerUser borra el usuario al detener el servidor, para que sus
// credenciales no queden vivas dentro del proceso.
func (e *Engine) removeServerUser(user string) {
	ed := e.eng.Analyzer.Catalog.MySQLDb.Editor()
	defer ed.Close()
	ed.RemoveUser(mysql_db.UserPrimaryKey{Host: "%", User: user})
}

// lockedHandler envuelve el mysql.Handler del motor. Al EMBEBER la interfaz
// (campo Handler sin nombre) hereda todos sus métodos; solo sobreescribimos los
// que ejecutan consultas, para tomar el candado de lectura durante cada una.
type lockedHandler struct {
	mysql.Handler
	mu *sync.RWMutex
}

func (h *lockedHandler) ComInitDB(c *mysql.Conn, schemaName string) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Handler.ComInitDB(c, schemaName)
}

func (h *lockedHandler) ComQuery(ctx context.Context, c *mysql.Conn, query string, callback mysql.ResultSpoolFn) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Handler.ComQuery(ctx, c, query, callback)
}

func (h *lockedHandler) ComMultiQuery(ctx context.Context, c *mysql.Conn, query string, callback mysql.ResultSpoolFn) (string, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Handler.ComMultiQuery(ctx, c, query, callback)
}

func (h *lockedHandler) ComPrepare(ctx context.Context, c *mysql.Conn, query string, prepare *mysql.PrepareData) ([]*querypb.Field, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Handler.ComPrepare(ctx, c, query, prepare)
}

func (h *lockedHandler) ComStmtExecute(ctx context.Context, c *mysql.Conn, prepare *mysql.PrepareData, callback func(*sqltypes.Result) error) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Handler.ComStmtExecute(ctx, c, prepare, callback)
}

// Lockdown deja el motor listo para exponerlo a clientes no confiables (la red
// o un navegador): solo lectura y sin acceso a archivos del servidor. Hay que
// llamarlo ANTES de aceptar consultas ajenas. Devuelve una función que limpia
// el directorio temporal usado; debe llamarse al terminar.
//
// Es idempotente: si el motor ya estaba blindado, no hace nada y devuelve una
// función vacía (el directorio lo limpia Close). Así la TUI y la web pueden
// blindar el motor al crearlo, y StartServer volver a llamarlo sin efecto.
//
// Lo usan `serve` (protocolo MySQL), `web` y la TUI: cualquier camino por el
// que lleguen consultas SQL ajenas necesita este mismo blindaje.
func (e *Engine) Lockdown() (cleanup func(), err error) {
	e.eng.ReadOnly.Store(true)

	e.lockMu.Lock()
	defer e.lockMu.Unlock()
	if e.sandbox != "" {
		return func() {}, nil
	}

	// SEGURIDAD: el motor trae LOAD_FILE(), SELECT ... INTO OUTFILE y LOAD
	// DATA INFILE, que leen/escriben archivos del servidor. Con la variable
	// secure_file_priv vacía (su valor por defecto) no tienen restricción: un
	// cliente autenticado —o una app con una inyección SQL— podría leer
	// cualquier archivo que el proceso pueda leer o crear archivos nuevos
	// (incluso en modo solo lectura, porque son SELECT). Esa variable no se
	// puede desactivar, pero sí apuntar a un directorio vacío y sin permiso de
	// escritura: así no hay nada que leer ni dónde escribir. Los clientes no
	// pueden cambiarla (no es dinámica).
	sandbox, err := os.MkdirTemp("", "access-tool-sandbox-")
	if err != nil {
		return nil, fmt.Errorf("creando directorio aislado: %w", err)
	}
	if err := os.Chmod(sandbox, 0o500); err != nil {
		os.RemoveAll(sandbox)
		return nil, err
	}
	// SetGlobal rechaza variables de solo lectura; AssignValues es la vía del
	// motor para fijar valores al arrancar.
	if err := sql.SystemVariables.AssignValues(map[string]any{"secure_file_priv": sandbox}); err != nil {
		os.RemoveAll(sandbox)
		return nil, fmt.Errorf("restringiendo acceso a archivos: %w", err)
	}
	e.sandbox = sandbox

	// SEGURIDAD: LOAD_FILE y los demás leen secure_file_priv de la SESIÓN, y una
	// sesión copia el valor global en el momento en que se crea. La sesión
	// interna del motor se creó antes de este blindaje (con la variable vacía,
	// o sea sin restricción), así que hay que recrearla ahora. Las sesiones de
	// los clientes de red se crean después y ya nacen con el valor correcto.
	e.mu.Lock()
	e.renewContext()
	e.mu.Unlock()
	return e.Close, nil
}

// renewContext reemplaza la sesión interna por una nueva, que toma los valores
// globales actuales. Debe llamarse con e.mu tomado para escritura.
func (e *Engine) renewContext() {
	session := memory.NewSession(sql.NewBaseSession(), e.provider)
	ctx := sql.NewContext(context.Background(), sql.WithSession(session))
	ctx.SetCurrentDatabase(e.dbName)
	if e.internalReady {
		ctx.Session.SetClient(sql.Client{User: InternalUser, Address: "localhost"})
	}
	e.ctx = ctx
}

// Close libera los recursos del motor ajenos a la memoria (el directorio
// aislado de Lockdown). Se puede llamar más de una vez.
func (e *Engine) Close() {
	e.lockMu.Lock()
	defer e.lockMu.Unlock()
	if e.sandbox != "" {
		os.RemoveAll(e.sandbox)
		e.sandbox = ""
	}
}
