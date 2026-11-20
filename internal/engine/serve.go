package engine

import (
	"context"
	"fmt"
	"os"

	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/server"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/vitess/go/mysql"
)

// ServeOptions configura el servidor con protocolo MySQL.
type ServeOptions struct {
	Address  string // "host:puerto", ej. "127.0.0.1:3306"
	User     string
	Password string
}

// Serve expone la base por el protocolo de MySQL hasta que ctx se cancele.
// Cualquier cliente MySQL (PDO, mysqli, el cliente `mysql`) puede conectarse.
//
// El servidor es de SOLO LECTURA: la base es una foto del .accdb, y una
// escritura se perdería al reiniciar sin que nadie se entere. Es preferible
// que la app reciba un error claro.
func (e *Engine) Serve(ctx context.Context, opts ServeOptions) error {
	e.eng.ReadOnly.Store(true)

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
		return fmt.Errorf("creando directorio aislado: %w", err)
	}
	defer os.RemoveAll(sandbox)
	if err := os.Chmod(sandbox, 0o500); err != nil {
		return err
	}
	// SetGlobal rechaza variables de solo lectura; AssignValues es la vía del
	// motor para fijar valores al arrancar.
	if err := sql.SystemVariables.AssignValues(map[string]any{"secure_file_priv": sandbox}); err != nil {
		return fmt.Errorf("restringiendo acceso a archivos: %w", err)
	}

	// Registramos el único usuario que puede conectarse. Al agregarlo, el
	// motor activa la autenticación: usuario o clave incorrectos se rechazan.
	mysqlDb := e.eng.Analyzer.Catalog.MySQLDb
	ed := mysqlDb.Editor()
	mysqlDb.AddEphemeralSuperUser(ed, opts.User, "%", opts.Password)
	ed.Close()

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

	srv, err := server.NewServer(
		server.Config{Protocol: "tcp", Address: opts.Address},
		e.eng, sql.NewContext, sessionBuilder, nil,
	)
	if err != nil {
		return fmt.Errorf("creando servidor: %w", err)
	}

	// Start bloquea; para detenerlo hay que llamar a Close desde otra
	// goroutine cuando se cancele el contexto (Ctrl+C).
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	if err := srv.Start(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}
