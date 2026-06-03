<?php
/**
 * 引导文件 — 所有 API 接口的公共初始化
 *
 * 1. 加载配置
 * 2. 设置响应头（Content-Type、CORS）
 * 3. 处理 OPTIONS 预检请求
 * 4. 加载公共库
 *
 * 认证已统一在 bootstrap 末尾处理（auth_extract + auth_verify）。
 * 到达 require 末尾时，$AUTH['valid'] === true 已保证。
 */

header('Content-Type: application/json; charset=utf-8');

$cfg = require __DIR__ . '/../config.php';

// 仅允许配置域名跨域访问（而非通配符 *）
$allowOrigin = $cfg['domain'] ?? '';
header('Access-Control-Allow-Origin: ' . $allowOrigin);
header('Access-Control-Allow-Headers: Content-Type, X-Token');
header('Access-Control-Allow-Methods: GET, POST, OPTIONS');
if ($_SERVER['REQUEST_METHOD'] === 'OPTIONS') { http_response_code(204); exit; }

// 加载公共库
require_once __DIR__ . '/helpers.php';
require_once __DIR__ . '/../key/keys.php';
require_once __DIR__ . '/../storage/json_store.php';
require_once __DIR__ . '/../lua/internal.php';

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

// ── 认证解析（公共化，所有 api 接口共享）──────────────
// php://input 只能读一次，提前缓存供 auth_extract 和业务文件使用
$RAW_INPUT = file_get_contents('php://input');

require_once __DIR__ . '/../key/auth_extract.php';
require_once __DIR__ . '/../key/auth_verify.php';

// @外调用_&3：auth_extract — API 链路认证凭证提取
$rawCredential = auth_extract($RAW_INPUT);
// @外调用_&4：auth_verify — API 链路认证凭证验证，返回 $authCtx
$AUTH = auth_verify($rawCredential ?? '');

if (!$AUTH['valid']) {
    http_response_code(406);
    if ($AUTH['reason'] === 'missing') {
        echo json_encode(['error' => '缺少密钥'], JSON_UNESCAPED_UNICODE);
    } else {
        echo json_encode(['error' => '密钥失效或不正确'], JSON_UNESCAPED_UNICODE);
    }
    exit;
}
