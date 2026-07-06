<?php
return [
    // 短链域名
    'domain'           => 'https://{你的域名}',  // 改为你的短链域名，用于拼接完整短链

    // 前端面板域名（用于 CORS，与短链跳转域名独立）
    // 如果前端面板与短链服务部署在不同域名，请改为面板实际域名（如 https://panel.example.com）
    // 未配置时回退使用 domain，保持向后兼容
    'panel_origin'     => '',

    // 前端面板总开关
    // false 时 PHP 层直接返回 403，前端不再可访问（短链跳转不受影响）
    // 同时建议在 nginx 的 /api/ 块启用 return 403（双层独立生效）
    'panel_enabled'    => true,

    // 时区设置（影响过期时间的显示和计算）
    //
    // ┌─────────────────────────────────────────────────────────────────────┐
    // │ 格式 1：固定偏移（不处理夏令时）                                      │
    // │   '+08:00'  — 中国标准时间（UTC+8）                                  │
    // │   '+09:00'  — 日本标准时间（UTC+9）                                  │
    // │   '+05:30'  — 印度标准时间（UTC+5:30）                               │
    // │   '-05:00'  — 美国东部标准时间（UTC-5）                              │
    // │   '+00:00'  — UTC/GMT                                              │
    // │                                                                     │
    // │ 格式 2：时区名称（自动处理夏令时）                                    │
    // │   'Asia/Shanghai'      — 中国（无夏令时，等效 +08:00）               │
    // │   'Asia/Tokyo'         — 日本（无夏令时，等效 +09:00）               │
    // │   'America/New_York'   — 美国东部（自动 EST↔EDT 切换）              │
    // │   'America/Los_Angeles'— 美国西部（自动 PST↔PDT 切换）              │
    // │   'Europe/London'      — 英国（自动 GMT↔BST 切换）                  │
    // │   'Europe/Berlin'      — 中欧（自动 CET↔CEST 切换）                 │
    // │   'Australia/Sydney'   — 悉尼（自动 AEST↔AEDT 切换）                │
    // │   'Pacific/Auckland'   — 新西兰（自动 NZST↔NZDT 切换）              │
    // └─────────────────────────────────────────────────────────────────────┘
    //
    // 中国/日本等无夏令时的地区，两种格式效果相同；
    // 美国/欧洲/澳洲等有夏令时的地区，建议使用时区名称以自动切换。
    'tz_offset'        => '+08:00',

    // 冷存储 JSON 文件路径
    'perm_path'        => __DIR__ . '/../backend/data/perm.json',  // 永久短链数据文件
    'temp_path'        => __DIR__ . '/../backend/data/temp.json',  // 临时短链数据文件

    // API Key 存储
    'keys_path'        => __DIR__ . '/../backend/data/keys.json',  // Key 存储文件
    'key_ttl_days'     => 7,                                       // 常驻 Key 有效期（天）
    'onetime_pool_size' => 20,                                     // 一次性 Key 池大小

    // 数量限制（所有类型总和不得超过 30000，受 10MB lua_shared_dict 内存限制）
    // 如需更多容量，请同步调整 nginx 的 lua_shared_dict su_url 大小
    'perm_limit'       => 10000, // 用户永久短链
    'temp_limit'       => 500,   // 用户临时短链

    // 服务密钥专用配置（仅无头链路生效）
    'svc_code_length'  => 4,     // 服务密钥生成的短码长度：4 或 5
    'svc_perm_limit'   => 18000, // 服务密钥永久短链上限
    'svc_temp_limit'   => 1500,  // 服务密钥临时短链上限

    // TTL 上限，临时短链最长存活时间（秒），默认 1 年
    'ttl_max'          => 365 * 24 * 3600,

    // 保留短码（与 nginx 路由前缀对齐，禁止用户注册为自定义短码）
    // 新增路由时需同步更新此列表。已补齐 headless / internal
    'reserved_codes'   => ['api', 'stat', 'admin', 'data', 'lua', 'headless', 'internal'],

    // 内部 OpenResty API 地址（仅本地 18500 端口，不对外暴露）
    // 注意！！ 如果 PHP 容器使用 bridge 网络，127.0.0.1 指向的是容器自身，访问不到 OpenResty，此时需改为 Docker 网桥的宿主机 IP（例如172.19.0.1）
    //如果你的 PHP 是直接部署的，则无需改网桥IP，保持 127.0.0.1 即可
    'internal_host'    => 'http://127.0.0.1:18500',   //具体内网地址请自行查阅！
    'internal_timeout' => 2.0,                        //内部接口请求超时时间（秒）
    'internal_token_path' => __DIR__ . '/../backend/data/internal_token',
    // internal_token 不在此缓存（PHP-FPM worker 会持有整个生命周期，轮换后不生效）。
    // 改为在 api/lua/internal.php 每次调用时从文件读取，保证 nurl -itk 轮换立即生效。
];
