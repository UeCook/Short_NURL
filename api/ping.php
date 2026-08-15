<?php
/**
 * GET /api/ping - 健康检查端点
 *
 * 用途：部署后探活（nginx 健康检查、监控拨测），仅确认 PHP-FPM 与路由可用。
 * 无认证要求、不暴露任何业务数据，因此不加载 api/common/bootstrap.php。
 * 对应 nginx.md 中的 location = /api/ping 路由。
 */
header('Content-Type: application/json; charset=utf-8');
http_response_code(200);
echo json_encode(['ok' => true], JSON_UNESCAPED_UNICODE);
