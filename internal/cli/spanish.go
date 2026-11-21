package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Cobra trae sus mensajes en inglés ("Usage", "Available Commands", "unknown
// flag"...). Todo esto los pasa a español y deja la línea de comandos coherente
// con el resto de la herramienta.

// usageTemplate es la plantilla de ayuda de cobra, traducida.
const usageTemplate = `Uso:{{if .Runnable}}
  {{.CommandPath}}{{if .HasAvailableFlags}} [opciones]{{end}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [comando]{{end}}{{if gt (len .Aliases) 0}}

Alias:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Ejemplos:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Comandos:{{range .Commands}}{{if .IsAvailableCommand}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Opciones:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Opciones globales:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Escribí "{{.CommandPath}} [comando] --help" para ver la ayuda de un comando.{{end}}
`

// localize aplica el español y los extras estándar (--version, ayuda) a todo el
// árbol de comandos. Se llama una vez, con todos los comandos ya agregados.
func localize(root *cobra.Command, version string) {
	root.Version = version
	root.SetVersionTemplate("Accesql {{.Version}}\n")
	root.Flags().BoolP("version", "v", false, "mostrar la versión")

	root.SetUsageTemplate(usageTemplate)
	root.SetFlagErrorFunc(translateFlagError)
	root.CompletionOptions.DisableDefaultCmd = true // el comando "completion" no se usa acá
	root.SetHelpCommand(&cobra.Command{
		Use:     "ayuda [comando]",
		Aliases: []string{"help"},
		Short:   "Muestra la ayuda de un comando",
		Run: func(c *cobra.Command, args []string) {
			target, rest, err := c.Root().Find(args)
			if target == nil || err != nil || len(rest) > 0 {
				c.Printf("No existe el comando %q. Probá «accesql --help».\n", strings.Join(args, " "))
				return
			}
			_ = target.Help()
		},
	})

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Flags().Lookup("help") == nil {
			c.Flags().BoolP("help", "h", false, "mostrar la ayuda")
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// translateFlagError pasa a español los errores de opciones más comunes.
func translateFlagError(c *cobra.Command, err error) error {
	msg := err.Error()
	hint := fmt.Sprintf(" (probá «%s --help»)", c.CommandPath())
	switch {
	case strings.HasPrefix(msg, "unknown flag: "):
		return fmt.Errorf("opción desconocida: %s%s", strings.TrimPrefix(msg, "unknown flag: "), hint)
	case strings.HasPrefix(msg, "unknown shorthand flag: "):
		return fmt.Errorf("opción corta desconocida: %s%s", strings.TrimPrefix(msg, "unknown shorthand flag: "), hint)
	case strings.HasPrefix(msg, "flag needs an argument: "):
		return fmt.Errorf("la opción necesita un valor: %s", strings.TrimPrefix(msg, "flag needs an argument: "))
	case strings.HasPrefix(msg, "invalid argument "):
		return fmt.Errorf("valor inválido: %s", strings.TrimPrefix(msg, "invalid argument "))
	}
	return err
}

// requireFlags exige opciones obligatorias con un mensaje en español. Reemplaza
// a MarkFlagRequired, cuyo mensaje sale en inglés.
func requireFlags(names ...string) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, _ []string) error {
		var missing []string
		for _, n := range names {
			if !c.Flags().Changed(n) {
				label := "--" + n
				if f := c.Flags().Lookup(n); f != nil && f.Shorthand != "" {
					label += " (-" + f.Shorthand + ")"
				}
				missing = append(missing, label)
			}
		}
		switch len(missing) {
		case 0:
			return nil
		case 1:
			return fmt.Errorf("falta la opción obligatoria %s (probá «%s --help»)", missing[0], c.CommandPath())
		}
		return fmt.Errorf("faltan las opciones obligatorias %s (probá «%s --help»)", strings.Join(missing, " y "), c.CommandPath())
	}
}

// unknownCommand es el validador de argumentos del comando raíz: cualquier
// palabra suelta es un comando que no existe.
func unknownCommand(c *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("no existe el comando «%s» (probá «%s --help»)", args[0], c.CommandPath())
	}
	return nil
}
