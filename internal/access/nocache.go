package access

import _ "unsafe" // necesario para //go:linkname

// El driver mdbgo guarda en RAM una copia completa de cada archivo .accdb que
// abre (hasta 512 MB) y no la libera al cerrar la conexión. Como nosotros ya
// copiamos todos los datos al motor SQL, esa copia sería memoria duplicada
// permanente. La variable que controla el límite está en un paquete internal
// de mdbgo (no se puede importar), así que la enlazamos con //go:linkname y la
// ponemos en 0, que desactiva el caché.
//
// Es frágil: si una versión futura de mdbgo renombra o mueve esa variable, el
// programa deja de compilar o enlazar (falla ruidosamente, no en silencio).
// Alternativa limpia a futuro: pedir en mdbgo una opción pública.
//
//go:linkname fileCacheMaxBytes github.com/Felamande/mdbgo/internal/gomdb.FileCacheMaxBytes
var fileCacheMaxBytes int64

func init() { fileCacheMaxBytes = 0 }
