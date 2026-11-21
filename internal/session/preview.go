package session

import (
	"bytes"
	"errors"
	"io"
	"strings"
)

var errPreviewFull = errors.New("vista previa completa")

// Preview ejecuta gen escribiendo en un búfer que acepta como máximo max bytes:
// al llenarse corta la generación (para no armar en memoria un script de cientos
// de megabytes solo para mostrar el comienzo). Devuelve el texto, cortado en el
// último salto de línea completo si no entró todo, y si quedó incompleto
// (truncated). Un error de gen que no sea el corte propio se devuelve tal cual.
func Preview(max int, gen func(w io.Writer) error) (text string, truncated bool, err error) {
	p := &previewBuf{max: max}
	err = gen(p)
	if err != nil && !errors.Is(err, errPreviewFull) {
		return "", false, err
	}
	t := p.buf.String()
	if p.full {
		if i := strings.LastIndexByte(t, '\n'); i >= 0 {
			t = t[:i+1]
		}
	}
	return t, p.full, nil
}

type previewBuf struct {
	buf  bytes.Buffer
	max  int
	full bool
}

func (p *previewBuf) Write(b []byte) (int, error) {
	room := p.max - p.buf.Len()
	if len(b) > room {
		room = max(room, 0)
		p.buf.Write(b[:room])
		p.full = true
		return room, errPreviewFull
	}
	return p.buf.Write(b)
}
