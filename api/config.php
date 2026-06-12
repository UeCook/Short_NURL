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

    // 时区偏移（ISO 8601 格式）
    // 影响过期时间的显示和计算
    'tz_offset'        => '+08:00',

    // 冷存储 JSON 文件路径
    'perm_path'        => __DIR__ . '/../backend/data/perm.json',  // 永久短链数据文件
    'temp_path'        => __DIR__ . '/../backend/data/temp.json',  // 临时短链数据文件

    // API Key 存储
    'keys_path'        => __DIR__ . '/../backend/data/keys.json',  // Key 存储文件
    'key_ttl_days'     => 7,                                       // 常驻 Key 有效期（天）
    'onetime_pool_size' => 20,                                     // 一次性 Key 池大小

    // 数量限制，本服务最大上限均为 9999
    'perm_limit'       => 9999, // 永久短链
    'temp_limit'       => 9999, // 临时短链

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
    'internal_token'      => trim(@file_get_contents(__DIR__ . '/../backend/data/internal_token') ?: ''),
];
