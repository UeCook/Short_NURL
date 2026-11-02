# Short_NURL

> 个人短链服务 | OpenResty + Go | JSON 冷存储 | `lua_shared_dict` 热存储 | 无数据库、无 Redis
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

### 项目简介与版本演进

**Short_NURL** 面向个人和小规模内部部署。OpenResty/Lua 处理短链跳转，Go 服务处理 API、认证、持久化和管理操作，无需 MySQL、SQLite、Redis、消息队列或对象存储。

JSON 文件是权威冷存储，`lua_shared_dict` 是加速跳转的热存储。正常跳转命中热存储后，不需要调用 Go 服务或读取磁盘。

V2 使用 PHP 架构，相比 V1 主要完成了前端解耦与后端整合。V3 延续这一结构，将管理后端和命令行工具迁移至 Go。此前因 PHP 的 [CVE-2026-109618](https://github.com/php/php-src/security/advisories/GHSA-w8hw-w9fx-j79w) 与 [CVE-2026-109619](https://github.com/php/php-src/security/advisories/GHSA-6wmf-4rv5-p3rp) 相关安全风险而关闭了旧版 PHP 服务，这也是本次迁移的直接原因。

V3 的管理服务和 CLI 无需 PHP 运行环境；完整的网页访问和短链跳转仍需部署 OpenResty/Nginx + Lua。

### 核心特性

- **前后端分离**：静态 WebUI 通过 API 管理短链，Go 承担认证和业务处理。
- **内存跳转**：OpenResty/Lua 从共享字典读取目标地址，JSON 文件负责持久化。
- **永久链与临时链**：支持永久短链和带 TTL 的临时短链。
- **自定义短码**：普通 Key 支持 1–4 位自定义短码；服务 Key 经无头 API 支持最长 5 位。
- **三类 API Key**：常驻 Key、一次性 Key 和服务 Key，按通道与操作限制权限。
- **内部通信认证**：Go 与 Lua 使用独立的 `LPA-Key`，缺失或校验失败时拒绝内部请求。
- **文件写入保护**：文件锁、临时文件、原子替换、备份和写后校验。
- **永久 URL 去重**：永久链接使用自动短码时，可复用已有目标 URL 的短链。
- **热存储同步状态**：创建和删除响应包含 `synced`、`warning`，支持严格模式与冷存储兜底。
- **URL 校验**：限制 URL 长度，拒绝内网、回环等不安全目标和无法安全解析的地址。
- **双通道 API**：WebUI API 服务浏览器面板，Headless API 服务脚本与其他后端。
- **多架构程序**：提供 Windows/Linux 运行包，也可自行编译。

### 获取与部署

#### 1. 选择运行包或自行编译

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

#### 2. 修改自己的配置

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

#### 3. 初始化并自检

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

#### 4. 启动 Go 服务并配置 OpenResty

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

#### 5. 验证服务

另开终端，将示例域名替换成自己的站点地址：

```sh
curl -sS https://short.example.com/api/ping
```

Windows 可使用 `curl.exe`。预期响应为：

```json
{"ok":true}
```

`/api/ping` 验证 Go API 存活；热存储连接情况需要查看经过认证的 `/api/stat` 中的 `hot_available`，并实际确认创建响应的 `synced` 和短链跳转。

### 存储模型与同步策略

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

### Key 类型

| 类型 | 生命周期 | 使用范围 | 说明 |
| --- | --- | --- | --- |
| 常驻 Key | 默认 7 天 | WebUI、Headless | 创建短链及管理操作 |
| 一次性 Key | 使用前不过期 | 两种通道的创建接口 | 业务成功后消费，不支持列表、查询、删除等管理操作 |
| 服务 Key | 永不过期 | 仅 Headless | 用于自动化和服务集成，可选择 4/5 位自动短码 |

一次性 Key 在认证时先预约，创建成功后消费；普通业务失败会释放预约。超过 60 秒未完成的预约会使 Key 失效。Key 池不会自动补充，可用 `./bin/nurl -full` 手动补充。

### API 概览

#### WebUI API

面向浏览器前端，支持 CORS。业务请求使用 `X-Token`；POST 请求也可在 JSON 中传入 `key`，请求头优先。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` / `HEAD` | `/api/ping` | 无需认证的 Go API 存活检查 |
| `POST` | `/api/create` | 创建短链 |
| `POST` | `/api/delete` | 删除短链 |
| `GET` | `/api/list` | 查询短链列表 |
| `GET` | `/api/stat` | 查询冷存储计数和热存储诊断信息 |

#### Headless API

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

### CLI 工具

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

### 测试与适用场景

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
