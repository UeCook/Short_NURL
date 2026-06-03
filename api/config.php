<?php
return [
    // 短链域名
    'domain'           => 'https://{你的域名}',  // 改为你的短链域名，用于拼接完整短链

    // 时区偏移（ISO 8601 格式）
    // 影响过期时间的显示和计算
    'tz_offset'        => '+08:00',

    // 冷存储 JSON 文件路径
    'perm_path'        => __DIR__ . '/../backend/data/perm.json',  // 永久短链数据文件
    'temp_path'        => __DIR__ . '/../backend/data/temp.json',  // 临时短链数据文件

    // API Key 存储
    'keys_path'        => __DIR__ . '/../backend/data/keys.json',                            // Key 存储文件
    'key_ttl_days'     => 7,                                                                 // 常驻 Key 有效期（天）
    'onetime_pool_size' => 20,                                                               // 一次性 Key 池大小
    'key_charset'      => '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz',  // Key 字符集，无需修改
    'key_length'       => 64,                                                                // Key 长度，无需修改

    // 数量限制，本服务最大上限均为 9999
    'perm_limit'       => 9999, // 永久短链
    'temp_limit'       => 9999, // 临时短链

    // TTL 上限，临时短链最长存活时间（秒），默认 1 年
    'ttl_max'          => 365 * 24 * 3600,

    // 内部 OpenResty API 地址（仅本地 18500 端口，不对外暴露）
    // 注意！！ 如果 PHP 容器使用 bridge 网络，127.0.0.1 指向的是容器自身，访问不到 OpenResty，此时需改为 Docker 网桥的宿主机 IP（例如172.19.0.1）
    'internal_host'    => 'http://172.19.0.1:18500',  //具体内网地址请自行查阅！
    'internal_timeout' => 2.0,                       //内部接口请求超时时间（秒）
];
