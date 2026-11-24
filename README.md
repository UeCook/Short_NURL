# Short_NURL

> 个人短链服务 | OpenResty + 可选 Go 管理 | JSON 冷存储 | `lua_shared_dict` 热存储 | 无数据库、无 Redis
>
> Self-hosted URL shortener | OpenResty + Go | JSON cold storage | `lua_shared_dict` hot storage | No database or Redis

![Version](https://img.shields.io/badge/version-v3.0.3-4c8bf5)
![License](https://img.shields.io/badge/license-Apache--2.0-blue)
![Runtime](https://img.shields.io/badge/runtime-OpenResty%20%2B%20Go-green)
![Storage](https://img.shields.io/badge/storage-JSON%20%2B%20shared--dict-orange)
![Docs](https://img.shields.io/badge/build%20guide-Chinese-red)


- 在线文档 / Online documentation：[https://nurl.uoca.top/](https://blog.uoca.top/Short_NURL)
- 演示 / Demo：[https://r.uoca.top](https://r.uoca.top)

部署 V3 时请以当前仓库的配置和说明为准；在线文档中的历史版本示例需要按版本核对。以下输出示例只保留识别特征，路径和凭证均使用占位符。

---

## 中文

## 项目简介

**Short_NURL** 是面向个人或小规模部署设计的短链服务。它基于 OpenResty / Lua 处理短链跳转，Go 服务处理 API、认证、持久化和管理操作，JSON 仅作持久化冷存储与启动恢复源，短链热数据由 Lua 直接写入并常驻 OpenResty 的 `lua_shared_dict`；跳转热路径只读共享内存，不读 JSON、不回源 Go，无需 MySQL、SQLite、Redis、消息队列或对象存储。

V2 使用 PHP 架构，相比 V1 主要完成了前端解耦与后端整合。V3 延续这一结构，将管理后端和命令行工具迁移至 Go。此前因 PHP 的 [CVE-2026-109618](https://github.com/php/php-src/security/advisories/GHSA-w8hw-w9fx-j79w) 与 [CVE-2026-109619](https://github.com/php/php-src/security/advisories/GHSA-6wmf-4rv5-p3rp) 相关安全风险而关闭了 PHP 服务。

| 版本 | 源码存档 |
| --- | --- |
| V1 | [Short_NURL_PHP_1](https://github.com/UeCook/Short_NURL_PHP_1) |
| V2 | [Short_NURL_PHP_2](https://github.com/UeCook/Short_NURL_PHP_2) |

Short_NURL 的 Lua 后端运行在 OpenResty + LuaJIT 上。

| 组件 | 项目中的用途 |
| --- | --- |
| `lua-nginx-module`（`ngx_http_lua_module`） | 提供 `ngx.shared` 共享字典、`ngx.timer` 定时器、HTTP 请求处理和 `ngx.redirect` 跳转。Linux 手工模式还通过 `ngx.run_worker_thread` 把文件读取、校验和快照发布交给线程池。 |
| `lua-cjson`（`cjson.safe`） | 读写短链 JSON 数据，解析内部 API 请求并生成响应。 |
| LuaJIT `ffi` + OpenSSL `libcrypto` | 手工模式通过 OpenSSL EVP 计算 **SHA3-256** 和 **SM3**，用于检测 JSON 快照是否变化。内置算法需要支持相应摘要的 OpenSSL 1.1.1 或更新版本。 |
| LuaJIT `bit` | 托管模式用 `bor`、`bxor` 比较内部 API 令牌。 |

## 核心特性

- **前后端分离**：静态 WebUI 通过 API 管理短链，Go 承担认证和业务处理。
- **内存级跳转**：OpenResty / Lua 从共享字典读取目标地址，JSON 文件仅负责持久化。
- **无 Go 独立运营**：手工模式只读 JSON，完整快照切换，错误文件或候选内存装载失败时自动保留上一版。
- **跳转防扫描**：所有 worker 共享未命中封禁与查询前的全局额度，覆盖多来源扫描与 GET/HEAD；配置及可用性取舍见 [Nginx 说明](deploy/nginx/nginx.md)。
- **支持永久短链与临时短链**：支持永久短链和带 TTL 的临时短链。
- **支持自定义短码**：普通 Key 支持 1–4 位自定义短码；服务 Key 经无头 API 支持最长 5 位。
- **人类友好短码**：排除 `0/1/i/l/o`，禁止相邻重复字符，以及 `m/n/r`、`u/v/w` 各组内部的相邻组合（如 `rn`、`vv`、`ww`、`vw`）。规则统一用于 所有自动短码；自定义短码与已有短链保持兼容，永久 URL 去重仍可返回已有短码。
- **三类 API Key**：常驻 Key、一次性 Key 和服务 Key，按通道与操作限制权限。
- **内置 API 防刷**：默认开启每 IP/全局限流、并发上限、错误 Key 逐级提示和递增封禁，无需 fail2ban、Redis 或 Cloudflare。
- **内部通信认证**：Go 与 Lua 使用独立的 `LPA-Key`，缺失或校验失败时拒绝内部请求。
- **文件写入保护**：文件锁、临时文件、原子替换、备份和写后校验。
- **URL 去重**：永久链接使用自动短码时，可复用已有目标 URL 的短链。
- **热存储同步状态**：创建和删除响应包含 `synced`、`warning`，支持严格模式与冷存储兜底。
- **URL 校验**：限制 URL 长度，拒绝内网、回环等不安全目标和无法安全解析的地址。
- **双通道 API**：WebUI API 服务浏览器面板，Headless API 服务脚本与其他后端。
- **多架构程序**：提供 Windows / Linux 运行包，支持自行使用源码编译。

## 服务可靠性

测试版本 **Short_NURL v3.1.4**。测试在 Windows WSL 2（Ubuntu 24.04 LTS，i7-12700H）上运行，服务端限额为 **2 核、2 GiB**，禁用交换空间。

按 **498 KB/s（3.984 Mbps）** 出站带宽和 99.5% 计划字节预算，HTTP/1.1 明文和 TLS 1.3 分别完成 **611.61** 和 **240.77** 次正确跳转/秒。明文模型每请求约 **810** 个出站字节，TLS 1.3 标定约 **2,057** 个出站字节。两种协议均逐请求新建 TCP，TLS 1.3 每次执行完整握手。

跳转计时从客户端开始建连，到收到短链服务的完整 `302` 为止，成功判定同时校验 `Location`。测试只请求短链服务。百分比表示计划出站字节预算，P99 为第 99 百分位延迟。手工模式默认发布上限为 **30,000 条**永久与临时短链合计。6 万条跳转场景在隔离副本中提高了条目上限，并关闭了默认跳转限流。

### 6 万条短链带宽步进阶梯表（TLS 1.3）

数据为 3 万条永久短链和 3 万条临时短链。标准档为 0.5–10 Mbps，每步 0.5 Mbps，约 80% 和约 98% 各发压 6 秒，取五轮的平均值。下表为 v3.1.4 数据。

| 带宽（Mbps） | 约 80% 平均（毫秒） | 约 80% P99（毫秒） | 约 98% 平均（毫秒） | 约 98% P99（毫秒） |
| --- | --- | --- | --- | --- |
| 0.5 | 4.211 | 7.060 | 4.003 | 6.730 |
| 1.0 | 3.600 | 5.950 | 3.495 | 5.810 |
| 1.5 | 3.143 | 5.130 | 2.789 | 4.940 |
| 2.0 | 2.998 | 5.697 | 2.811 | 5.066 |
| 2.5 | 2.459 | 4.240 | 2.277 | 5.440 |
| 3.0 | 2.552 | 7.830 | 2.244 | 4.870 |
| 3.5 | 2.024 | 3.150 | 2.649 | 9.040 |
| **基准（498 KB/s）** | 2.257 | 4.532 | 2.161 | 4.216 |
| 4.0 | 1.981 | 3.730 | 2.007 | 4.860 |
| 4.5 | 4.231 | 53.630 | 2.072 | 5.120 |
| 5.0 | 1.971 | 5.100 | 1.887 | 3.350 |
| 5.5 | 1.926 | 4.790 | 1.777 | 2.850 |
| 6.0 | 1.758 | 3.240 | 2.095 | 4.570 |
| 6.5 | 2.180 | 4.070 | 2.285 | 6.090 |
| 7.0 | 2.293 | 4.560 | 2.157 | 4.810 |
| 7.5 | 2.379 | 6.510 | 1.963 | 4.400 |
| 8.0 | 1.828 | 3.470 | 1.823 | 3.770 |
| 8.5 | 1.811 | 3.090 | 1.884 | 4.420 |
| 9.0 | 1.922 | 4.130 | 2.217 | 7.290 |
| 9.5 | 1.802 | 3.670 | 2.417 | 6.130 |
| 10.0 | 1.844 | 3.840 | 2.231 | 5.160 |


### 99.5% 字节预算表（TLS 1.3）

3 万条永久短链 + 3 万条临时短链，每轮发压 10 秒，发包粒度 20 ms。1–5 Mbps 标准档原测各三轮。表中为各档逐轮均值。

| Mbps | HTTP 请求/秒 | TLS 请求/秒 | HTTP 实际 KB/s | TLS 实际 KB/s | TLS P99（ms） |
| --- | --- | --- | --- | --- | --- |
| 1 | 153.48 | 60.40 | 124.42 | 124.11 | 8.65 |
| 2 | 306.97 | 120.89 | 248.98 | 248.43 | 10.01 |
| 3 | 460.54 | 181.37 | 373.65 | 372.73 | 9.75 |
| **基准（498KB/s）** | 611.61 | 240.77 | 496.13 | 495.13 | 13.28 |
| 4 | 614.01 | 241.87 | 498.12 | 497.06 | 9.24 |
| 5 | 767.62 | 302.27 | 622.58 | 621.54 | 10.36 |


### 创建 API 带宽矩阵

以 HTTP/1.1 + TLS 1.3 调用 `POST /api/create` 创建永久短链。1–5 Mbps 每步 1 Mbps。每个带宽按 80%、95%、99.5% 计划字节预算各测三轮，每轮从空库发压 5 秒。实际下行占比取三轮客户端网卡实测均值。

窗口内新增以 5 秒内收到完整 HTTP `200`、确认新增与热同步、最终 JSON 记录相符为准。整轮完成时间从发压开始计至最后一条窗口内新增，表中取三轮均值。单条耗时从开始建连计至完整响应，合并三轮新增样本统计，字节加权使用各条 HTTP 响应字节数。

| 带宽（Mbps） | 计划发压 | 新增 / 发出 | 新增/秒 | 实际下行占比 | 整轮完成均值（ms） | 单条最快（ms） | 单条最慢（ms） | 单条平均（ms） | 字节加权（ms） | P99（ms） |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | 80% | 487 / 627 | 32.47 | 80.02% | 4,979.20 | 9.22 | 278.01 | 19.94 | 19.94 | 196.48 |
| 1 | 95% | 578 / 744 | 38.53 | 94.80% | 4,973.75 | 7.95 | 161.14 | 16.00 | 16.00 | 100.10 |
| 1 | 99.5% | 604 / 780 | 40.27 | 98.56% | 4,953.58 | 7.96 | 259.06 | 23.32 | 23.32 | 175.10 |
| 2 | 80% | 972 / 1,257 | 64.80 | 79.80% | 4,996.67 | 7.38 | 447.47 | 18.82 | 18.82 | 241.71 |
| 2 | 95% | 1,156 / 1,491 | 77.07 | 94.35% | 4,993.00 | 7.50 | 365.84 | 23.50 | 23.50 | 245.21 |
| 2 | 99.5% | 1,209 / 1,563 | 80.60 | 97.40% | 4,996.17 | 7.61 | 535.97 | 72.23 | 72.23 | 451.50 |
| 3 | 80% | 1,459 / 1,884 | 97.27 | 79.82% | 4,997.16 | 7.16 | 188.90 | 14.21 | 14.21 | 116.22 |
| 3 | 95% | 1,734 / 2,238 | 115.60 | 94.30% | 4,994.11 | 7.36 | 316.92 | 20.86 | 20.86 | 178.77 |
| 3 | 99.5% | 1,818 / 2,346 | 121.20 | 98.11% | 4,997.13 | 6.68 | 548.61 | 35.45 | 35.45 | 280.64 |
| **基准（498 KB/s）** | 80% | 1,936 / 2,505 | 129.07 | 79.70% | 4,996.37 | 7.62 | 539.02 | 40.65 | 40.65 | 376.90 |
| **基准（498 KB/s）** | 95% | 2,117 / 2,973 | 141.13 | 91.93% | 4,996.10 | 7.66 | 507.49 | 132.21 | 132.21 | 463.55 |
| **基准（498 KB/s）** | 99.5% | 2,165 / 3,114 | 144.33 | 95.08% | 4,995.10 | 7.93 | 555.54 | 148.42 | 148.42 | 522.44 |
| 4 | 80% | 1,943 / 2,514 | 129.53 | 79.67% | 4,994.06 | 7.33 | 283.41 | 22.98 | 22.98 | 203.57 |
| 4 | 95% | 2,128 / 2,985 | 141.87 | 91.97% | 4,995.00 | 7.85 | 439.56 | 108.30 | 108.30 | 415.05 |
| 4 | 99.5% | 2,160 / 3,126 | 144.00 | 94.82% | 4,996.81 | 8.43 | 597.34 | 159.55 | 159.55 | 552.12 |
| 5 | 80% | 2,113 / 3,144 | 140.87 | 76.31% | 4,994.58 | 7.78 | 702.18 | 222.31 | 222.31 | 659.32 |
| 5 | 95% | 2,117 / 3,732 | 141.13 | 90.31% | 4,996.02 | 48.52 | 1,027.72 | 541.71 | 541.71 | 1,018.49 |
| 5 | 99.5% | 2,187 / 3,909 | 145.80 | 94.72% | 4,993.72 | 43.18 | 1,036.47 | 534.01 | 534.01 | 1,025.00 |


### 4 万条存量创建（GO）

每轮先装载 3 万条永久短链和 1 万条未到期临时短链，在提高条目上限的隔离副本中测试。Go 创建场景压测创建 API。采用 HTTP/1.1 + TLS 1.3，以 498 KB/s 基准、95% 计划字节预算发压，每轮 10 秒。以种子 321799 重新生成 10,000 条唯一 URL，与原测试数据的重合数为 0。两组以同一批新 URL 交替运行，各五轮，每轮 1,983 条，每组计划提交 9,915 条。

请求与落库计数为五轮合计。后端完成计时从发压开始计至 Go 处理完毕，表中列出五轮的最短和最长耗时。锁等待、锁内工作和 Go 事务的分位数汇总五轮成功提交样本。取消 Go 并发准入上限后，存储锁仍执行 **2 秒** 等待上限，排队超时可返回 `429 api_busy`。

| 并发上限 | 计划提交 | 实际发出 | 未发出 | 10 秒内确认新建（条） | 最终写入新短链（条） | 已发出但未写入（丢弃） | 服务繁忙次数（429） | 网关超时次数（504） | 已写入但客户端未收到确认（条） | 后端处理完毕用时（秒） | 等待写入锁的中位数（秒） | 持锁处理耗时中位数（秒） | Go 创建耗时 P99（秒） | 永久短链 JSON 峰值大小（五轮均值，MiB） |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 上限 128 | 9,915 | 9,915 | 0 | 308 | 374 | 9,541 | 9,227 | 0 | 0 | 12.03–12.24 | 1.905 | 0.156 | 2.192 | 5.04 |
| 取消上限 | 9,915 | 9,915 | 0 | 304 | 376 | 9,539 | 8,532 | 0 | 0 | 12.25–12.43 | 1.997 | 0.157 | 2.196 | 5.04 |

Go 入口并发上限是一项冗余保护设计，与存储锁的 2 秒等待上限配合，限制写入高峰时同时排队和处理的请求数。

| 并发上限 | 实际发出 |  未发出| 最终写入短链 | 丢弃总数 | 服务繁忙拒绝（429） | 请求冲突（409） | 网站防火墙拦截（403） |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 上限 128 | 9,915 | 0 | 374 | 9,541 | 9,227 | 75 | 239 |
| 取消上限 | 9,915 | 0 | 376 | 9,539 | 8,532 | 768 | 239 |

## 获取与部署

### 1. 选择运行包或自行编译

下载与目标机器匹配的运行包并解压。需要从源码编译时，参照 [BUILD.md](BUILD.md)。源码包包含 Go、Lua、前端资源及说明；编译后的程序位于运行包的 `bin/` 中。

| 系统 | 运行包架构后缀 |
| --- | --- |
| Windows | `windows-386`、`windows-amd64`、`windows-arm64` |
| Linux | `linux-386`、`linux-amd64`、`linux-armv7`、`linux-arm64` |

`386` 是 x86 32 位，`amd64` 是 x86 64 位，`armv7` 是 ARM 32 位，`arm64` 是 ARM 64 位。运行包中的 Go 程序可独立执行；完整服务还需要可用的 OpenResty/Nginx + Lua 环境。

以下命令均在包含 `config/` 的项目或运行包根目录执行。Linux 解压后先赋予执行权限并确认版本：

```sh
chmod +x ./bin/nurl-server ./bin/nurl ./bin/nurl-key
./bin/nurl-server -version
```

应看到 `3.0.3_GitHub`。Windows 使用 `.\bin\nurl-server.exe -version`，其余命令也使用对应的 `.exe` 文件。

### 2. 修改自己的配置

编辑 `config/config.json`，保留配置文件中的其他字段，按部署环境调整：

| 字段 | 用途 |
| --- | --- |
| `site.domain` | 完整的 HTTP(S) 站点地址，例如 `https://short.example.com` |
| `site.panel_origin` | 面板使用独立域名时填写；留空使用 `site.domain` |
| `server.listen` | Go 服务监听地址，默认 `127.0.0.1:8080` |
| `storage.*` | JSON 数据文件和内部令牌路径 |
| `internal.host` | OpenResty 内部接口地址，默认 `http://127.0.0.1:18500` |
| `policy.cold_fallback` | 热同步失败时的处理策略，默认 `false` |

源码中的域名占位符需要替换后才能正常启动。配置在运行时读取，不会编译进二进制。可通过环境变量 `NURL_CONFIG` 指定配置文件，服务端也支持 `-config <配置文件路径>`。

### 3. 初始化并自检

Linux：

```sh
./bin/nurl -new
./bin/nurl-server -check
```

Windows PowerShell：

```powershell
.\bin\nurl.exe -new
.\bin\nurl-server.exe -check
```

首次初始化会生成常驻 Key、补充一次性 Key 池，并在缺失时创建内部 LPA-Key。只需辨认这些输出特征：

```text
新常驻密钥：<已隐藏>
新一次性密钥：
新内部通信令牌：<已隐藏>
自检通过。
```

### 4. 启动 Go 服务并配置 OpenResty

Linux：

```sh
./bin/nurl-server -config ./config/config.json
```

Windows PowerShell：

```powershell
.\bin\nurl-server.exe -config .\config\config.json
```

出现 `nurl-server 启动` 日志后，保持进程运行。

参照 [Nginx 配置片段](deploy/nginx/nginx.md)，配置共享字典、内部 Lua 接口、API 反向代理和短链跳转；将 `index.html`、`frontend/` 作为静态站点提供。Go 与 Lua 必须使用相同的数据文件和内部令牌。Go 进程需要写入数据目录；OpenResty 需要读取数据和令牌，首次冷启动创建空数据文件时还需目录写权限。

### 5. 验证服务

另开终端，将示例域名替换成自己的站点地址：

```sh
curl -sS https://short.example.com/api/ping
```

Windows 可使用 `curl.exe`。预期响应为：

```json
{"ok":true}
```

`/api/ping` 验证 Go API 存活；热存储连接情况需要查看经过认证的 `/api/stat` 中的 `hot_available`，并实际确认创建响应的 `synced` 和短链跳转。

## 存储模型与同步策略

| 文件 | 用途 |
| --- | --- |
| `backend/data/perm.json` | 永久短链冷存储 |
| `backend/data/temp.json` | 临时短链冷存储 |
| `backend/data/keys.json` | API Key 摘要、前缀、预约和消费状态 |
| `backend/data/internal_token` | Go 与 Lua 内部通信使用的 LPA-Key |
| `*.bak` | 写入前的数据备份 |
| `*.lock`、`.operations/`、临时文件 | 文件锁及操作期间的辅助文件 |

短链 JSON 使用统一信封结构，示例内容均为演示数据：

```json
{
  "v": 1,
  "at": "2026-01-01T00:00:00+08:00",
  "d": {
    "abc1": {
      "id": "abc1",
      "url": "https://example.com",
      "lurl": "https://short.example.com/abc1"
    }
  }
}
```

临时短链额外包含 `t`，保存 ISO 8601 格式的过期时间。

- `cold_fallback=false`：默认严格模式。热同步失败时返回 `503`，不提交本次冷存储变更。
- `cold_fallback=true`：允许热同步未确认时提交冷数据，并返回 `synced=false` 和 `warning`。创建的链接可能暂不可用，删除的链接可能仍可访问，等待后台对账。

## Key 类型

| 类型 | 生命周期 | 使用范围 | 说明 |
| --- | --- | --- | --- |
| 常驻 Key | 默认 7 天 | WebUI、Headless | 创建短链及管理操作 |
| 一次性 Key | 使用前不过期 | 两种通道的创建接口 | 业务成功后消费，不支持列表、查询、删除等管理操作 |
| 服务 Key | 永不过期 | 仅 Headless | 用于自动化和服务集成，可选择 4/5 位自动短码 |

一次性 Key 在认证时先预约，创建成功后消费；普通业务失败会释放预约。超过 60 秒未完成的预约会使 Key 失效。Key 池不会自动补充，可用 `./bin/nurl -full` 手动补充。

## API 概览

### WebUI API

面向浏览器前端，支持 CORS。业务请求使用 `X-Token`；POST 请求也可在 JSON 中传入 `key`，请求头优先。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` / `HEAD` | `/api/ping` | 无需认证的 Go API 存活检查 |
| `POST` | `/api/create` | 创建短链 |
| `POST` | `/api/delete` | 删除短链 |
| `GET` | `/api/list` | 查询短链列表 |
| `GET` | `/api/stat` | 查询冷存储计数和热存储诊断信息 |

### Headless API

面向脚本、其他后端和 CLI，仅接受 `X-Headless-Token` 认证。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `POST` | `/headless/api/create` | 创建短链；服务 Key 可指定 `len: 4` 或 `len: 5` |
| `POST` | `/headless/api/delete` | 删除短链 |
| `GET` | `/headless/api/list` | 查询短链列表 |
| `GET` | `/headless/api/stat` | 查询配额和存储状态 |
| `GET` | `/headless/api/get/{code}` | 查询单条短链 |

创建成功响应示例：

```json
{
  "short_url": "https://short.example.com/abc1",
  "exp": null,
  "dedup": false,
  "synced": true,
  "warning": null,
  "key_consumed": false
}
```

## CLI 工具

| 工具 | 定位 | 主要用途 |
| --- | --- | --- |
| `nurl-server` | Go HTTP 服务 | 提供管理 API，配置自检，冷数据校验 |
| `nurl` | 本地管理工具 | 生成、查看和撤销 Key，补充一次性池，轮换 LPA-Key，清理过期数据 |
| `nurl-key` | 远程 Headless 客户端 | 创建、查询、删除短链，查看列表与统计 |

Linux 常用本地命令（Windows 使用对应 `.exe`）：

```sh
./bin/nurl -look
./bin/nurl -full
./bin/nurl -sweep
./bin/nurl-server -verify-data
```

分别可看到 `密钥状态：`、`新一次性密钥：` 或 `一次性密钥池已满。`、清理结果，以及 `perm.json:` / `temp.json:` 等数据校验行。

需要服务 Key 时，在尚未设置服务 Key 的部署中执行：

```sh
./bin/nurl -svc -label:automation
```

输出包含 `新服务密钥：<已隐藏>`。将实际 Key 保存在自己可读的文件中；下面假定 `backend/data/client-key.txt` 已存入服务 Key，该文件属于运行数据，不随源码包发布：

```sh
./bin/nurl-key -key-file ./backend/data/client-key.txt -add -url https://example.com -ttl 0 -len 5
```

成功时出现 `短链创建成功：`、`短链：` 等字段。远程创建需要 `-add`；单独提供 `-url` 不会发起创建操作。可用各工具的 `-h` 查看完整参数。

### 默认容量

| 调用类型 | 永久链上限 | 临时链上限 |
| --- | ---: | ---: |
| 普通 Key（常驻、一次性） | 10000 | 500 |
| 服务 Key | 10000 | 500 |

对应 `quota.perm_limit`、`temp_limit`、`svc_perm_limit`、`svc_temp_limit`，四项配置的合计不得超过 30000。各类 Key 共用短链存储，请求按调用类型使用对应上限；这些配置不提供多租户数据隔离。调整容量时还需评估 Lua 共享字典大小。

## 测试与适用场景

源码目录中可执行：

```sh
go test ./...
go vet ./...
```

测试通过的行以 `ok` 开头，可能带 `(cached)`；`go vet` 成功时通常无输出。历史 PHP 对照测试需要另行配置 `PHP_BIN`、`PHP_SRC`，未配置时跳过。

适合个人、私有部署及小规模内部链接管理。编译步骤、架构选择和输出特征见 [BUILD.md](BUILD.md)。

---

## English

> The following translation was done by ChatGPT-6.1-Sol and is for reference only.

### Introduction

**Short_NURL** is a self-hosted URL shortener for personal and small internal deployments. OpenResty/Lua handles redirects, while Go handles management APIs, authentication, persistence, and CLI tools. It requires no database or Redis.

JSON files remain the authoritative cold storage. `lua_shared_dict` provides hot storage for redirects; a normal hot-cache hit does not call the Go service or read from disk.

V2 separated the frontend and consolidated the backend built on PHP. V3 continues that structure and migrates the management backend and CLI tools to Go. The earlier PHP service was taken offline over the risks associated with [CVE-2026-109618](https://github.com/php/php-src/security/advisories/GHSA-w8hw-w9fx-j79w) and [CVE-2026-109619](https://github.com/php/php-src/security/advisories/GHSA-6wmf-4rv5-p3rp), prompting this migration.

Prebuilt Go programs do not require PHP or a Go installation. A complete deployment still requires OpenResty/Nginx with Lua support and access to the same data files and internal token.

### Highlights

- Static WebUI, Go management APIs, and OpenResty/Lua redirects.
- Permanent and temporary links, custom codes, and automatic permanent-URL deduplication.
- Resident, one-time, and service API Keys with channel and operation restrictions.
- Authenticated Go-to-Lua synchronization using a separate `LPA-Key`.
- File locks, temporary writes, backups, atomic replacement, and readback verification.
- Strict hot-storage synchronization by default; optional cold-storage fallback reports `synced` and `warning`.
- URL validation rejects private, loopback, and other unsafe destinations.
- Windows x86 32/64-bit and ARM64 programs; Linux x86 32/64-bit, ARMv7, and ARM64 programs.

### Quick Start

Choose a runtime archive matching the target system, or build from source using [BUILD.md](BUILD.md). The build guide and [Nginx snippets](deploy/nginx/nginx.md) are currently written in Chinese.

From the extracted runtime directory, edit `config/config.json` for your own deployment. Set `site.domain` to a complete HTTP(S) origin and configure the Go listener, data paths, and `internal.host`. Runtime configuration is not embedded in the binaries.

On Linux, initialize and check the deployment:

```sh
chmod +x ./bin/nurl-server ./bin/nurl ./bin/nurl-key
./bin/nurl-server -version
./bin/nurl -new
./bin/nurl-server -check
```

Look for `3.0.3_GitHub` and the final check message `自检通过。`. Initialization prints new credentials; this README omits their values. On Windows, use the corresponding `.exe` programs, such as `.\bin\nurl.exe -new`.

Start the Go service and keep it running:

```sh
./bin/nurl-server -config ./config/config.json
```

Look for `nurl-server 启动` in the log. Configure OpenResty to serve `index.html` and `frontend/`, proxy the management APIs, provide the internal Lua API, and handle short-link redirects. Use the same JSON paths and internal token in Go and Lua. Go needs data-directory write access; OpenResty needs read access and permission to initialize missing JSON files during cold start.

From another terminal, replace the example domain with your own:

```sh
curl -sS https://short.example.com/api/ping
```

Expected body: `{"ok":true}`. This checks the Go API; verify hot storage separately through authenticated statistics and a create/redirect test.

### Keys and Synchronization

| Key type | Lifetime | Scope |
| --- | --- | --- |
| Resident | 7 days by default | WebUI and Headless creation and management |
| One-time | No expiry before use | Creation only, through either channel |
| Service | No expiry | Headless only; supports 4/5-character generated codes |

One-time Keys are reserved during authentication and consumed after successful creation. Ordinary business failures release the reservation; unfinished reservations become invalid after 60 seconds. The pool is refilled manually with `nurl -full`.

- `cold_fallback=false`: the default strict mode returns `503` on hot-sync failure without committing the cold-storage change.
- `cold_fallback=true`: cold changes may commit with `synced=false` and a warning. Redirect availability may lag until reconciliation.

The default permanent/temporary limits are 10000/500 for both regular and service settings. Keys share the same link store; quota settings do not provide tenant isolation.

### API Overview

| Channel | Authentication | Endpoints |
| --- | --- | --- |
| WebUI | `X-Token`; POST bodies may also provide `key` | `POST /api/create`, `POST /api/delete`, `GET /api/list`, `GET /api/stat` |
| Headless | `X-Headless-Token` only | `POST /headless/api/create`, `POST /headless/api/delete`, `GET /headless/api/list`, `GET /headless/api/stat`, `GET /headless/api/get/{code}` |

`GET /api/ping` and `HEAD /api/ping` require no authentication. Create/delete responses report hot synchronization status. Service Keys are accepted only by the Headless channel; one-time Keys cannot perform management operations.

### CLI Tools

| Program | Role |
| --- | --- |
| `nurl-server` | HTTP APIs, configuration checks, and cold-data verification |
| `nurl` | Local Key administration, pool refill, internal-token rotation, and expiry cleanup |
| `nurl-key` | Remote Headless client for link creation, queries, deletion, lists, and statistics |

After generating a service Key, store its value in a private file. Assuming `backend/data/client-key.txt` contains that Key:

```sh
./bin/nurl-key -key-file ./backend/data/client-key.txt -add -url https://example.com -ttl 0 -len 5
```

Look for `短链创建成功：` on success. Use `-h` for all supported options. For V3, follow the current repository configuration and guides when older online documentation differs.

---

## License

Short_NURL is licensed under the [Apache License 2.0](LICENSE.txt).

```text
Copyright 2026 Uecook / 圣堂之魂

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```

### NOTICE

```text
Short_NURL
Copyright 2026 Uecook / 圣堂之魂
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.

Short_NURL by Uecook / 圣堂之魂
GitHub: https://github.com/UeCook/Short_NURL

NOTICE: In accordance with Section 4(d) of the Apache License 2.0,
any distribution of this software or derivative works MUST publicly
retain this attribution and version information in at least one of
the following locations:
  1. A "NOTICE" text file distributed with the software
  2. Source code or accompanying documentation
  3. User interface (e.g. About page, Startup screen, Legal/Attributions section)

You may add your own attribution notices to this file, but you may not
modify or remove the above notices in any way that alters their meaning.
```
---

The web interface uses [Basecoat UI](https://github.com/hunvreus/basecoat), licensed under the [MIT License](frontend/LICENSE.txt).

页面前端使用 Basecoat UI 构建。

```
MIT License

Copyright (c) 2025 Ronan Berder

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

页面中使用的图标来自 Lucide 。

```
ISC License

Copyright (c) 2026 Lucide Icons and Contributors

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

---

The following Lucide icons are derived from the Feather project:

airplay, alert-circle, alert-octagon, alert-triangle, aperture, arrow-down-circle, arrow-down-left, arrow-down-right, arrow-down, arrow-left-circle, arrow-left, arrow-right-circle, arrow-right, arrow-up-circle, arrow-up-left, arrow-up-right, arrow-up, at-sign, calendar, cast, check, chevron-down, chevron-left, chevron-right, chevron-up, chevrons-down, chevrons-left, chevrons-right, chevrons-up, circle, clipboard, clock, code, columns, command, compass, corner-down-left, corner-down-right, corner-left-down, corner-left-up, corner-right-down, corner-right-up, corner-up-left, corner-up-right, crosshair, database, divide-circle, divide-square, dollar-sign, download, external-link, feather, frown, hash, headphones, help-circle, info, italic, key, layout, life-buoy, link-2, link, loader, lock, log-in, log-out, maximize, meh, minimize, minimize-2, minus-circle, minus-square, minus, monitor, moon, more-horizontal, more-vertical, move, music, navigation-2, navigation, octagon, pause-circle, percent, plus-circle, plus-square, plus, power, radio, rss, search, server, share, shopping-bag, sidebar, smartphone, smile, square, table-2, tablet, target, terminal, trash-2, trash, triangle, tv, type, upload, x-circle, x-octagon, x-square, x, zoom-in, zoom-out

The MIT License (MIT) (for the icons listed above)

Copyright (c) 2013-present Cole Bemis

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
