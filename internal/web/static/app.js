/* Accesql — interfaz web.
   JavaScript sin dependencias. Toda la lógica vive en el servidor (la sesión);
   esta página solo muestra el estado y manda las acciones. */
(function () {
  "use strict";

  const $ = (id) => document.getElementById(id);

  /* ---------------------------------------------------------------
     Utilidades
  --------------------------------------------------------------- */
  let toastTimer;
  function toast(msg) {
    const t = $("toast");
    t.textContent = msg;
    t.classList.add("show");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => t.classList.remove("show"), 2800);
  }

  function el(tag, attrs, text) {
    const e = document.createElement(tag);
    if (attrs) for (const k in attrs) e.setAttribute(k, attrs[k]);
    if (text != null) e.textContent = text;
    return e;
  }

  function svgEl(tag, attrs, text) {
    const e = document.createElementNS("http://www.w3.org/2000/svg", tag);
    if (attrs) for (const k in attrs) e.setAttribute(k, attrs[k]);
    if (text != null) e.textContent = text;
    return e;
  }

  function iconUse(name) {
    const s = svgEl("svg", { "class": "ico", "aria-hidden": "true" });
    s.appendChild(svgEl("use", { href: "#i-" + name }));
    return s;
  }

  function showError(id, msg) {
    const e = $(id);
    e.hidden = !msg;
    e.textContent = msg || "";
  }

  function fmtSize(n) {
    if (n < 1024) return n + " B";
    if (n < 1048576) return (n / 1024).toFixed(0) + " KB";
    if (n < 1073741824) return (n / 1048576).toFixed(1) + " MB";
    return (n / 1073741824).toFixed(2) + " GB";
  }

  function fmtDate(iso) {
    const d = new Date(iso);
    return isNaN(d) ? "" : d.toLocaleDateString("es-AR") + " " + d.toLocaleTimeString("es-AR", { hour: "2-digit", minute: "2-digit" });
  }

  function plural(n, one, many) { return n + " " + (n === 1 ? one : many); }

  function copyText(text) {
    const done = () => toast("Copiado al portapapeles.");
    try {
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(done, () => toast("No se pudo copiar."));
        return;
      }
    } catch (e) { /* sigue al plan B */ }
    try {
      const ta = el("textarea"); ta.value = text; document.body.appendChild(ta); ta.select();
      document.execCommand("copy"); ta.remove(); done();
    } catch (e) { toast("No se pudo copiar."); }
  }

  /* ---------------------------------------------------------------
     API: el token viaja en el fragmento del enlace (#t=…), se guarda
     en sessionStorage y se saca de la barra de direcciones.
  --------------------------------------------------------------- */
  let token = "";
  try { token = sessionStorage.getItem("accesql-token") || ""; } catch (e) { /* sin almacenamiento */ }
  const m = /(?:^|[#&])t=([0-9a-f]+)/.exec(location.hash);
  if (m) {
    token = m[1];
    try { sessionStorage.setItem("accesql-token", token); } catch (e) { /* ignorar */ }
    history.replaceState(null, "", location.pathname + location.search);
  }

  // Si se pega un enlace nuevo en la misma pestaña (por ejemplo, tras reiniciar
  // el programa) el navegador solo cambia el "#" y no recarga la página, que
  // seguiría con el token viejo: se recarga para tomar el nuevo.
  window.addEventListener("hashchange", () => {
    const n = /(?:^|[#&])t=([0-9a-f]+)/.exec(location.hash);
    if (!n) return;
    try { sessionStorage.setItem("accesql-token", n[1]); } catch (e) { /* ignorar */ }
    history.replaceState(null, "", location.pathname + location.search);
    location.reload();
  });

  let authShown = false;
  function needAuth() {
    if (authShown) return;
    authShown = true;
    $("authDialog").showModal();
  }

  async function errorFrom(r) {
    let msg = "Error " + r.status;
    try { const j = await r.json(); if (j && j.error) msg = j.error; } catch (e) { /* sin cuerpo JSON */ }
    if (r.status === 401) needAuth();
    const err = new Error(msg);
    err.status = r.status;
    return err;
  }

  function request(method, path, body) {
    return fetch(path, {
      method,
      headers: { "Authorization": "Bearer " + token, "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  }

  async function api(method, path, body) {
    const r = await request(method, path, body);
    if (!r.ok) throw await errorFrom(r);
    return r.json();
  }

  // download pide un archivo y lo guarda con el nombre que manda el servidor.
  async function download(method, path, body, fallbackName) {
    const r = await request(method, path, body);
    if (!r.ok) throw await errorFrom(r);
    const cd = r.headers.get("Content-Disposition") || "";
    const nm = /filename="([^"]+)"/.exec(cd);
    const blob = await r.blob();
    const a = el("a", { href: URL.createObjectURL(blob), download: nm ? nm[1] : fallbackName });
    document.body.appendChild(a); a.click(); a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 4000);
  }

  /* ---------------------------------------------------------------
     Estado de la sesión
  --------------------------------------------------------------- */
  let st = null;          // último estado recibido
  let imageKey = "";      // identifica la imagen actual (para detectar una nueva)
  let lastFile = null;    // para actualizar el nombre sugerido solo al cambiar de archivo
  let tables = [];        // tablas de la imagen
  let schema = null;      // esquema (diagrama), se pide al abrirlo
  const LOCKABLE = ["p-server", "p-query", "p-convert", "p-er"];

  async function refresh() {
    st = await api("GET", "/api/state");
    render();
  }

  async function loop() {
    try { await refresh(); } catch (e) { if (e.status === 401) return; }
    setTimeout(loop, st && st.generating ? 300 : 4000);
  }

  function setLocked(locked) {
    LOCKABLE.forEach((id) => {
      const p = $(id);
      p.classList.toggle("is-locked", locked);
      p.querySelector(":scope > .body").disabled = locked;
    });
    if (!locked) syncServerControls();
  }

  function render() {
    $("ver").textContent = "v" + st.version;

    // Base de datos
    $("filePath").value = st.file || "";
    if (st.file !== lastFile) {
      lastFile = st.file;
      $("dbName").value = st.suggestedDatabase || "";
    }
    $("openBtn").disabled = st.generating;
    $("dbName").disabled = st.generating;
    $("genBtn").disabled = !st.file || st.generating;
    $("cancelBtn").hidden = !st.generating;
    $("progress").hidden = !st.generating;
    if (st.generating && st.progress) {
      const p = st.progress;
      $("prog").value = Math.round((p.number - 1) / p.total * 100);
      $("progText").textContent = "Leyendo «" + p.table + "» (" + p.number + " de " + p.total + ")…";
    } else if (st.generating) {
      $("prog").value = 0;
      $("progText").textContent = "Abriendo el archivo…";
    }
    showError("genError", st.generating ? "" : st.generateError ? "No se pudo generar la imagen: " + st.generateError : "");

    $("okBox").hidden = !st.imageReady || st.generating;
    if (st.imageReady) {
      $("okSummary").textContent = plural(st.tables, "tabla", "tablas") + " · " + st.rows.toLocaleString("es-AR") + " filas · base «" + st.database + "»";
    }

    // Bloques opcionales
    const key = st.imageReady ? st.loadedAt : "";
    const locked = !st.imageReady || st.generating;
    if (key !== imageKey) {
      imageKey = key;
      onImageChanged();
    }
    setLocked(locked);
    syncServer();
  }

  // onImageChanged limpia lo que dependía de la imagen anterior y carga la nueva.
  async function onImageChanged() {
    schema = null;
    $("result").hidden = true;
    $("scriptOut").hidden = true;
    showError("queryError", "");
    showError("scriptError", "");
    if (!st.imageReady) { tables = []; buildTables(); return; }
    try {
      tables = await api("GET", "/api/tables");
    } catch (e) {
      toast("No se pudieron leer las tablas: " + e.message);
      tables = [];
    }
    buildTables();
    $("newName").value = st.database;
  }

  function buildTables() {
    const chips = $("tableChips");
    chips.textContent = "";
    tables.forEach((t) => chips.appendChild(el("button", { type: "button", "class": "chip", "data-table": t.name, title: plural(t.rows, "fila", "filas") }, t.name)));
    $("tableCount").textContent = "(" + tables.length + ")";

    const map = $("mapList");
    map.textContent = "";
    tables.forEach((t) => {
      const item = el("div", { "class": "map-item" });
      const row = el("div", { "class": "map-row" });
      row.appendChild(el("input", { type: "checkbox", checked: "", "aria-label": "Migrar " + t.name }));
      row.appendChild(el("span", { "class": "name", title: t.name }, t.name));
      row.appendChild(el("span", { "class": "arrow", "aria-hidden": "true" }, "→"));
      row.appendChild(el("input", { type: "text", value: t.name, "aria-label": "Tabla de destino para " + t.name, autocomplete: "off", spellcheck: "false", "data-role": "target" }));
      row.appendChild(el("button", { type: "button", "class": "btn btn-sm", "data-role": "colsBtn", "aria-expanded": "false" }, "Columnas"));
      const cols = el("div", { "class": "map-cols", hidden: "" });
      cols.appendChild(el("textarea", { rows: "2", spellcheck: "false", "data-role": "cols", "aria-label": "Columnas de la tabla de destino para " + t.name,
        placeholder: "Columnas de la tabla de MySQL, separadas por coma o una por línea. Vacío = copiar todas (" + t.columns.length + ")." }));
      item.appendChild(row);
      item.appendChild(cols);
      map.appendChild(item);
    });
  }

  /* ---------------------------------------------------------------
     Base de datos: explorador de archivos y generación
  --------------------------------------------------------------- */
  const fileDialog = $("fileDialog");
  let fd = null;          // último listado recibido
  let picked = null;      // entrada elegida

  async function loadDir(path) {
    try {
      fd = await api("GET", "/api/files?path=" + encodeURIComponent(path));
      showError("fileError", "");
    } catch (e) {
      showError("fileError", e.message);
      return;
    }
    renderFiles();
  }

  function renderFiles() {
    const list = $("fileList");
    list.textContent = "";
    $("curPath").value = fd.path || "(unidades del equipo)";
    $("upBtn").disabled = !fd.hasParent;
    fd.entries.forEach((e) => {
      const li = el("li");
      const b = el("button", { type: "button", "class": "row-btn" + (e.isDir ? " dir" : ""), "data-path": e.path, "data-dir": e.isDir ? "1" : "0", "aria-pressed": String(!!picked && picked.path === e.path) });
      b.appendChild(iconUse(e.isDir ? "folder" : "db"));
      b.appendChild(el("span", null, e.name));
      if (!e.isDir) b.appendChild(el("span", { "class": "ext" }, fmtSize(e.size) + (e.modified ? " · " + fmtDate(e.modified) : "")));
      li.appendChild(b);
      list.appendChild(li);
    });
    if (!fd.entries.length) list.appendChild(el("li", { "class": "empty" }, "Esta carpeta no tiene bases de Access."));
    $("selName").textContent = picked ? picked.name : "Ningún archivo seleccionado";
    $("acceptBtn").disabled = !picked;
  }

  $("openBtn").addEventListener("click", async () => {
    picked = null;
    showError("fileError", "");
    fileDialog.showModal();
    await loadDir(st && st.file ? st.file : "");
  });
  $("upBtn").addEventListener("click", () => { picked = null; if (fd && fd.hasParent) loadDir(fd.parent); });

  $("fileList").addEventListener("click", (ev) => {
    const b = ev.target.closest(".row-btn"); if (!b) return;
    if (b.dataset.dir === "1") { picked = null; loadDir(b.dataset.path); return; }
    picked = fd.entries.find((e) => e.path === b.dataset.path) || null;
    renderFiles();
  });
  $("fileList").addEventListener("dblclick", (ev) => {
    const b = ev.target.closest(".row-btn");
    if (b && b.dataset.dir === "0") acceptFile();
  });
  $("acceptBtn").addEventListener("click", acceptFile);

  async function acceptFile() {
    if (!picked) return;
    $("acceptBtn").disabled = true;
    try {
      st = await api("POST", "/api/open", { path: picked.path });
      fileDialog.close();
      render();
    } catch (e) {
      showError("fileError", e.message);
      $("acceptBtn").disabled = false;
    }
  }

  $("genBtn").addEventListener("click", async () => {
    $("genBtn").disabled = true;
    showError("genError", "");
    try {
      await api("POST", "/api/generate", { database: $("dbName").value.trim() });
    } catch (e) {
      showError("genError", e.message);
      $("genBtn").disabled = false;
      return;
    }
    await refresh();
  });
  $("cancelBtn").addEventListener("click", async () => {
    $("cancelBtn").disabled = true;
    try { await api("POST", "/api/generate/cancel"); } catch (e) { toast(e.message); }
    await refresh();
    $("cancelBtn").disabled = false;
  });
  $("dumpBtn").addEventListener("click", () =>
    download("GET", "/api/dump", undefined, "base.json").catch((e) => toast(e.message)));

  /* ---------------------------------------------------------------
     Servidor MySQL
  --------------------------------------------------------------- */
  let srvLocalErr = "";

  function syncServerControls() {
    const running = !!(st && st.server && st.server.running);
    const on = $("srvOn").checked || running;
    $("srvOn").checked = on;
    $("srvOn").disabled = running;
    $("srvFields").disabled = !on || running;
    $("srvBtn").disabled = !on;
  }

  function hostHint() {
    const h = $("srvHost").value.trim();
    let msg = "Una dirección de esta computadora.";
    if (h === "127.0.0.1" || h === "localhost") msg = "Solo esta computadora.";
    else if (h === "0.0.0.0") msg = "Toda la red, sin cifrado: usá una contraseña fuerte.";
    $("srvHostHint").textContent = msg;
  }

  function syncServer() {
    const sv = (st && st.server) || {};
    $("srvDb").value = st.database || st.suggestedDatabase || "";
    syncServerControls();

    const b = $("srvBtn");
    b.classList.toggle("btn-primary", !sv.running);
    b.classList.toggle("btn-danger", !!sv.running);
    b.querySelector("use").setAttribute("href", sv.running ? "#i-stop" : "#i-play");
    b.querySelector("span").textContent = sv.running ? "Detener servidor" : "Iniciar servidor";
    $("srvDot").classList.toggle("on", !!sv.running);
    $("srvStatus").textContent = sv.running ? "Activo en " + sv.address : "Detenido";

    $("srvConnect").hidden = !sv.running;
    if (sv.running) {
      const i = sv.address.lastIndexOf(":");
      let host = sv.address.slice(0, i), port = sv.address.slice(i + 1);
      if (host === "0.0.0.0" || host === "::" || host === "[::]") host = "127.0.0.1";
      $("srvCmd").textContent = "mysql -h " + host + " -P " + port + " -u " + sv.user + " -p " + sv.database;
      $("srvDsn").textContent = "mysql:host=" + host + ";port=" + port + ";dbname=" + sv.database + ";charset=utf8mb4";
    }
    showError("srvError", srvLocalErr || (sv.error && !sv.running ? "El servidor se detuvo: " + sv.error : ""));
  }

  $("srvOn").addEventListener("change", () => { syncServerControls(); });
  $("srvHost").addEventListener("input", hostHint);
  $("srvShow").addEventListener("click", function () {
    const pw = $("srvPass"), show = pw.type === "password";
    pw.type = show ? "text" : "password";
    this.textContent = show ? "Ocultar" : "Mostrar";
    this.setAttribute("aria-pressed", String(show));
  });

  $("srvBtn").addEventListener("click", async () => {
    srvLocalErr = "";
    $("srvBtn").disabled = true;
    try {
      if (st.server && st.server.running) {
        st = await api("POST", "/api/server/stop");
        toast("Servidor detenido.");
      } else {
        const port = Number($("srvPort").value);
        st = await api("POST", "/api/server/start", {
          host: $("srvHost").value.trim(), port: Number.isInteger(port) ? port : 0,
          user: $("srvUser").value.trim(), password: $("srvPass").value,
        });
        toast("Servidor iniciado.");
      }
    } catch (e) {
      srvLocalErr = e.message;
    }
    render();
    $("srvBtn").disabled = false;
    syncServerControls();
  });

  document.addEventListener("click", (e) => {
    const c = e.target.closest("[data-copy]");
    if (c) copyText($(c.dataset.copy).textContent);
  });

  /* ---------------------------------------------------------------
     Consultas
  --------------------------------------------------------------- */
  let lastSql = "";

  $("tableChips").addEventListener("click", (e) => {
    const c = e.target.closest(".chip"); if (!c) return;
    const s = $("sql");
    s.value = "SELECT * FROM `" + c.dataset.table + "` LIMIT 100";
    s.focus();
  });

  async function runQuery() {
    const sql = $("sql").value.trim();
    if (!sql) { showError("queryError", "Escribí una consulta primero."); $("sql").focus(); return; }
    showError("queryError", "");
    $("runBtn").disabled = true;
    try {
      const res = await api("POST", "/api/query", { sql, limit: Number($("limit").value) });
      lastSql = sql;
      renderResult(res);
    } catch (e) {
      $("result").hidden = true;
      showError("queryError", e.message);
    } finally {
      $("runBtn").disabled = false;
    }
  }

  function renderResult(res) {
    const grid = $("grid");
    grid.textContent = "";
    const thead = el("thead"), tr = el("tr");
    res.columns.forEach((c) => tr.appendChild(el("th", { scope: "col" }, c)));
    thead.appendChild(tr);
    grid.appendChild(thead);
    const tbody = el("tbody");
    res.rows.forEach((r) => {
      const row = el("tr");
      r.forEach((v) => {
        const td = el("td");
        if (v === null) { td.className = "null"; td.textContent = "NULL"; }
        else if (typeof v === "number") { td.className = "num"; td.textContent = String(v); }
        else if (typeof v === "object") td.textContent = JSON.stringify(v);
        else td.textContent = String(v);
        row.appendChild(td);
      });
      tbody.appendChild(row);
    });
    grid.appendChild(tbody);
    $("resultInfo").textContent = plural(res.count, "fila", "filas") + " · " + res.ms + " ms" +
      (res.truncated ? " · hay más filas: se muestran las primeras " + res.count.toLocaleString("es-AR") : "");
    $("result").hidden = false;
  }

  $("runBtn").addEventListener("click", runQuery);
  $("clearBtn").addEventListener("click", () => {
    $("sql").value = ""; $("result").hidden = true; showError("queryError", ""); $("sql").focus();
  });
  $("sql").addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") { e.preventDefault(); runQuery(); }
  });

  function exportResult(format) {
    const table = $("expTable").value.trim() || "resultado";
    download("POST", "/api/export", { sql: lastSql, format, table }, "resultado").catch((e) => toast(e.message));
  }
  $("expJson").addEventListener("click", () => exportResult("json"));
  $("expCsv").addEventListener("click", () => exportResult("csv-excel"));
  $("expSql").addEventListener("click", () => exportResult("sql"));

  /* ---------------------------------------------------------------
     Convertir a MySQL
  --------------------------------------------------------------- */
  let lastScript = null;   // { path, body } de la última vista previa, para descargar completo

  document.querySelectorAll("input[name=mode]").forEach((r) => {
    r.addEventListener("change", () => {
      const add = document.querySelector("input[name=mode]:checked").value === "add";
      $("modeNew").hidden = add; $("modeAdd").hidden = !add;
      $("scriptOut").hidden = true; showError("scriptError", "");
    });
  });

  function markAll(on) {
    document.querySelectorAll("#mapList .map-row input[type=checkbox]").forEach((c) => { c.checked = on; });
  }
  $("mapAll").addEventListener("click", () => markAll(true));
  $("mapNone").addEventListener("click", () => markAll(false));

  $("mapList").addEventListener("click", (e) => {
    const b = e.target.closest("[data-role=colsBtn]"); if (!b) return;
    const box = b.closest(".map-item").querySelector(".map-cols");
    box.hidden = !box.hidden;
    b.setAttribute("aria-expanded", String(!box.hidden));
    if (!box.hidden) box.querySelector("textarea").focus();
  });

  function splitColumns(text) {
    return text.split(/[\n,;]+/).map((s) => s.trim()).filter(Boolean);
  }

  // scriptRequest arma la petición según el modo elegido; devuelve null y avisa si falta algo.
  function scriptRequest() {
    const mode = document.querySelector("input[name=mode]:checked").value;
    if (mode === "new") {
      const name = $("newName").value.trim();
      if (!/^[A-Za-z0-9_$]{1,64}$/.test(name)) { showError("scriptError", "El nombre solo puede tener letras, números, _ o $ (hasta 64 caracteres)."); return null; }
      return { path: "/api/script/new", body: { database: name, data: $("newData").checked, foreignKeys: $("newFk").checked, drop: $("newDrop").checked } };
    }
    const sel = [];
    document.querySelectorAll("#mapList .map-item").forEach((item, i) => {
      if (!item.querySelector(".map-row input[type=checkbox]").checked) return;
      sel.push({
        source: tables[i].name,
        target: item.querySelector("[data-role=target]").value.trim(),
        columns: splitColumns(item.querySelector("[data-role=cols]").value),
      });
    });
    if (!sel.length) { showError("scriptError", "Marcá al menos una tabla para migrar."); return null; }
    return { path: "/api/script/add", body: { tables: sel, onDuplicate: $("dupMode").value, transaction: $("addTx").checked } };
  }

  $("scriptBtn").addEventListener("click", async () => {
    showError("scriptError", "");
    const req = scriptRequest();
    if (!req) return;
    $("scriptBtn").disabled = true;
    $("scriptBtn").textContent = "Generando…";
    try {
      const res = await api("POST", req.path, Object.assign({ preview: true }, req.body));
      lastScript = req;
      showScript(res);
    } catch (e) {
      $("scriptOut").hidden = true;
      showError("scriptError", e.message);
    } finally {
      $("scriptBtn").disabled = false;
      $("scriptBtn").textContent = "Generar script SQL";
    }
  });

  function showScript(res) {
    $("scriptText").value = res.script;
    const lines = res.script.split("\n").length - 1;
    $("scriptInfo").textContent = res.truncated
      ? "Vista previa (" + lines + " líneas): el script completo es más largo, descargalo"
      : plural(lines, "línea", "líneas");
    $("scriptCopy").disabled = res.truncated;
    $("scriptCopy").title = res.truncated ? "Es solo una vista previa: descargá el archivo para tener el script completo" : "";

    // Los avisos van en el encabezado del script como "-- AVISO: …".
    const notes = $("scriptNotes");
    notes.textContent = "";
    res.script.split("\n").forEach((l) => {
      if (l.startsWith("-- AVISO: ")) notes.appendChild(el("li", null, l.slice(10)));
    });
    notes.hidden = !notes.children.length;
    $("scriptOut").hidden = false;
  }

  $("scriptCopy").addEventListener("click", () => copyText($("scriptText").value));
  $("scriptDown").addEventListener("click", async () => {
    if (!lastScript) return;
    $("scriptDown").disabled = true;
    try {
      await download("POST", lastScript.path, Object.assign({ preview: false }, lastScript.body), "accesql.sql");
    } catch (e) { toast(e.message); }
    $("scriptDown").disabled = false;
  });

  /* ---------------------------------------------------------------
     Diagrama de relaciones
  --------------------------------------------------------------- */
  const BOX_W = 230, HEAD_H = 28, ROW_H = 18, PAD = 6, GAP_X = 70, GAP_Y = 36, MAX_ROWS = 10;
  let erFor = null;

  function clipText(s, n) { return s.length > n ? s.slice(0, n - 1) + "…" : s; }

  // Ordena las tablas poniendo cerca las que se relacionan (recorrido en anchura
  // desde la más conectada) para que las líneas sean cortas.
  function orderTables(sc) {
    const adj = new Map(sc.tables.map((t) => [t.name.toLowerCase(), new Set()]));
    sc.relations.forEach((r) => {
      const a = r.table.toLowerCase(), b = r.refTable.toLowerCase();
      if (a !== b && adj.has(a) && adj.has(b)) { adj.get(a).add(b); adj.get(b).add(a); }
    });
    const byKey = new Map(sc.tables.map((t) => [t.name.toLowerCase(), t]));
    const left = new Set(adj.keys());
    const out = [];
    while (left.size) {
      let start = null;
      left.forEach((k) => { if (start === null || adj.get(k).size > adj.get(start).size) start = k; });
      const queue = [start]; left.delete(start);
      while (queue.length) {
        const k = queue.shift(); out.push(byKey.get(k));
        [...adj.get(k)].filter((n) => left.has(n)).sort((x, y) => adj.get(y).size - adj.get(x).size).forEach((n) => { left.delete(n); queue.push(n); });
      }
    }
    return out;
  }

  function buildDiagram(sc) {
    const host = $("erHost");
    host.textContent = "";
    if (!sc.tables.length) { host.appendChild(el("p", { "class": "hint" }, "La base no tiene tablas.")); return; }

    const ordered = orderTables(sc);
    const cols = ordered.length <= 4 ? 2 : ordered.length <= 9 ? 3 : 4;

    // Cada tabla muestra primero sus claves y después el resto, hasta MAX_ROWS.
    const boxes = new Map();
    ordered.forEach((t) => {
      const cs = t.columns.slice().sort((a, b) => (b.pk || b.fk) - (a.pk || a.fk));
      const shown = cs.slice(0, MAX_ROWS);
      boxes.set(t.name.toLowerCase(), { t, shown, more: cs.length - shown.length, x: 0, y: 0, h: HEAD_H + PAD * 2 + shown.length * ROW_H + (cs.length > shown.length ? ROW_H : 0) });
    });

    // Cuadrícula: la altura de cada fila es la de su tabla más alta.
    let y = GAP_Y, i = 0;
    const list = [...boxes.values()];
    while (i < list.length) {
      const row = list.slice(i, i + cols);
      const rowH = Math.max(...row.map((b) => b.h));
      row.forEach((b, c) => { b.x = GAP_X / 2 + c * (BOX_W + GAP_X); b.y = y; });
      y += rowH + GAP_Y; i += cols;
    }
    const W = GAP_X + cols * BOX_W + (cols - 1) * GAP_X, H = y;

    const svg = svgEl("svg", { "class": "er-svg", width: W, height: H, viewBox: "0 0 " + W + " " + H, role: "img", "aria-label": "Diagrama de tablas y relaciones" });
    const lines = svgEl("g"), tablesG = svgEl("g");
    svg.appendChild(lines); svg.appendChild(tablesG);

    const rowY = (b, colName) => {
      const j = b.shown.findIndex((c) => c.name.toLowerCase() === colName.toLowerCase());
      return b.y + (j < 0 ? HEAD_H / 2 : HEAD_H + PAD + j * ROW_H + ROW_H / 2);
    };

    const relLines = [];
    sc.relations.forEach((r) => {
      const a = boxes.get(r.table.toLowerCase()), b = boxes.get(r.refTable.toLowerCase());
      if (!a || !b) return;
      const ya = rowY(a, r.columns[0]), yb = rowY(b, r.refColumns[0]);
      let d, la, lb;
      if (a === b) {                                    // relación consigo misma
        const x = a.x + BOX_W;
        d = "M" + x + " " + ya + "C" + (x + 40) + " " + ya + "," + (x + 40) + " " + (ya + 24) + "," + x + " " + (ya + 24);
        la = [x + 8, ya - 4, "start"]; lb = [x + 8, ya + 34, "start"];
      } else if (Math.abs(a.x - b.x) < 1) {             // misma columna: arco por la derecha
        const x = a.x + BOX_W, off = 46 + (Math.abs(a.y - b.y) > 400 ? 20 : 0);
        d = "M" + x + " " + ya + "C" + (x + off) + " " + ya + "," + (x + off) + " " + yb + "," + x + " " + yb;
        la = [x + 8, ya - 4, "start"]; lb = [x + 8, yb - 4, "start"];
      } else {
        const toRight = b.x > a.x;
        const xa = toRight ? a.x + BOX_W : a.x, xb = toRight ? b.x : b.x + BOX_W;
        const dx = Math.max(30, Math.abs(xb - xa) / 2) * (toRight ? 1 : -1);
        d = "M" + xa + " " + ya + "C" + (xa + dx) + " " + ya + "," + (xb - dx) + " " + yb + "," + xb + " " + yb;
        la = [xa + (toRight ? 8 : -8), ya - 4, toRight ? "start" : "end"]; lb = [xb + (toRight ? -8 : 8), yb - 4, toRight ? "end" : "start"];
      }
      const g = svgEl("g", { "data-a": r.table.toLowerCase(), "data-b": r.refTable.toLowerCase() });
      const path = svgEl("path", { "class": "er-line" + (r.enforced ? "" : " weak"), d });
      path.appendChild(svgEl("title", null, r.table + "." + r.columns.join(", ") + " → " + r.refTable + "." + r.refColumns.join(", ") +
        (r.enforced ? " (integridad referencial)" : " (sin integridad referencial)")));
      g.appendChild(path);
      g.appendChild(svgEl("text", { "class": "er-card", x: la[0], y: la[1], "text-anchor": la[2] }, r.oneToOne ? "1" : "N"));
      g.appendChild(svgEl("text", { "class": "er-card", x: lb[0], y: lb[1], "text-anchor": lb[2] }, "1"));
      lines.appendChild(g);
      relLines.push(g);
    });

    boxes.forEach((b, key) => {
      const g = svgEl("g", { "class": "er-table", "data-key": key, tabindex: "0", role: "button", "aria-label": "Resaltar las relaciones de " + b.t.name });
      g.appendChild(svgEl("rect", { "class": "er-box", x: b.x, y: b.y, width: BOX_W, height: b.h, rx: 5 }));
      g.appendChild(svgEl("path", { "class": "er-head", d: "M" + b.x + " " + (b.y + HEAD_H) + "V" + (b.y + 5) + "a5 5 0 0 1 5-5h" + (BOX_W - 10) + "a5 5 0 0 1 5 5v" + (HEAD_H - 5) + "z" }));
      g.appendChild(svgEl("line", { "class": "er-head-line", x1: b.x, y1: b.y + HEAD_H, x2: b.x + BOX_W, y2: b.y + HEAD_H }));
      g.appendChild(svgEl("text", { "class": "er-title", x: b.x + 10, y: b.y + 19 }, clipText(b.t.name, 26)));
      g.appendChild(svgEl("text", { "class": "er-count", x: b.x + BOX_W - 10, y: b.y + 19, "text-anchor": "end" }, b.t.rows.toLocaleString("es-AR")));
      b.shown.forEach((c, j) => {
        const ty = b.y + HEAD_H + PAD + j * ROW_H + 13;
        if (c.pk || c.fk) g.appendChild(svgEl("text", { "class": c.pk ? "er-key" : "er-fk", x: b.x + 10, y: ty }, c.pk ? "PK" : "FK"));
        g.appendChild(svgEl("text", { "class": "er-field", x: b.x + 34, y: ty }, clipText(c.name, 22)));
        g.appendChild(svgEl("text", { "class": "er-type", x: b.x + BOX_W - 10, y: ty, "text-anchor": "end" }, clipText(c.accessType || c.type, 12)));
      });
      if (b.more > 0) g.appendChild(svgEl("text", { "class": "er-more", x: b.x + 10, y: b.y + HEAD_H + PAD + b.shown.length * ROW_H + 13 }, "… y " + plural(b.more, "columna más", "columnas más")));
      tablesG.appendChild(g);
    });

    // Un clic en una tabla resalta sus relaciones y atenúa el resto.
    let active = null;
    function highlight(key) {
      active = active === key ? null : key;
      svg.classList.toggle("focus", active !== null);
      const related = new Set([active]);
      relLines.forEach((g) => {
        const on = active !== null && (g.dataset.a === active || g.dataset.b === active);
        g.classList.toggle("hl", on);
        if (on) { related.add(g.dataset.a); related.add(g.dataset.b); }
      });
      tablesG.querySelectorAll(".er-table").forEach((t) => t.classList.toggle("dim", active !== null && !related.has(t.dataset.key)));
    }
    tablesG.addEventListener("click", (e) => { const t = e.target.closest(".er-table"); if (t) highlight(t.dataset.key); });
    tablesG.addEventListener("keydown", (e) => {
      if (e.key === "Enter" || e.key === " ") { const t = e.target.closest(".er-table"); if (t) { e.preventDefault(); highlight(t.dataset.key); } }
    });

    host.appendChild(svg);
    $("erNote").textContent = sc.relations.length
      ? plural(sc.relations.length, "relación definida", "relaciones definidas") + " en Access · clic en una tabla para resaltar las suyas"
      : "Esta base no define relaciones entre tablas.";
  }

  $("erBtn").addEventListener("click", async () => {
    $("erNote").textContent = "Cargando…";
    $("erDialog").showModal();
    try {
      if (!schema || erFor !== imageKey) {
        schema = await api("GET", "/api/schema");
        erFor = imageKey;
        buildDiagram(schema);
      }
    } catch (e) {
      $("erHost").textContent = "";
      $("erHost").appendChild(el("p", { "class": "error" }, e.message));
      $("erNote").textContent = "";
    }
  });

  /* ---------------------------------------------------------------
     Ventanas: cerrar con la X, con "Cancelar" o haciendo clic fuera
  --------------------------------------------------------------- */
  document.querySelectorAll("dialog").forEach((d) => {
    if (d.id === "authDialog") return;
    d.addEventListener("click", (e) => {
      if (e.target.closest("[data-close]") || e.target === d) d.close();
    });
  });

  /* ---------------------------------------------------------------
     Arranque
  --------------------------------------------------------------- */
  hostHint();
  if (!token) needAuth(); else loop();
})();
