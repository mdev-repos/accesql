package cli

import (
	"bytes"
	"strings"
	"testing"

	"accesql/internal/version"
)

// run ejecuta el comando raíz con los argumentos dados y devuelve lo que
// imprimió (salida y errores juntos) y el error.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestVersion(t *testing.T) {
	for _, flag := range []string{"--version", "-v"} {
		out, err := run(t, flag)
		if err != nil || strings.TrimSpace(out) != "Accesql "+version.Version {
			t.Errorf("%s → %q (%v)", flag, out, err)
		}
	}
}

func TestHelpIsInSpanish(t *testing.T) {
	out, err := run(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Uso:", "Comandos:", "dump", "query", "serve", "web", "ayuda", "Opciones:", "accesql [opciones]", "mostrar la ayuda", "mostrar la versión", "accesql [comando]"} {
		if !strings.Contains(out, want) {
			t.Errorf("la ayuda no contiene %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"Usage:", "Available Commands", "Flags:", "help for", "[flags]", "completion", "version for"} {
		if strings.Contains(out, bad) {
			t.Errorf("la ayuda tiene texto en inglés o de más (%q):\n%s", bad, out)
		}
	}
}

func TestEverySubcommandHelpIsInSpanish(t *testing.T) {
	for _, c := range []string{"dump", "query", "serve", "web"} {
		out, err := run(t, c, "--help")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "Uso:") || !strings.Contains(out, "Opciones:") || strings.Contains(out, "Usage:") || strings.Contains(out, "help for") {
			t.Errorf("%s --help:\n%s", c, out)
		}
	}
}

func TestAyudaCommand(t *testing.T) {
	out, err := run(t, "ayuda", "dump")
	if err != nil || !strings.Contains(out, "accesql dump") || !strings.Contains(out, "--table") {
		t.Errorf("ayuda dump: %v\n%s", err, out)
	}
	if out, _ := run(t, "help", "serve"); !strings.Contains(out, "--reload-every") {
		t.Errorf("«help» debe seguir funcionando como alias:\n%s", out)
	}
	if out, _ := run(t, "ayuda", "nada"); !strings.Contains(out, "No existe el comando") {
		t.Errorf("comando inexistente:\n%s", out)
	}
}

func TestErrorsAreInSpanish(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--nada"}, "opción desconocida: --nada"},
		{[]string{"dump", "-z"}, "opción corta desconocida"},
		{[]string{"dump", "-f"}, "la opción necesita un valor"},
		{[]string{"serve", "--port", "abc", "-f", "x.accdb"}, "valor inválido"},
		{[]string{"dump"}, "falta la opción obligatoria --file (-f)"},
		{[]string{"query", "-f", "x.accdb"}, "falta la opción obligatoria --sql (-q)"},
		{[]string{"query"}, "faltan las opciones obligatorias --file (-f) y --sql (-q)"},
		{[]string{"serve"}, "falta la opción obligatoria --file (-f)"},
		{[]string{"nada"}, "no existe el comando «nada»"},
	}
	for _, c := range cases {
		out, err := run(t, c.args...)
		if err == nil {
			t.Errorf("%v debería fallar", c.args)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v → error %q, esperaba %q", c.args, err, c.want)
		}
		if strings.Contains(out, "unknown") || strings.Contains(out, "required flag") {
			t.Errorf("%v dejó texto en inglés: %s", c.args, out)
		}
	}
}

func TestCompletionCommandIsGone(t *testing.T) {
	if _, err := run(t, "completion", "bash"); err == nil {
		t.Error("el comando «completion» ya no debería existir")
	}
}

// Sin una terminal real (como en un test o un pipe) el comando raíz muestra la
// ayuda en vez de abrir el menú.
func TestRootWithoutTerminalPrintsHelp(t *testing.T) {
	out, err := run(t)
	if err != nil || !strings.Contains(out, "Uso:") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestRequiredFlagsAreSatisfiedBeforeRunning(t *testing.T) {
	// Con las opciones presentes, el error ya no es de opciones sino del archivo.
	_, err := run(t, "dump", "-f", "no-existe.accdb")
	if err == nil || strings.Contains(err.Error(), "opción") {
		t.Errorf("debería fallar por el archivo: %v", err)
	}
}
