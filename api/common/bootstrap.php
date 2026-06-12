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

// ── 前端面板总开关（PHP 层）─────────────────────────
// 关闭时直接返回 403，所有 /api/* 接口不可访问
// 短链跳转走 Lua，不受此影响；nginx 层的 return 403 与此独立生效
if (empty($cfg['panel_enabled'])) {
    http_response_code(403);
    echo json_encode(['error' => '前端面板未启用'], JSON_UNESCAPED_UNICODE);
    exit;
}

// 仅允许配置域名跨域访问（而非通配符 *）
// panel_origin 为前端面板域名（与短链跳转域名独立），未配置时回退使用 domain
$allowOrigin = $cfg['panel_origin'] ?? '';
if ($allowOrigin === '') {
    $allowOrigin = $cfg['domain'] ?? '';
}
header('Access-Control-Allow-Origin: ' . $allowOrigin);
header('Access-Control-Allow-Headers: Content-Type, X-Token');
header('Access-Control-Allow-Methods: GET, POST, OPTIONS');
if (($_SERVER['REQUEST_METHOD'] ?? '') === 'OPTIONS') { http_response_code(204); exit; }

// 加载公共库
require_once __DIR__ . '/helpers.php';
require_once __DIR__ . '/../key/keys.php';
require_once __DIR__ . '/../storage/json_store.php';
require_once __DIR__ . '/../lua/internal.php';

// ── 检查数据文件可读写性（认证之前，先于密钥校验）──────────────
// 权限问题返回 430 并列出具体文件，避免误报 406"密钥失效"
$checkFiles = $checkFiles ?? [
    $cfg['perm_path']  ?? '',
    $cfg['temp_path']  ?? '',
    $cfg['keys_path']  ?? '',
];
$failedFiles = [];
foreach ($checkFiles as $f) {
    if (!checkFileAccess($f)) {
        $failedFiles[] = basename($f);
    }
}
if (!empty($failedFiles)) {
    http_response_code(430);
    echo json_encode(['error' => '数据不可访问：' . implode('、', $failedFiles)], JSON_UNESCAPED_UNICODE);
    exit;
}

// ── 认证解析（公共化，所有 api 接口共享）──────────────
// php://input 只能读一次，提前缓存供 auth_extract 和业务文件使用
// 显式写入 $GLOBALS 避免被函数内 require 时局部作用域导致 $GLOBALS['RAW_INPUT'] 为空
$GLOBALS['RAW_INPUT'] = @file_get_contents('php://input');
if ($GLOBALS['RAW_INPUT'] === false) {
    $GLOBALS['RAW_INPUT'] = '';
}

require_once __DIR__ . '/../key/auth_extract.php';
require_once __DIR__ . '/../key/auth_verify.php';

// @外调用_&3：auth_extract — API 链路认证凭证提取（仅接受 X-Token / body.key）
$rawCredential = auth_extract($GLOBALS['RAW_INPUT'], 'api');
// @外调用_&4：auth_verify — API 链路认证凭证验证，返回 $authCtx
$AUTH = auth_verify($rawCredential ?? '', 'api');

if (!$AUTH['valid']) {
    http_response_code(406);
    if ($AUTH['reason'] === 'missing') {
        echo json_encode(['error' => '缺少密钥'], JSON_UNESCAPED_UNICODE);
    } elseif ($AUTH['reason'] === 'wrong_channel') {
        echo json_encode(['error' => '服务密钥不可用于前端API链路'], JSON_UNESCAPED_UNICODE);
    } else {
        echo json_encode(['error' => '密钥失效或不正确'], JSON_UNESCAPED_UNICODE);
    }
    exit;
}

// getKeyStore() 已提取至 helpers.php，由 function_exists 守卫防止重复定义
