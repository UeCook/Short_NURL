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
 * 与 api/bootstrap.php 的差异：
 *   - 不设置 CORS（无浏览器调用）
 *   - 认证方式不同（统一 X-Headless-Token）
 *   - 错误格式不同（{ "error": "code", "message": "中文" }）
 */

header('Content-Type: application/json; charset=utf-8');

$cfg = require __DIR__ . '/../api/config.php';

// 无头链路不需要 CORS，仅处理 OPTIONS 预检
if ($_SERVER['REQUEST_METHOD'] === 'OPTIONS') { http_response_code(204); exit; }

// 加载公共库（复用 api/common/）
require_once __DIR__ . '/../api/common/helpers.php';
require_once __DIR__ . '/../api/common/keys.php';
require_once __DIR__ . '/../api/common/json_store.php';
require_once __DIR__ . '/../api/common/internal.php';

/**
 * 无头链路统一错误输出
 *
 * @param string $code        错误代码（英文标识符，如 "missing_key"）
 * @param string $message     人类可读的中文错误描述
 * @param int    $httpStatus  HTTP 状态码
 */
// @关键_$24：hl_error — 无头链路统一错误输出（{error:code, message:中文} 格式）
function hl_error($code, $message, $httpStatus) {
    http_response_code($httpStatus);
    echo json_encode(['error' => $code, 'message' => $message], JSON_UNESCAPED_UNICODE);
    exit;
}

// ── 检查数据文件可读写性（必须在认证之前）──────────
// data 目录（777）及内部文件（666）权限缺失时，提前返回 430
// 避免被误判为 Key 无效（406）
if (!checkDataAccess($cfg)) {
    hl_error('data_inaccessible', '数据不可访问', 430);
}

/**
 * 无头链路认证
 * 仅接受 X-Headless-Token 请求头，无 fallback
 */
$rawToken = $_SERVER['HTTP_X_HEADLESS_TOKEN'] ?? '';
if (empty($rawToken)) {
    hl_error('missing_key', '缺少密钥', 406);
}
// @外调用_&10：getKeyStore()->verify — 验证无头链路 API Key
$keyType = getKeyStore()->verify($rawToken);
if (!$keyType) {
    hl_error('invalid_key', '密钥失效或不正确', 406);
}

/**
 * 获取全局配置的 KeyStore 实例
 * 与 api/bootstrap.php 中的完全一致，确保共享同一实例
 */
// @关键_$25：getKeyStore（无头版）— 获取全局 KeyStore 单例（延迟初始化）
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
