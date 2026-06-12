<?php
/**
 * 无头链路引导文件 — headless/* 所有接口的公共初始化
 *
 * 1. 加载配置（复用 api/config.php）
 * 2. 设置响应头（Content-Type，不设 CORS — 无头客户端不需要）
 * 3. 处理 OPTIONS 预检请求
 * 4. 加载公共库（复用 api/common/）
 * 5. 无头认证：仅接受 X-Headless-Token 请求头
 *
 * 与 api/common/bootstrap.php 的差异：
 *   - 不设置 CORS（无浏览器调用）
 *   - 认证方式不同（统一 X-Headless-Token）
 *   - 错误格式不同（{ "error": "code", "message": "中文" }）
 */

header('Content-Type: application/json; charset=utf-8');

$cfg = require __DIR__ . '/../api/config.php';

// 无头链路不需要 CORS，仅处理 OPTIONS 预检
if (($_SERVER['REQUEST_METHOD'] ?? '') === 'OPTIONS') { http_response_code(204); exit; }

// 加载公共库（复用 api/ 模块）
require_once __DIR__ . '/../api/common/helpers.php';
require_once __DIR__ . '/../api/key/keys.php';
require_once __DIR__ . '/../api/storage/json_store.php';
require_once __DIR__ . '/../api/lua/internal.php';

/**
 * 无头链路统一错误输出
 *
 * @param string $code        错误代码（英文标识符，如 "missing_key"）
 * @param string $message     人类可读的中文错误描述
 * @param int    $httpStatus  HTTP 状态码
 */
// @关键_$22：hl_error — 无头链路统一错误输出（{error:code, message:中文} 格式）
function hl_error($code, $message, $httpStatus) {
    http_response_code($httpStatus);
    echo json_encode(['error' => $code, 'message' => $message], JSON_UNESCAPED_UNICODE);
    exit;
}

// ── 认证解析（与 api 链路共享同一套公共函数）─────────
// php://input 只能读一次，提前缓存
// 显式写入 $GLOBALS 避免被函数内 require 时局部作用域导致 $GLOBALS['RAW_INPUT'] 为空
$GLOBALS['RAW_INPUT'] = @file_get_contents('php://input');
if ($GLOBALS['RAW_INPUT'] === false) {
    $GLOBALS['RAW_INPUT'] = '';
}

require_once __DIR__ . '/../api/key/auth_extract.php';
require_once __DIR__ . '/../api/key/auth_verify.php';

// @外调用_&5：auth_extract — 无头链路认证凭证提取（仅接受 X-Headless-Token）
$rawCredential = auth_extract($GLOBALS['RAW_INPUT'], 'headless');
// @外调用_&6：auth_verify — 无头链路认证凭证验证，返回 $authCtx
$AUTH = auth_verify($rawCredential ?? '', 'headless');

if (!$AUTH['valid']) {
    if ($AUTH['reason'] === 'missing') {
        hl_error('missing_key', '缺少密钥', 406);
    } else {
        hl_error('invalid_key', '密钥失效或不正确', 406);
    }
}

// ── 检查数据文件可读写性（认证通过后）──────────────
// 支持文件不存在时检查目录可写性（允许首次部署时自动创建）
if (!checkDataAccess($cfg)) {
    hl_error('data_inaccessible', '数据不可访问', 430);
}

// getKeyStore() 已提取至 helpers.php，由 function_exists 守卫防止重复定义
