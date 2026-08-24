# 请手动挂载以下配置

# Nginx 主配置

```nginx
# ═══════════════════════════════════════════════════
# 声明共享字典和限流区域
# ═══════════════════════════════════════════════════

    # ———— 共享字典 ——————————————————————————————————————
    # su_url 10m：与 config.php 的容量假设一致（四类 limit 总和 30000 条按 10MB 内存推导）
    lua_shared_dict su_url       10m;
    lua_shared_dict su_exp       2m;
    lua_shared_dict su_meta      1m;
    lua_shared_dict su_blocklist 1m;

    # ———— API 限流：每 IP 每分钟 30 次请求 ————————————————
    limit_req_zone $binary_remote_addr zone=api:10m rate=30r/m;

    # ———— 跳转限流：每个 IP 每秒 2 次，突发 10 次 ——————————
    limit_req_zone $binary_remote_addr zone=redirect_burst:10m rate=2r/s;

    # ———— Worker 初始化：注册定时器（每次 worker 启动/reload 均执行）————
    # 注意：init_worker_by_lua_block 中 ngx.var 不可用，配置通过 config.lua 读取
    # expire_sweep 定时器在此注册，确保 nginx reload 后定时器不丢失
    init_worker_by_lua_block {
        package.path = "{prefix}/backend/lua/?.lua;;;" .. package.path
        local init = require "shorturl.init"
        init.init_worker()
    }

    # ═══════════════════════════════════════════════════
    # 内部 Lua API（仅本地访问）
    # ═══════════════════════════════════════════════════
    server {
        listen 18500;
        server_name _;

        # 必须与 api/config.php 的 internal_token_path 指向同一文件
        set $su_internal_token_path "{prefix}/backend/data/internal_token";

        location /internal/ {
            # 访问控制：允许本地回环 + Docker bridge 网段
            # 请将 172.18.0.1/16 替换为你实际的 Docker 网桥子网
            allow 127.0.0.1;
            allow 172.18.0.1/16;
            deny all;

            content_by_lua_block {
                package.path = "{prefix}/backend/lua/?.lua;;;" .. package.path
                local internal = require "shorturl.internal"
                internal.dispatch()
            }
        }
    }
# ═══════════════════════[END]═════════════════════════
```


---

# 站点 Nginx 配置

```nginx
# ═══════════════════════════════════════════════════
# ★ 新增配置
# ═══════════════════════════════════════════════════

    # ★ 业务参数
    set $su_php_fpm          "127.0.0.1:9000";   # PHP-FPM 监听地址，按实际环境修改
    set $su_domain           "https://{你的域名}";
    set $su_tz_offset        "+08:00";
    set $su_perm_path        "{prefix}/backend/data/perm.json";
    set $su_temp_path        "{prefix}/backend/data/temp.json";
    set $su_expire_interval  3600;
    set $su_redirect_code    302;   # 短链跳转状态码，可选 301（永久）或 302（临时）

    # ★ 安全响应头（补充 ShortURL 需要的）
    add_header X-Content-Type-Options  "nosniff"          always;
    add_header X-Frame-Options         "DENY"             always;
    add_header Referrer-Policy         "no-referrer"      always;

    # ★ 屏蔽敏感目录
    location ^~ /data/ {
        deny all;
        return 404;
    }
    location ^~ /lua/ {
        deny all;
        return 404;
    }
    location ^~ /backend/ {
        deny all;
        return 404;
    }
    location ^~ /api/common/ {
        deny all;
        return 404;
    }
    location ^~ /api/key/ {
        deny all;
        return 404;
    }
    location ^~ /api/lua/ {
        deny all;
        return 404;
    }
    location ^~ /api/storage/ {
        deny all;
        return 404;
    }
    location ^~ /headless/ {
        deny all;
        return 404;
    }
    # ^~ 前缀匹配，拦截所有 /internal/* 路径（不仅是精确的 /internal/）
    location ^~ /internal/ {
        deny all;
        return 404;
    }

    #———— ★ API 路由 → PHP-FPM ————————————————————————————————————————————————————
    location ^~ /api/ {
        limit_req zone=api burst=5 nodelay;

        location = /api/create {
            limit_except POST { deny all; }
            include fastcgi_params;
            fastcgi_pass $su_php_fpm;
            fastcgi_index index.php;
            fastcgi_param SCRIPT_FILENAME {prefix}/api/routes/create.php;
        }

        location = /api/delete {
            limit_except POST { deny all; }
            include fastcgi_params;
            fastcgi_pass $su_php_fpm;
            fastcgi_index index.php;
            fastcgi_param SCRIPT_FILENAME {prefix}/api/routes/delete.php;
        }

        location = /api/list {
            limit_except GET { deny all; }
            include fastcgi_params;
            fastcgi_pass $su_php_fpm;
            fastcgi_index index.php;
            fastcgi_param SCRIPT_FILENAME {prefix}/api/routes/list.php;
        }

        location = /api/stat {
            limit_except GET { deny all; }
            include fastcgi_params;
            fastcgi_pass $su_php_fpm;
            fastcgi_index index.php;
            fastcgi_param SCRIPT_FILENAME {prefix}/api/routes/stat.php;
        }

        location = /api/ping {
            limit_except GET { deny all; }
            include fastcgi_params;
            fastcgi_pass $su_php_fpm;
            fastcgi_index index.php;
            fastcgi_param SCRIPT_FILENAME {prefix}/api/ping.php;
        }
    }

    #———— ★ 无头链路路由 → PHP-FPM（nurl-key 远程管理工具使用）——————————————
    location ^~ /headless/api/ {
        limit_req zone=api burst=5 nodelay;

        location = /headless/api/create {
            limit_except POST { deny all; }
            include fastcgi_params;
            fastcgi_pass $su_php_fpm;
            fastcgi_index index.php;
            fastcgi_param SCRIPT_FILENAME {prefix}/headless/create.php;
        }

        location = /headless/api/delete {
            limit_except POST { deny all; }
            include fastcgi_params;
            fastcgi_pass $su_php_fpm;
            fastcgi_index index.php;
            fastcgi_param SCRIPT_FILENAME {prefix}/headless/delete.php;
        }

        location = /headless/api/list {
            limit_except GET { deny all; }
            include fastcgi_params;
            fastcgi_pass $su_php_fpm;
            fastcgi_index index.php;
            fastcgi_param SCRIPT_FILENAME {prefix}/headless/list.php;
        }

        location = /headless/api/stat {
            limit_except GET { deny all; }
            include fastcgi_params;
            fastcgi_pass $su_php_fpm;
            fastcgi_index index.php;
            fastcgi_param SCRIPT_FILENAME {prefix}/headless/stat.php;
        }

        # ~* 大小写不敏感，与其他接口对齐；PHP 侧已做 strtolower
        location ~* "^/headless/api/get/([0-9a-zA-Z-]{1,5})$" {
            limit_except GET { deny all; }
            include fastcgi_params;
            fastcgi_pass $su_php_fpm;
            fastcgi_index index.php;
            fastcgi_param SCRIPT_FILENAME {prefix}/headless/get.php;
            fastcgi_param PATH_INFO       /$1;
        }
    }

    # ————★ 一次性数据加载（惰性触发）————————————————————————————————
    # 每个 worker 首次请求时执行冷启动（从 JSON 加载到 shared dict），后续请求跳过
    # 注意：定时器注册已移至 init_worker_by_lua_block（见主配置），此处仅负责数据加载
    access_by_lua_block {
        package.path = "{prefix}/backend/lua/?.lua;;;" .. package.path
        local su_meta = ngx.shared.su_meta
        if not su_meta:get("inited") then
            local init = require "shorturl.init"
            init.init()
        end
    }

    # ————★ 短链跳转（最低优先级，匹配 1-5 位字母数字）————————————————————
    # 正则中的花括号必须用双引号包裹，否则 Nginx 会报错
    location ~* "^/([0-9a-z-]{1,5})$" {
        limit_req zone=redirect_burst burst=10 nodelay;
        limit_except GET { deny all; }
        content_by_lua_block {
            package.path = "{prefix}/backend/lua/?.lua;;;" .. package.path
            local redirect = require "shorturl.redirect"
            redirect.go()
        }
    }
    
# ══════════════════════[END]═════════════════════════════
```
