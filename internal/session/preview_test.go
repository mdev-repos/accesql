package session

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestPreviewCutsAtLineBoundary(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	text, truncated, err := Preview(1000, func(w io.Writer) error {
		for i := 0; i < 5000; i++ {
			if _, err := w.Write([]byte(line)); err != nil {
				return err // como hacen los generadores: propagan el corte
			}
		}
		return nil
	})
	if err != nil || !truncated {
		t.Fatalf("debería quedar truncada sin error: truncated=%v err=%v", truncated, err)
	}
	if len(text) > 1000 || !strings.HasSuffix(text, "\n") {
		t.Errorf("debe terminar en un salto de línea completo (%d bytes)", len(text))
	}
	for _, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if len(l) != 99 {
			t.Fatalf("hay una línea cortada a la mitad: %d caracteres", len(l))
		}
	}
}

func TestPreviewSmallScriptIsComplete(t *testing.T) {
	text, truncated, err := Preview(1000, func(w io.Writer) error { _, err := w.Write([]byte("poco\n")); return err })
	if err != nil || truncated || text != "poco\n" {
		t.Errorf("%q %v %v", text, truncated, err)
	}
}

func TestPreviewPropagatesRealErrors(t *testing.T) {
	boom := errors.New("falló de verdad")
	if _, _, err := Preview(1000, func(io.Writer) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("un error real no debe confundirse con el corte: %v", err)
	}
}
