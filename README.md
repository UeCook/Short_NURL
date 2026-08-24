# Short_NURL

> 个人短链服务 | OpenResty + PHP-FPM | JSON 冷存储 | `lua_shared_dict` 热存储 | 无数据库、无 Redis
>
> Personal short URL service | OpenResty + PHP-FPM | JSON cold storage | `lua_shared_dict` hot storage | No database, no Redis

![Version](https://img.shields.io/badge/version-v1.11.0-4c8bf5)
![License](https://img.shields.io/badge/license-Apache--2.0-blue)
![Runtime](https://img.shields.io/badge/runtime-OpenResty%20%2B%20PHP--FPM-green)
![Storage](https://img.shields.io/badge/storage-JSON%20%2B%20shared--dict-orange)
![Docs](https://img.shields.io/badge/docs-Chinese%20only-red)

- Documentation / 文档：[https://blog.uoca.top/Short_NURL](https://blog.uoca.top/Short_NURL)
- Demo / 演示：[https://r.uoca.top](https://r.uoca.top)

---

## 中文

### 项目简介

**Short_NURL** 是一个面向个人和小规模内部场景的短链服务。它使用 OpenResty/Lua 处理高频短链跳转，使用 PHP-FPM 处理 API、认证和持久化，不依赖 MySQL、SQLite、Redis、消息队列或对象存储。

JSON 文件是系统的权威冷存储，`lua_shared_dict` 是用于加速跳转的热存储。正常跳转命中热存储后不需要访问 PHP 或磁盘。

完整部署、API 和 CLI 文档目前仅提供中文版本。

### 核心特性

- **读写分离**：OpenResty/Lua 负责跳转，PHP-FPM 负责管理操作。
- **零数据库依赖**：无需 MySQL、SQLite、Redis、消息队列或对象存储。
- **冷热双存储**：JSON 是权威数据源，共享内存是运行时投影。
- **永久链与临时链**：支持永久短链和带 TTL 的临时短链。
- **自定义短码**：普通 Key 支持 1-4 位短码；服务 Key 通过无头 API 支持最长 5 位短码。
- **三类 API Key**：支持常驻 Key、一次性 Key和服务 Key。
- **内部通信认证**：PHP 与 Lua 的内部接口使用独立 `LPA-Key`，缺失时默认拒绝访问。
- **安全写入**：支持文件锁、临时文件、原子替换、备份和写后校验。
- **永久 URL 去重**：自动生成永久短链时可复用已有目标 URL。
- **热存储同步状态**：创建和删除响应会报告 `synced`，同步失败时冷存储数据仍会保留。
- **URL 安全校验**：限制 URL 长度，并拒绝内网、保留地址和无法安全解析的目标。
- **标准 API 与无头 API**：同时支持浏览器面板、脚本、后端服务和 CLI。
- **CLI 工具**：提供本地密钥管理和远程短链管理工具。
- **多种部署方式**：文档包含原生 Docker、1Panel 和宝塔部署说明。


### 存储模型

| 文件 | 用途 |
| --- | --- |
| `backend/data/perm.json` | 永久短链冷存储 |
| `backend/data/temp.json` | 临时短链冷存储 |
| `backend/data/keys.json` | API Key 哈希、前缀和状态 |
| `backend/data/internal_token` | PHP 与 Lua 内部通信使用的 LPA-Key |
| `*.bak` | 最近一次成功写入的备份，由安全写入流程生成 |
| `*.lock` / `*.tmp` | 文件锁和原子写入过程中的运行时文件 |

短链 JSON 使用统一信封结构：

```json
{
  "v": 1,
  "at": "2026-08-24T12:00:00+08:00",
  "d": {
    "abc1": {
      "id": "abc1",
      "url": "https://example.com",
      "lurl": "https://s.example.com/abc1"
    }
  }
}
```

临时短链会额外包含 `t` 字段，用于保存 ISO 8601 格式的过期时间。

### Key 类型

| 类型 | 生命周期 | 使用范围 | 说明 |
| --- | --- | --- | --- |
| 常驻 Key | 默认 7 天 | 标准 API、无头 API | 可重复使用，可执行创建和管理操作 |
| 一次性 Key | 使用前不过期 | 标准 API、无头 API | 认证成功时立即消费，不会自动补充 |
| 服务 Key | 永不过期 | 仅无头 API | 适合服务端和自动化任务，可使用服务配额及 5 位短码 |

一次性 Key 在认证成功后、业务参数校验前就会被消费。即使后续请求因参数错误、冲突或权限不足而失败，该 Key 也不会恢复。使用 `php nurl -full` 手动补充一次性 Key 池。

### API 概览

#### 标准 API

标准 API 面向浏览器前端，支持 CORS。业务接口可通过 `X-Token` 认证；POST 接口也支持在 JSON 请求体中提供 `key`，请求头优先。

| 方法 | 路径 | 认证 | 用途 |
| --- | --- | --- | --- |
| `GET` | `/api/ping` | 无 | PHP-FPM 和路由健康检查 |
| `POST` | `/api/create` | `X-Token` 或 `body.key` | 创建短链 |
| `POST` | `/api/delete` | `X-Token` 或 `body.key` | 删除短链 |
| `GET` | `/api/list` | `X-Token` | 查询短链列表 |
| `GET` | `/api/stat` | `X-Token` | 查询冷存储计数和热存储诊断信息 |

#### 无头 API

无头 API 面向脚本、后端服务和 CLI，只接受 `X-Headless-Token`。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `POST` | `/headless/api/create` | 创建短链；服务 Key 可选择 4/5 位自动短码 |
| `POST` | `/headless/api/delete` | 删除短链 |
| `GET` | `/headless/api/list` | 查询短链列表 |
| `GET` | `/headless/api/stat` | 查询配额和存储状态 |
| `GET` | `/headless/api/get/{code}` | 查询单条短链 |

创建成功响应包含：

```json
{
  "short_url": "https://s.example.com/abc1",
  "exp": null,
  "dedup": false,
  "synced": true,
  "warning": null,
  "key_consumed": false
}
```

完整请求参数、响应字段和错误码请查看[项目文档](https://blog.uoca.top/Short_NURL)。

### CLI 工具

| 工具 | 定位 | 主要用途 |
| --- | --- | --- |
| `nurl` | 本地管理工具 | 生成、查看和撤销 Key，维护一次性 Key 池，轮换 LPA-Key，清理过期数据 |
| `nurl-key` | 远程无头客户端 | 创建、查询和删除短链，查看列表和统计信息 |

常用初始化命令：

```bash
php nurl -new
```

该命令会生成常驻 Key、补充一次性 Key 池，并在缺失时创建内部 LPA-Key。

服务 Key 可通过 `nurl-key` 选择自动短码长度：

```bash
php nurl-key -key "su_xxx" -url "https://example.com" -len 5
```

### 部署要求

运行 Short_NURL 需要：

- 支持 Lua 的 OpenResty/Nginx。
- PHP-FPM 和 PHP CLI。
- PHP cURL 扩展。
- 可供 PHP-FPM 写入、OpenResty 读取的数据目录。
- 与当前版本匹配的 `api/config.php` 和 `backend/nginx/nginx.md`。

部署时至少需要：

1. 修改 `api/config.php` 中的 `domain`、存储路径和内部服务地址。
2. 按实际目录修改 Nginx 的 Lua 模块路径、PHP-FPM 地址和数据文件路径。
3. 确保 PHP 与 Lua 使用同一个 `internal_token_path`。
4. 执行 `php nurl -new` 初始化 Key 和 LPA-Key。
5. 使用 `GET /api/ping` 检查 PHP 路由，再测试创建、同步和跳转。

不要直接复制其他版本的 Nginx 配置。v1.11.0 需要 10 MB 的 `su_url` 共享字典、1-5 位跳转路由和 fail-close 的内部令牌配置。

### 默认容量

| 类型 | 永久链 | 临时链 |
| --- | ---: | ---: |
| 普通 Key | 10000 | 500 |
| 服务 Key | 18000 | 1500 |

四项限制总和不得超过 30000。调整容量时还应同步评估 `lua_shared_dict su_url` 的大小。

### 适用场景

适合：

- 个人短链服务。
- 私有部署的短链工具。
- 小规模团队内部链接分发。
- 博客、文档站和导航页的短链管理。
- 不希望引入数据库或 Redis 的轻量部署场景。

不建议未经额外改造直接作为大型、多租户公网 SaaS 使用。

---

## English

### Introduction

The following translation was done by Gemini-3.7-flash and is for reference only.

**Short_NURL** is a self-hosted short URL service for personal and small internal deployments. OpenResty/Lua handles the high-frequency redirect path, while PHP-FPM handles APIs, authentication, and persistence. It does not require MySQL, SQLite, Redis, message queues, object storage, or other external data services.

JSON files are the authoritative cold storage. `lua_shared_dict` is a hot in-memory projection used to accelerate redirects. A normal redirect served from hot storage does not invoke PHP or read from disk.

Complete deployment, API, and CLI documentation is currently available in Chinese only.

### Highlights

- **Read/write separation**: OpenResty/Lua serves redirects; PHP-FPM handles management operations.
- **No database or Redis**: all authoritative data is stored in local JSON files.
- **Hot and cold storage**: shared memory accelerates reads while JSON remains authoritative.
- **Permanent and temporary links**: temporary links use a configurable TTL.
- **Custom codes**: regular Keys support 1-4 characters; service Keys can use up to 5 characters through the headless API.
- **Three Key types**: resident, one-time, and service Keys.
- **Authenticated internal API**: PHP-to-Lua synchronization uses a separate fail-closed `LPA-Key`.
- **Safer writes**: file locks, temporary files, atomic replacement, backups, and post-write validation.
- **Permanent URL deduplication**: existing permanent links can be reused automatically.
- **Synchronization reporting**: create and delete responses report whether hot storage was updated.
- **URL validation and SSRF protection**: private, reserved, or unsafe destinations are rejected.
- **Standard and headless APIs**: supports browser panels, scripts, backend services, and CLI clients.
- **CLI tooling**: local Key administration and remote short-link management are included.
- **Deployment guides**: native Docker, 1Panel, and Baota guides are available in the Chinese documentation.

### Key Types

| Type | Lifetime | Scope | Notes |
| --- | --- | --- | --- |
| Resident Key | 7 days by default | Standard and headless APIs | Reusable; supports creation and management operations |
| One-time Key | No pre-use expiry | Standard and headless APIs | Consumed immediately after successful authentication; never refilled automatically |
| Service Key | No expiry | Headless API only | Uses service quotas and can request 4- or 5-character generated codes |

A one-time Key is consumed during authentication, before business validation. It is not restored when a later validation, conflict, permission, or server error occurs.

### API Overview

#### Standard API

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/ping` | Unauthenticated PHP and routing health check |
| `POST` | `/api/create` | Create a short URL |
| `POST` | `/api/delete` | Delete a short URL |
| `GET` | `/api/list` | List short URLs |
| `GET` | `/api/stat` | Read authoritative cold counts and hot-storage diagnostics |

Standard API requests use `X-Token`. POST requests may also provide `key` in the JSON body, with the request header taking precedence.

#### Headless API

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/headless/api/create` | Create a short URL |
| `POST` | `/headless/api/delete` | Delete a short URL |
| `GET` | `/headless/api/list` | List short URLs |
| `GET` | `/headless/api/stat` | Query counters and storage state |
| `GET` | `/headless/api/get/{code}` | Query one short URL |

Headless requests accept only the `X-Headless-Token` header. Service Keys can use `len: 4` or `len: 5` when requesting an automatically generated code.

### CLI Tools

| Tool | Role | Purpose |
| --- | --- | --- |
| `nurl` | Local administration | Generate, inspect, and revoke Keys; refill the one-time pool; rotate the LPA-Key; sweep expired data |
| `nurl-key` | Remote client | Create, query, list, and delete links through the headless API |

Initialize a deployment with:

```bash
php nurl -new
```

### Runtime Requirements

- OpenResty/Nginx with Lua support.
- PHP-FPM and PHP CLI.
- PHP cURL extension.
- A data directory writable by PHP-FPM and readable by OpenResty.
- Version-matched `api/config.php` and `backend/nginx/nginx.md` configuration.

Use the Chinese [project documentation](https://blog.uoca.top/Short_NURL) for complete deployment instructions, API contracts, error codes, and CLI usage.

### Recommended Use

Short_NURL is suitable for personal services, private deployments, small internal teams, blogs, documentation sites, and other lightweight environments where running a database or Redis is unnecessary.

It is not intended to be exposed as a large, multi-tenant public SaaS without additional architecture and security controls.

---

## License

Short_NURL is licensed under the [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0).

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

## Acknowledgments / 鸣谢

The web interface is built with [Basecoat UI](https://github.com/hunvreus/basecoat), licensed under the MIT License.

页面前端使用 [Basecoat UI](https://github.com/hunvreus/basecoat) 构建，其源码使用 MIT License。
