<?php
// Prueba de humo de `accesql serve` con los drivers PHP reales (PDO y mysqli).
//
// Requiere php-cli + php-mysql y un servidor corriendo con Northwind.MDB:
//   ACCESQL_PASSWORD=secreta ./bin/accesql serve -f testdb/Northwind.MDB -p 3307 &
//   php scripts/php_smoke_test.php 127.0.0.1 3307 access secreta
//
// Sale con código 1 si alguna comprobación falla.

[$_, $host, $port, $user, $pass] = $argv + [null, '127.0.0.1', '3307', 'access', ''];
$fails = 0;

function check(string $name, bool $ok, string $detail = ''): void {
    global $fails;
    echo ($ok ? "  OK   " : "  FAIL ") . $name . ($detail !== '' ? "  [$detail]" : '') . "\n";
    if (!$ok) $fails++;
}

function expectThrows(string $name, callable $fn, string $contains = ''): void {
    try {
        $fn();
        check($name, false, 'no lanzó excepción');
    } catch (Throwable $e) {
        check($name, $contains === '' || stripos($e->getMessage(), $contains) !== false, substr($e->getMessage(), 0, 70));
    }
}

$dsn = "mysql:host=$host;port=$port;dbname=access;charset=utf8mb4";

echo "## PDO (preparadas emuladas, el valor por defecto)\n";
$pdo = new PDO($dsn, $user, $pass, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
$rows = $pdo->query('SELECT CustomerID, ContactName, City FROM Customers ORDER BY CustomerID LIMIT 5')->fetchAll(PDO::FETCH_ASSOC);
check('SELECT simple devuelve 5 filas', count($rows) === 5);
check('acentos correctos en UTF-8', $rows[1]['City'] === 'México D.F.' && $rows[4]['City'] === 'Luleå', $rows[1]['City'] . ' / ' . $rows[4]['City']);
$json = json_encode($rows, JSON_UNESCAPED_UNICODE);
check('json_encode sin errores', $json !== false && str_contains($json, 'México D.F.'));
echo "       $json\n";

$st = $pdo->prepare('SELECT COUNT(*) FROM Customers WHERE City = ?');
$st->execute(['México D.F.']);
$exacto = (int)$st->fetchColumn();
$st->execute(['méxico d.f.']);
$otraCaja = (int)$st->fetchColumn();
check('preparada: "méxico d.f." encuentra lo mismo que "México D.F."', $exacto >= 1 && $otraCaja === $exacto, "exacto=$exacto minúsculas=$otraCaja");

$st = $pdo->prepare('SELECT OrderID, Freight, OrderDate FROM Orders WHERE CustomerID = :c ORDER BY OrderID LIMIT 1');
$st->execute([':c' => 'VINET']);
$o = $st->fetch(PDO::FETCH_ASSOC);
check('parámetro con nombre', $o['OrderID'] == 10248, json_encode($o));
check('decimal exacto', (string)$o['Freight'] === '32.38', var_export($o['Freight'], true));
check('fecha como texto sin zona', str_starts_with((string)$o['OrderDate'], '1994-08-04'), (string)$o['OrderDate']);

$agg = $pdo->query('SELECT c.Country, COUNT(*) AS pedidos, SUM(d.Quantity) AS unidades FROM Customers c JOIN Orders o ON o.CustomerID=c.CustomerID JOIN `Order Details` d ON d.OrderID=o.OrderID GROUP BY c.Country ORDER BY pedidos DESC LIMIT 1')->fetch(PDO::FETCH_ASSOC);
check('JOIN + GROUP BY', isset($agg['Country']) && $agg['pedidos'] > 0, json_encode($agg));

echo "## PDO con preparadas NATIVAS (ATTR_EMULATE_PREPARES = false)\n";
$pdo2 = new PDO($dsn, $user, $pass, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION, PDO::ATTR_EMULATE_PREPARES => false]);
$st = $pdo2->prepare('SELECT OrderID, Freight, OrderDate, ShipCountry FROM Orders WHERE CustomerID = ? AND ShipCountry LIKE ? ORDER BY OrderID LIMIT 2');
$st->execute(['VINET', 'fr%']);
$r = $st->fetchAll(PDO::FETCH_ASSOC);
check('preparada nativa con 2 parámetros', count($r) === 2, json_encode($r));
check('entero nativo llega como int', is_int($r[0]['OrderID']), gettype($r[0]['OrderID']));
$col = $st->getColumnMeta(1);
check('metadatos de columna decimal', isset($col['native_type']), json_encode($col['native_type'] ?? null) . ' ' . ($col['name'] ?? ''));
$st = $pdo2->prepare('SELECT COUNT(*) FROM Customers WHERE CompanyName = ?');
foreach (['Alfreds Futterkiste', 'ALFREDS FUTTERKISTE'] as $v) { $st->execute([$v]); check("mayúsculas: $v", (int)$st->fetchColumn() === 1); }

echo "## PDO: transacciones y comandos de sesión\n";
$pdo->beginTransaction();
$v = $pdo->query('SELECT 1')->fetchColumn();
$pdo->commit();
check('beginTransaction / commit', (int)$v === 1);
$pdo->exec("SET NAMES utf8mb4");
check('SET NAMES', true);

echo "## PDO: solo lectura y errores\n";
expectThrows('INSERT rechazado', fn() => $pdo->exec("INSERT INTO Shippers (ShipperID, CompanyName) VALUES (99, 'x')"), 'read only');
expectThrows('UPDATE rechazado', fn() => $pdo->exec("UPDATE Customers SET City='x'"), 'read only');
expectThrows('DELETE rechazado', fn() => $pdo->exec("DELETE FROM Customers"), 'read only');
expectThrows('tabla inexistente', fn() => $pdo->query('SELECT * FROM NoExiste'), 'not found');
expectThrows('clave incorrecta', fn() => new PDO($dsn, $user, 'mala', [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]), 'denied');
expectThrows('usuario incorrecto', fn() => new PDO($dsn, 'otro', $pass, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]));
expectThrows('base inexistente', fn() => new PDO("mysql:host=$host;port=$port;dbname=otra", $user, $pass, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]), 'not found');
$f = $pdo->query("SELECT LOAD_FILE('/etc/passwd')")->fetchColumn();
check('LOAD_FILE no lee archivos', $f === null || $f === false, var_export($f, true));
expectThrows('INTO OUTFILE rechazado', fn() => $pdo->query("SELECT 'x' INTO OUTFILE '/tmp/php_pwn.txt'"));
check('no se creó /tmp/php_pwn.txt', !file_exists('/tmp/php_pwn.txt'));

echo "## mysqli\n";
mysqli_report(MYSQLI_REPORT_ERROR | MYSQLI_REPORT_STRICT);
$m = new mysqli($host, $user, $pass, 'access', (int)$port);
$m->set_charset('utf8mb4');
$res = $m->query('SELECT CustomerID, City FROM Customers ORDER BY CustomerID LIMIT 3');
$all = $res->fetch_all(MYSQLI_ASSOC);
check('mysqli query + fetch_all', count($all) === 3 && $all[1]['City'] === 'México D.F.', json_encode($all, JSON_UNESCAPED_UNICODE));
$stmt = $m->prepare('SELECT OrderID, Freight FROM Orders WHERE CustomerID = ? AND OrderID > ? ORDER BY OrderID LIMIT 2');
$cid = 'VINET'; $min = 10000;
$stmt->bind_param('si', $cid, $min);
$stmt->execute();
$rs = $stmt->get_result()->fetch_all(MYSQLI_ASSOC);
check('mysqli preparada bind_param', count($rs) === 2 && is_int($rs[0]['OrderID']), json_encode($rs));
expectThrows('mysqli DELETE rechazado', fn() => $m->query("DELETE FROM Orders"), 'read only');
$m->close();

echo "## Conexiones y volumen\n";
$t = microtime(true);
for ($i = 0; $i < 100; $i++) {
    $p = new PDO($dsn, $user, $pass);
    $p->query('SELECT COUNT(*) FROM Customers')->fetchColumn();
}
check('100 conexiones nuevas seguidas', true, sprintf('%.0f ms', (microtime(true) - $t) * 1000));
$t = microtime(true);
$big = $pdo->query('SELECT * FROM `Order Details`')->fetchAll(PDO::FETCH_ASSOC);
check('fetchAll de 2155 filas', count($big) === 2155, sprintf('%d filas, %.0f ms', count($big), (microtime(true) - $t) * 1000));

echo "\n" . ($fails === 0 ? "TODO OK\n" : "$fails comprobaciones FALLARON\n");
exit($fails === 0 ? 0 : 1);
