# Licencia

> **Resumen no vinculante.** Lo que vale legalmente es el texto de las licencias (en inglés), en
> [`LICENSES/`](../LICENSES). Esta página es solo una guía para leerlo. No es asesoramiento legal.

Accesql **no es software libre ni «open source» en el sentido de la OSI**: el código es público y se puede
leer, usar y modificar, pero con límites pensados para que nadie lucre con el trabajo ajeno. Se ofrece bajo
**dos licencias, a elección de cada persona**:

| Si sos… | Licencia | Texto |
|---|---|---|
| Una persona usándolo para algo personal, de estudio, investigación o por hobby; o una organización sin fines de lucro, escuela o entidad pública | **PolyForm Noncommercial 1.0.0** | [texto](../LICENSES/PolyForm-Noncommercial-1.0.0.md) |
| Una empresa que lo usa en **su propia operación** (por ejemplo, para leer o migrar sus propias bases de Access) | **PolyForm Internal Use 1.0.0** | [texto](../LICENSES/PolyForm-Internal-Use-1.0.0.md) |

## Qué se puede y qué no

| | Uso no comercial | Uso interno de empresa |
|---|:---:|:---:|
| Usarlo gratis | ✅ | ✅ |
| Modificarlo para uso propio | ✅ | ✅ |
| Compartir copias con otras personas | ✅ (sin fines comerciales) | ❌ |
| **Venderlo**, o incluirlo en un producto que se vende | ❌ | ❌ |
| **Ofrecerlo como servicio** (pago o alojado) | ❌ | ❌ |
| Usarlo para prestar un servicio a terceros (por ejemplo, migrar las bases de un cliente) | ❌ | ❌ |

Si querés hacer algo que no está cubierto, pedí una licencia aparte, por escrito, al autor.

## Sin garantías

Ambas licencias incluyen la cláusula *No Liability*: **«tal cual», sin garantías, y el autor no responde por
daños** derivados del uso, en la medida que la ley lo permita. En la práctica:

- Hacé siempre una **copia de seguridad** de tus bases antes de trabajar con ellas. Accesql no modifica
  el archivo de Access, pero no hay garantía de que una lectura sea perfecta.
- **Revisá los scripts generados** antes de ejecutarlos en un servidor de producción, sobre todo si usás
  *Borrar la base si ya existe*.
- Probalo con tus datos antes de depender de él (ver [limitaciones](tipos-y-limitaciones.md)).

## Privacidad

Accesql corre en tu equipo y **no envía datos a ningún lado**: no tiene telemetría ni se conecta a
internet. Los únicos puertos que abre son los que vos activás (la interfaz web local y, si lo pedís, el
servidor MySQL).

## Marcas

«Microsoft Access» y «MySQL» son marcas de sus respectivos dueños. Accesql es un proyecto independiente, sin
relación con Microsoft ni con Oracle; los nombres se usan solo para describir con qué formatos trabaja. La
base de ejemplo *Northwind* es de Microsoft y no se distribuye con este repositorio.

## Componentes de terceros

Accesql usa software de terceros, cada uno con su propia licencia (casi todas permisivas: MIT, BSD, Apache).
Sus avisos están en [`THIRD_PARTY_NOTICES.md`](../THIRD_PARTY_NOTICES.md) y viajan con cada ejecutable. Dos
puntos para tener presentes:

- El lector de Access (`mdbgo`) contiene código portado de **mdbtools, licenciado LGPL-2.0 o posterior**.
  Como el código fuente completo de Accesql está disponible, cualquiera puede volver a compilar el programa.
- Un componente (`golang-lru`) es **MPL-2.0**; su código fuente está enlazado en los avisos.

Ninguna dependencia es GPL ni AGPL.

Para regenerar los avisos al cambiar dependencias: `python3 scripts/gen-notices.py`.
