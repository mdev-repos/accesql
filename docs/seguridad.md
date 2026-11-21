# Seguridad

Accesql ejecuta SQL ajeno, puede abrir un puerto MySQL y, en su interfaz web, explora carpetas del
equipo. Por eso se diseñó con varias defensas. Esta página resume el modelo y sus límites.

## En todos los modos

- **Solo lectura.** El archivo de Access nunca se modifica. Los datos viven en una copia en memoria
  (la «imagen»), y cualquier escritura SQL contra ella es rechazada.
- **Sin acceso a archivos desde SQL.** `LOAD_FILE`, `INTO OUTFILE`, `INTO DUMPFILE` y `LOAD DATA`
  están deshabilitados: de lo contrario, un cliente autenticado (o una inyección SQL en una app) podría
  leer o crear archivos del servidor. Se logra apuntando `secure_file_priv` a un directorio vacío y sin
  permiso de escritura, y recreando la sesión interna después de fijarlo.
- **No se puede aflojar el blindaje** ni crear cuentas desde SQL (`SET GLOBAL`, `CREATE USER` y `GRANT`
  no tienen efecto).

## Servidor MySQL (`serve` y la opción de la interfaz)

- Escucha solo en `127.0.0.1` por defecto y exige **usuario y contraseña**. Sin contraseña no arranca.
- Con `serve`, la contraseña se pasa por la variable `ACCESQL_PASSWORD`, mejor que `--password`
  (que queda visible en la lista de procesos).
- Al detener el servidor se **borra su usuario**: reiniciarlo con otra clave no deja valer la anterior.
- **No hay cifrado (TLS).** Si las aplicaciones están en otra máquina, no uses `--host 0.0.0.0` a través
  de una red no confiable: conectá por un túnel SSH o una VPN.
- Es un solo usuario.

## Interfaz web (`web`)

Una página que ejecuta SQL, explora carpetas y abre un puerto es una superficie de ataque:

- Escucha solo en `127.0.0.1` por defecto.
- Todas las rutas `/api/` exigen un **token aleatorio de 128 bits**. Va en el fragmento `#t=` del enlace,
  que el navegador no envía al servidor ni incluye en el `Referer`, y la página lo saca de la barra de
  direcciones al cargar. Sin el token, la API responde 401.
- **Quien tiene el token puede explorar las carpetas del equipo y abrir un puerto MySQL:** tratalo como
  una contraseña.
- Se valida la cabecera `Host` (defensa contra *DNS rebinding*: una web ajena que apunta su dominio a
  `127.0.0.1`) y la cabecera `Origin` (defensa contra *CSRF*).
- Una política **CSP** impide cargar scripts o estilos externos; la página no usa CDN ni fuentes remotas.
- Los cuerpos de las peticiones y los resultados tienen tope de tamaño, y un error interno se convierte
  en una respuesta 500 sin tumbar el servidor.
- Los **CSV** neutralizan la *inyección de fórmulas*: un texto que empieza con `=`, `+`, `-` o `@` se
  exporta con una comilla delante, para que Excel no lo ejecute como fórmula.
- `--host 0.0.0.0` expone la interfaz a la red **sin cifrado** (HTTP): el token viaja en claro. Usalo solo
  en redes de confianza o detrás de un túnel SSH/VPN. El programa lo advierte al iniciar.

## Qué no cubre

- No hay TLS en ninguno de los dos servidores.
- No hay roles ni múltiples usuarios.
- Si compartís el enlace de la interfaz web o la contraseña del servidor MySQL, quien los reciba tiene
  acceso a todos los datos de la base.

## Cómo reportar un problema de seguridad

Escribí al mantenedor del repositorio por un canal privado antes de publicar el detalle.
