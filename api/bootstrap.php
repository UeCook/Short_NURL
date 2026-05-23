<?php
/**
 * 引导文件 — 所有 API 接口的公共初始化
 *
 * 1. 加载配置
 * 2. 设置响应头（Content-Type、CORS）
 * 3. 处理 OPTIONS 预检请求
 * 4. 加载公共库
 *
 * 认证由各接口自行处理：
 *   - POST 接口（create、delete）：key 在请求体 JSON 中
 *   - GET 接口（list、stat）：key 在 X-Token 请求头中
 */

header('Content-Type: application/json; charset=utf-8');

$cfg = require __DIR__ . '/config.php';

// 仅允许配置域名跨域访问（而非通配符 *）
$allowOrigin = $cfg['domain'] ?? '';
header('Access-Control-Allow-Origin: ' . $allowOrigin);
header('Access-Control-Allow-Headers: Content-Type, X-Token');
header('Access-Control-Allow-Methods: GET, POST, OPTIONS');
if ($_SERVER['REQUEST_METHOD'] === 'OPTIONS') { http_response_code(204); exit; }

// 加载公共库
require_once __DIR__ . '/common/helpers.php';
require_once __DIR__ . '/common/keys.php';
require_once __DIR__ . '/common/json_store.php';
require_once __DIR__ . '/common/internal.php';

// ── 检查数据文件可读写性 ──────────────────────────────
// data 目录（777）及内部文件（666）权限缺失时，提前返回 430
// 避免被误判为 Key 无效（406）
// 支持按需检查：接口文件可在 require bootstrap.php 之前设置 $checkFiles 数组
$checkFiles = $checkFiles ?? [
    $cfg['perm_path']  ?? '',
    $cfg['temp_path']  ?? '',
    $cfg['keys_path']  ?? '',
];
foreach ($checkFiles as $f) {
    if (!checkFileAccess($f)) {
        http_response_code(430);
        echo json_encode(['error' => '数据不可访问'], JSON_UNESCAPED_UNICODE);
        exit;
    }
}

/**
 * 获取全局配置的 KeyStore 实例
 */
// @关键_$23：getKeyStore — 获取全局 KeyStore 单例（延迟初始化）
function getKeyStore() {
    global $cfg;
    static $instance = null;
    if ($instance === null) {
        $instance = new KeyStore(
            $cfg['keys_path'],
            $cfg['tz_offset'],
            $cfg['key_ttl_days'],
            $cfg['onetime_pool_size'],
            $cfg['key_charset'],
            $cfg['key_length']
        );
    }
    return $instance;
}
