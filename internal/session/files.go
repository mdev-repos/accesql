package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Entry es una carpeta o una base de Access dentro de un listado.
type Entry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	IsDir    bool      `json:"isDir"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified,omitempty"`
}

// Listing es el contenido de una carpeta, para el explorador de archivos de
// ambas interfaces.
type Listing struct {
	Path   string `json:"path"`   // carpeta actual ("" = lista de unidades en Windows)
	Parent string `json:"parent"` // carpeta superior ("" = la lista de unidades en Windows)
	// HasParent dice si se puede subir un nivel: en Linux es falso en "/", y
	// en Windows es verdadero en cualquier unidad (se sube a la lista de unidades).
	HasParent bool    `json:"hasParent"`
	Entries   []Entry `json:"entries"`
}

// DefaultDir es la carpeta donde arranca el explorador: la del usuario, o la de
// trabajo si no se conoce.
func DefaultDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return string(filepath.Separator)
}

// ListDir lista las carpetas y las bases de Access (.accdb, .mdb) de una
// carpeta. Primero van las carpetas y después los archivos, cada grupo por
// orden alfabético. Las carpetas ocultas (que empiezan con punto) no se
// muestran. path vacío = DefaultDir (en Windows, la lista de unidades). Si
// path es un archivo, se lista su carpeta.
//
// Muestra el contenido de CUALQUIER carpeta que el programa pueda leer: la
// interfaz web debe ofrecerlo solo detrás de su token de acceso.
func ListDir(path string) (Listing, error) {
	if path == "" {
		if runtime.GOOS == "windows" {
			return listDrives(), nil
		}
		path = DefaultDir()
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Listing{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return Listing{}, fmt.Errorf("no se puede abrir la carpeta %q: %w", abs, unwrapPathErr(err))
	}
	if !fi.IsDir() {
		abs = filepath.Dir(abs)
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		return Listing{}, fmt.Errorf("no se puede leer la carpeta %q: %w", abs, unwrapPathErr(err))
	}

	parent := parentDir(abs)
	l := Listing{Path: abs, Parent: parent, HasParent: parent != "" || runtime.GOOS == "windows", Entries: []Entry{}}
	for _, d := range des {
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		info, err := entryInfo(abs, d)
		if err != nil {
			continue // enlace roto o sin permiso: se omite
		}
		switch {
		case info.IsDir():
			l.Entries = append(l.Entries, Entry{Name: name, Path: filepath.Join(abs, name), IsDir: true})
		case IsAccessFile(name):
			l.Entries = append(l.Entries, Entry{Name: name, Path: filepath.Join(abs, name), Size: info.Size(), Modified: info.ModTime()})
		}
	}
	sort.SliceStable(l.Entries, func(i, j int) bool {
		a, b := l.Entries[i], l.Entries[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return l, nil
}

// entryInfo sigue los enlaces simbólicos para saber si apuntan a una carpeta.
func entryInfo(dir string, d fs.DirEntry) (fs.FileInfo, error) {
	if d.Type()&fs.ModeSymlink != 0 {
		return os.Stat(filepath.Join(dir, d.Name()))
	}
	return d.Info()
}

// parentDir devuelve la carpeta superior, o "" si ya es la raíz.
func parentDir(abs string) string {
	p := filepath.Dir(abs)
	if p == abs {
		if runtime.GOOS == "windows" {
			return "" // desde C:\ se vuelve a la lista de unidades
		}
		return ""
	}
	return p
}

func listDrives() Listing {
	l := Listing{Entries: []Entry{}}
	for c := 'A'; c <= 'Z'; c++ {
		root := string(c) + `:\`
		if _, err := os.Stat(root); err == nil || !errors.Is(err, fs.ErrNotExist) {
			l.Entries = append(l.Entries, Entry{Name: root, Path: root, IsDir: true})
		}
	}
	return l
}
