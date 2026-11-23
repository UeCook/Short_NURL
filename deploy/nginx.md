## 选择运行模式

- **Go 托管模式**：使用本文的配置，保留 API、内部通信和现有管理功能。
- **纯 Lua＋JSON 手工模式**：使用`manual.conf.example`，按手工运营说明部署。此模式不需要 Go、WebUI、API Key 或内部令牌，Lua 只读 JSON，通过完整快照发布文件变更。

同一组共享字典只运行一种模式。切换到手工模式前停止 Go 服务，移除管理 API 与内部服务配置；不要同时手改和通过 Go 写同一份数据。

## Nginx 主配置

Go 后端已默认开启独立的 API 防刷，下面的 Nginx 限流作为额外保护。Go 默认只信任回环代理提供的 `X-Real-IP`；示例已在 API 路由中覆盖此请求头。代理分处容器/主机时，请设置 `abuse.trusted_proxies` 为实际代理地址段。

使用 Cloudflare 时，请先在 Nginx 中按 CF 的可信地址段还原访客 IP，再将 `$remote_addr` 传给 Go，否则所有请求可能被计入 CF 节点 IP。配置方法见 [Cloudflare 真实 IP 说明](https://developers.cloudflare.com/support/troubleshooting/restoring-visitor-ips/restoring-original-visitor-ips/)。

```nginx lineNumbers title="nginx.conf" {21,27}
    # ═══════════════════════════════════════════════════
    # 声明共享字典和限流区域
    # 放置在主 Nginx 配置中
    # ═══════════════════════════════════════════════════
    # ———— 共享字典 ——————————————————————————————————————
    lua_shared_dict su_url       10m;
    lua_shared_dict su_exp       2m;
    lua_shared_dict su_meta      1m;
    # Lua 跳转防扫描状态；不能与 su_url/su_meta 共用，避免挤出链接或同步标记
    lua_shared_dict su_blocklist 4m;
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
    set $su_mode "managed";
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
        proxy_ignore_client_abort off;
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
        proxy_ignore_client_abort off;
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
        limit_req_status 429;
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

## 跳转防扫描（两种运行模式共用）

除 Nginx 的每 IP 每秒 2 次、突发 10 次限流外，`shorturl.redirect_guard` 使用 `su_blocklist` 在所有 worker 间共享状态。配置位于 `backend/lua/shorturl/config.lua` 的 `redirect_guard`，修改后 reload Nginx：

| 字段 | 默认值 | 行为 |
| --- | --- | --- |
| `enabled` | `true` | 是否启用 Lua 防扫描 |
| `global_requests_per_second` | `100` | 整个实例每个整秒窗口最多放行 100 次跳转查询，包含命中与未命中、GET 与 HEAD |
| `miss_window_seconds` | `60` | 从该 IP 首次未命中开始计时的窗口；后续请求不刷新窗口 |
| `max_misses` | `20` | 窗口内第 20 次请求无效或已过期短码时封禁；不要求短码互不相同 |
| `ban_seconds` | `900` | 封禁 15 分钟，必须大于等于未命中窗口；重试不延长封禁 |

全局预算在查询短码之前扣除，更换 IP 或使用有效短码不能绕过。整秒边界允许相邻两秒各自使用额度；这不是平滑的每秒漏桶。被封禁的来源直接拒绝，不再消耗全局额度。成功跳转不清除已有未命中计数。Lua 防护拒绝返回 `429` 与 `Retry-After`，数据尚未就绪或防护状态无法保存时返回 `503` 与 `Retry-After`；Nginx 自身的每 IP 限流仅保证返回 `429`。共享字典满时拒绝继续放行，不通过 LRU 驱逐有效封禁。禁用 Lua 防护仍保留 Nginx 的每 IP 限流。

**全局额度也限制正常跳转**，分布式扫描期间正常用户可能收到 `429`。按实际流量调整额度与共享字典大小。状态只在当前 Nginx 实例内共享；reload 保留共享内存，整机/进程完全重启后重置，多实例不共享。此机制提高批量枚举成本，4/5 位公开短链仍不具备访问授权能力。

来源只使用 Nginx 的 `$remote_addr`。反向代理或 CDN 场景需用 `set_real_ip_from` 仅信任实际代理网段，并正确还原访客 IP；不要直接信任任意客户端的 `X-Real-IP`、`X-Forwarded-For` 或其他自定义请求头。IPv6 按完整地址计数，全局预算覆盖更换 IPv6 地址的探测。

跳转响应设置 `Cache-Control: no-store`，让新增的 URL/到期时间变更和删除能在后续请求中生效。已被浏览器或 CDN 缓存的历史永久跳转无法由服务端主动撤回。
