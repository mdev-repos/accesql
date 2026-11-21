package engine

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"access2mysql/internal/access"
)

// settleTime es cuánto tiempo debe llevar el archivo sin modificarse antes de
// recargarlo: si Access lo está escribiendo en este momento, la lectura podría
// salir corrupta.
const settleTime = 2 * time.Second

// FileState identifica una versión del archivo (fecha de modificación y
// tamaño) para detectar cambios sin leerlo.
type FileState struct {
	ModTime time.Time
	Size    int64
}

// StatFile devuelve el estado actual del archivo.
func StatFile(path string) (FileState, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return FileState{}, err
	}
	return FileState{ModTime: fi.ModTime(), Size: fi.Size()}, nil
}

// Reload vuelve a leer el archivo y reemplaza los datos, SIN duplicar la base
// en memoria.
//
//   - Toma el candado de escritura: espera a que terminen las consultas en
//     curso y frena las nuevas hasta terminar (las apps ven una pausa).
//   - Reemplaza tabla por tabla. Para cada una, primero LEE la nueva (si falla,
//     la vieja sigue intacta) y recién entonces borra la vieja y carga la
//     nueva. El pico extra de RAM es el de la tabla más grande, igual que en la
//     carga inicial, no el de toda la base.
//   - Si falla a mitad, quedan algunas tablas nuevas y otras viejas; el
//     servidor sigue funcionando y se devuelve el error para reintentar.
func (e *Engine) Reload(path string) error {
	src, err := access.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	names, err := src.Tables()
	if err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	keep := make(map[string]bool, len(names))
	for _, name := range names {
		t, err := src.Load(name)
		if err != nil {
			return err
		}
		keep[name] = true
		e.meta[name] = t.Columns
		if err := e.replaceTable(t); err != nil {
			return fmt.Errorf("recargando tabla %q: %w", name, err)
		}
		// Devolver al sistema la memoria de la tabla vieja ahora, no al final.
		debug.FreeOSMemory()
	}

	// Tablas que ya no existen en el archivo.
	current, err := e.db.GetTableNames(e.ctx)
	if err != nil {
		return err
	}
	for _, name := range current {
		if !keep[name] {
			if err := e.db.DropTable(e.ctx, name); err != nil {
				return err
			}
			delete(e.meta, name)
		}
	}
	if rels, err := src.Relations(); err == nil {
		e.relations = rels
	}
	e.loadedAt = time.Now()
	debug.FreeOSMemory()
	return nil
}

// replaceTable borra la tabla vieja (si existe) y carga la nueva.
func (e *Engine) replaceTable(t *access.Table) error {
	current, err := e.db.GetTableNames(e.ctx)
	if err != nil {
		return err
	}
	for _, name := range current {
		if name == t.Name {
			if err := e.db.DropTable(e.ctx, name); err != nil {
				return err
			}
			break
		}
	}
	return loadTable(e.ctx, e.db, t)
}

// AutoReload revisa el archivo cada `every` y lo recarga si cambió, hasta que
// ctx se cancele. `last` es el estado del archivo al momento de la carga
// inicial (tomado ANTES de cargar, para no perder un cambio que ocurra
// durante la carga). logf recibe los mensajes informativos y los errores.
func (e *Engine) AutoReload(ctx context.Context, path string, every time.Duration, last FileState, logf func(format string, args ...any)) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		cur, err := StatFile(path)
		if err != nil {
			logf("reload: no se pudo leer %s: %v", path, err)
			continue
		}
		if cur == last {
			continue
		}
		if time.Since(cur.ModTime) < settleTime {
			continue // se está escribiendo; probar en el próximo ciclo
		}

		start := time.Now()
		logf("reload: el archivo cambió, recargando (las consultas se pausan)...")
		if err := e.safeReload(path); err != nil {
			// No actualizamos `last`: se reintenta en el próximo ciclo.
			logf("reload: ERROR, se reintentará: %v", err)
			continue
		}
		last = cur
		logf("reload: listo en %v", time.Since(start).Round(time.Millisecond))
	}
}

// safeReload llama a Reload convirtiendo un pánico en error. El reload corre
// en su propia goroutine: sin esto, un archivo corrupto que haga fallar al
// lector de Access con un pánico tumbaría todo el servidor en lugar de solo
// fallar la recarga.
func (e *Engine) safeReload(path string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("pánico al leer el archivo (¿corrupto o incompleto?): %v", r)
		}
	}()
	return e.Reload(path)
}
