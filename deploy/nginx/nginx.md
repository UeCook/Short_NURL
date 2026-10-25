## Nginx 主配置

```nginx lineNumbers title="nginx.conf" {21,27}
    # ═══════════════════════════════════════════════════
    # 声明共享字典和限流区域
    # 放置在主 Nginx 配置中
    # ═══════════════════════════════════════════════════
    # ———— 共享字典 ——————————————————————————————————————
    lua_shared_dict su_url       10m;
    lua_shared_dict su_exp       2m;
    lua_shared_dict su_meta      1m;
    lua_shared_dict su_blocklist 1m;
    # ———— API 限流：每 IP 每分钟 30 次请求 ————————————————
    limit_req_zone $binary_remote_addr zone=api:10m rate=30r/m;
    # ———— 跳转限流：每个 IP 每秒 2 次，突发 10 次 ——————————
    limit_req_zone $binary_remote_addr zone=redirect_burst:10m rate=2r/s;
    # ═══════════════════════════════════════════════════
    # 内部 Lua API（仅本地 18500 端口）
    # ═══════════════════════════════════════════════════
    server {
        listen 18500;
        server_name _;
        # 必须与后端的 internal_token_path 指向同一文件
        set $su_internal_token_path "{部署目录}/backend/data/internal_token";
        location /internal/ {
            allow 127.0.0.1;
            allow 172.18.0.1/16;
            deny all;
            content_by_lua_block {
                package.path = "{部署目录}/backend/lua/?.lua;;;" .. package.path
                local internal = require "shorturl.internal"
                internal.dispatch()
            }
        }
    }
    # ═══════════════════════════════════[END]═══════════════════════
```

---

## 站点 Nginx 配置

```nginx lineNumbers title="nginx.conf" {5,7,8,80,97}
    # ═══════════════════════════════════════════════════
    # ★ ShortURL 配置（后端 nurl-server @ 127.0.0.1:8080）
    # ═══════════════════════════════════════════════════
    # ★ 业务参数
    set $su_domain "https://{你的域名}"; 
    set $su_tz_offset "+08:00"; 
    set $su_perm_path "{部署目录}/backend/data/perm.json"; 
    set $su_temp_path "{部署目录}/backend/data/temp.json"; 
    set $su_expire_interval 3600; 
    set $su_redirect_code 302; 
    client_max_body_size 1m; 
    
    # ★ 安全响应头
    add_header X-Content-Type-Options "nosniff" always; 
    add_header X-Frame-Options "DENY" always; 
    add_header Referrer-Policy "no-referrer" always; 
    
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
    location ^~ /internal/ {
        deny all; 
        return 404; 
    }
    location ^~ /config/ {
        deny all; 
        return 404; 
    }
    location ^~ /deploy/ {
        deny all; 
        return 404; 
    }
    
    #———— ★ API 路由 → Go 后端 ——————————————————————————————————————
    location ^~ /api/ {
        limit_req zone=api burst=5 nodelay; 
        limit_req_status 429; 
        add_header Cache-Control "no-store" always; 
        proxy_pass http://127.0.0.1:8080; 
        proxy_http_version 1.1; 
        proxy_set_header Host $host; 
        proxy_set_header X-Real-IP $remote_addr; 
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for; 
        proxy_set_header X-Forwarded-Proto https; 
        proxy_set_header Connection ""; 
        proxy_connect_timeout 5s; 
        proxy_read_timeout 15s; 
        proxy_send_timeout 15s; 
    }
    
    #———— ★ 无头链路路由 → Go 后端（nurl-key 远程管理工具）————————————
    location ^~ /headless/ {
        limit_req zone=api burst=5 nodelay; 
        limit_req_status 429; 
        add_header Cache-Control "no-store" always; 
        proxy_pass http://127.0.0.1:8080; 
        proxy_http_version 1.1; 
        proxy_set_header Host $host; 
        proxy_set_header X-Real-IP $remote_addr; 
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for; 
        proxy_set_header X-Forwarded-Proto https; 
        proxy_set_header Connection ""; 
        proxy_connect_timeout 5s; 
        proxy_read_timeout 15s; 
        proxy_send_timeout 15s; 
    }
    
    # ————★ 一次性数据加载（惰性触发）————————————————————————————————
    access_by_lua_block {
        package.path = "{部署目录}/backend/lua/?.lua;;;" .. package.path
        local init = require "shorturl.init"
        init.init()
    }
    
    # ————★ 短链跳转（最低优先级，匹配 1-5 位字母数字）————————————————————
    location ~* "^/([0-9a-z-]{1,5})$" {
        limit_req zone=redirect_burst burst=10 nodelay; 
        limit_except GET {
            deny all; 
        }
        content_by_lua_block {
            package.path = "{部署目录}/backend/lua/?.lua;;;" .. package.path
            local redirect = require "shorturl.redirect"
            redirect.go()
        }
    }
    # ════════════════════════[END]═════════════════════════════
```
