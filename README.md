# Short_NURL

> 个人短链服务 · OpenResty + PHP-FPM · JSON 冷存储 · `lua_shared_dict` 热存储 · 零数据库 / 零 Redis
>
> Personal short URL service · OpenResty + PHP-FPM · JSON cold storage · `lua_shared_dict` hot storage · No database, No Redis

![License](https://img.shields.io/badge/license-Apache--2.0-blue)
![Runtime](https://img.shields.io/badge/runtime-OpenResty%20%2B%20PHP--FPM-green)
![Storage](https://img.shields.io/badge/storage-JSON%20%2B%20shared--dict-orange)
![Docs](https://img.shields.io/badge/docs-Chinese%20only-red)

---

文档地址（docs）：[https://uoca.top/Short_NURL](https://uoca.top/Short_NURL)

演示地址（demo）：[https://r.uoca.top](https://r.uoca.top)

---

## English / 英语

## 1. Project Introduction

**Short_NURL** is a short URL service designed for personal use cases. It aims to achieve high performance, low resource consumption, a clear structure, and simple deployment while maintaining a "near-native" technology stack.

It uses **OpenResty** to handle short URL redirections and **PHP-FPM** to handle management APIs, authentication, creation, deletion, and data persistence. The project does not depend on any database, Redis, message queue, or external service. The core data is maintained solely by local JSON files and Nginx shared memory.

> **Documentation Note** 
> 
> This README provides introductions in both Chinese and English.
> 
> However, the complete documentation, deployment guides, API references, and CLI documentation for the project are currently **only available in Chinese**.
> 
> If you are willing to help write an English version of the documentation, I would greatly appreciate it. Please directly submit your completed work (e.g., via Pull Request).

---

## 2. Core Features

* **Read-Write Separation**: Redirection requests are directly read from memory by OpenResty, while management operations are uniformly handled by PHP-FPM.
* **Zero External Dependencies**: No MySQL, SQLite, Redis, object storage, or message queues required.
* **Hot / Cold Dual Storage**: `lua_shared_dict` serves as hot storage, and JSON files serve as the authoritative cold storage.
* **High-Speed Redirection Path**: `GET /{code}` directly returns a 302 redirect upon a memory cache hit, involving zero disk I/O.
* **Secure Write Mechanism**: JSON writes support file locking, temporary files, atomic `rename()`, backups, verification, and rollbacks.
* **Permanent / Temporary Links**: Supports permanent short links as well as temporary short links with a TTL.
* **Custom Short Codes**: Supports 1–4 character custom short codes, automatically converted to lowercase, featuring reserved words and conflict detection.
* **Dual-Mode Keys**: Supports persistent keys, one-time keys.
* **Standard API + Headless API**: Can be called by browser frontends or utilized in scripts, backends, and CLI scenarios.
* **CLI Tools**: Provides local key management, cold storage cleanup, remote short link management, and more.
* **Docker / 1Panel Friendly**: Provides native Docker and 1Panel deployment instructions.

---

## 3. Storage Model

Short_NURL uses two types of data files:

| File            | Description                                                   |
| --------------- | ------------------------------------------------------------- |
| `perm.json`     | Permanent short URL data                                      |
| `temp.json`     | Temporary short URL data                                      |
| `perm.json.bak` | Snapshot of the last successful write to permanent short URLs |
| `temp.json.bak` | Snapshot of the last successful write to temporary short URLs |
| `keys.json`     | Hashed data for API Keys                       |

Cold storage JSON adopts a unified envelope format:

```json
{
  "v": 1,
  "at": "2026-05-23T12:00:00+08:00",
  "d": {
    "abc1": {
      "id": "abc1",
      "url": "https://example.com",
      "lurl": "https://s.yourdomain.com/abc1"
    }
  }
}
```

Temporary short links will include an extra `t` field representing the ISO 8601 expiration time.

---

## 4. Write Safety

All operations writing to JSON follow a unified rule set:

1. Read and acquire a file lock.
2. Complete business validation and conflict checks.
3. Write to a temporary file first.
4. Use `rename()` to atomically replace the target file.
5. Re-read and validate the JSON after writing.
6. Attempt to roll back from the `.bak` file if validation fails.

Therefore, cold storage remains the authoritative data source of the system at all times; hot storage is merely a memory projection used to accelerate redirections.

---

## 5. API Overview

### 1. Standard API

| Method | Path          | Description                   |
| ------ | ------------- | ----------------------------- |
| `POST` | `/api/create` | Create a short URL            |
| `POST` | `/api/delete` | Delete a short URL            |
| `GET`  | `/api/list`   | Query all short URLs          |
| `GET`  | `/api/stat`   | Query statistical information |

Standard APIs are designed for browser frontends and support CORS. The keys for `create` / `delete` are placed in the request body JSON, keys for `list` / `stat` are placed in the `X-Token` header.

### 2. Headless API

Headless APIs are designed for backends, scripts, automated tasks, and CLIs. They provide the same core capabilities as the Standard API while additionally supporting non-browser scenarios such as single-record queries.

### 3. CLI Tools

The project provides two CLI tools:

| Tool       | Position                | Purpose                                                                                 |
| ---------- | ----------------------- | --------------------------------------------------------------------------------------- |
| `nurl`     | Local Management Tool   | Generate / view / revoke Keys, clean up expired data, and check data file health status |
| `nurl-key` | Remote Short URL Client | View, create, and delete short URLs via Headless APIs                                   |

### 4. Deployment Methods

The project documentation includes two types of deployment instructions:

* Native Docker deployment
* 1Panel dashboard deployment

When deploying, you need to run both concurrently:

1. **OpenResty**: Responsible for short URL redirection, hot storage, internal interfaces, and scheduled cleanup.
2. **PHP-FPM**: Responsible for APIs, authentication, cold storage read/write, and CLI management capabilities.

Please refer to the Chinese documentation for detailed deployment steps.

> **Important: The complete documentation is currently only available in Chinese.**

---

## 6. Applicable Scenarios

Short_NURL is suitable for:

* Personal short URL services
* Privately deployed short URL tools
* Small-scale team internal link distribution
* Short link management for blogs, documentation sites, and navigation pages
* Lightweight deployment scenarios where you do not want to introduce a database or Redis

It is not recommended to use it directly as a large-scale, multi-tenant public SaaS.

---

### 7. License

The project is open-sourced under the **Apache License 2.0**.

```
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

```
Short_NURL
Copyright 2026 Uecook / 圣堂之魂
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.

Short_NURL by Uecook / 圣堂之魂
GitHub:https://github.com/UeCook/Short_NURL

NOTICE: In accordance with Section 4(d) of the Apache License 2.0, 
any distribution of this software or derivative works MUST publicly 
retain this attribution and version information in at least one of 
the following locations:
  1. A "NOTICE" text file distributed with the software
  2. Source code or accompanying documentation
  3. User interface (e.g., About page, Startup screen, Legal/Attributions section)

You may add your own attribution notices to this file, but you may not 
modify or remove the above notices in any way that alters their meaning.
```

Copyright 2026 Uecook / 圣堂之魂

---

## Acknowledgments

This page frontend is built with Basecoat UI.

Basecoat UI repository：[https://github.com/hunvreus/basecoat](https://github.com/hunvreus/basecoat)

```
MIT License

Copyright (c) 2025 Ronan Berder

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```


---

## 中文 / Chinese

## 一、项目简介

**Short_NURL** 是一个面向个人使用场景的短链服务，目标是做到：高性能、低占用、结构清晰、部署简单，并尽量保持“类原生”的技术栈。

使用 **OpenResty** 处理短链跳转，使用 **PHP-FPM** 处理管理 API、认证、创建、删除和数据写入。项目不依赖数据库、Redis、消息队列或任何外部服务，核心数据仅由本地 JSON 文件和 Nginx 共享内存维护。

> **文档说明**  
> 本 README 提供中英文介绍。  
> 但项目的完整说明文档、部署文档、API 文档和 CLI 文档目前**仅提供中文版本**。

---

## 二、核心特性

- **读写分离**：跳转请求由 OpenResty 直接读取内存，管理操作由 PHP-FPM 统一处理。
- **零外部服务依赖**：无需 MySQL、SQLite、Redis、对象存储或消息队列。
- **热 / 冷双存储**：`lua_shared_dict` 作为热存储，JSON 文件作为权威冷存储。
- **高速跳转路径**：`GET /{code}` 命中内存后直接返回 302，无磁盘 I/O。
- **安全写入机制**：JSON 写入支持文件锁、临时文件、原子 rename、备份、验证和回滚。
- **永久链 / 临时链**：支持永久短链和带 TTL 的临时短链。
- **自定义短码**：支持 1～4 位自定义短码，自动转小写，并包含保留词和冲突检测。
- **双模式 Key**：支持常驻 Key、一次性 Key。
- **标准 API + 无头 API**：既可供浏览器前端调用，也可用于脚本、后端或 CLI 场景。
- **CLI 工具**：提供本地密钥管理、冷存储清理、远程短链管理等能力。
- **Docker / 1Panel 友好**：提供原生 Docker 与 1Panel 部署说明。

---

## 三、存储模型

Short_NURL 使用两类数据文件：

| 文件 | 说明 |
|------|------|
| `perm.json` | 永久短链数据 |
| `temp.json` | 临时短链数据 |
| `perm.json.bak` | 永久短链上一次成功写入快照 |
| `temp.json.bak` | 临时短链上一次成功写入快照 |
| `keys.json` | API Key |

冷存储 JSON 采用统一信封格式：

```json
{
  "v": 1,
  "at": "2026-05-23T12:00:00+08:00",
  "d": {
    "abc1": {
      "id": "abc1",
      "url": "https://example.com",
      "lurl": "https://s.yourdomain.com/abc1"
    }
  }
}
```

临时短链会额外包含 `t` 字段表示 ISO 8601 过期时间。

---

## 四、写入安全

所有写入 JSON 的操作都遵循统一规则：

1. 读取并加锁。
2. 完成业务校验和冲突检查。
3. 先写入临时文件。
4. 使用 `rename()` 原子替换目标文件。
5. 写入后重新读取并验证 JSON。
6. 验证失败时尝试从 `.bak` 回滚。

因此，冷存储始终是系统的权威数据源；热存储只是用于加速跳转的内存投影。

---

## 五、API 概览

### 1.标准 API

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/api/create` | 创建短链 |
| `POST` | `/api/delete` | 删除短链 |
| `GET` | `/api/list` | 查询全部短链 |
| `GET` | `/api/stat` | 查询统计信息 |

标准 API 面向浏览器前端，支持 CORS。`create` / `delete` 的 Key 放在请求体 JSON 中，`list` / `stat` 的 Key 放在 `X-Token` 请求头中。

### 2.无头 API

无头 API 面向后端、脚本、自动化任务和 CLI，提供与标准 API 相同的核心能力，并额外支持单条查询等非浏览器场景。

### 3.CLI 工具

项目提供两个 CLI 工具：

| 工具 | 定位 | 用途 |
|------|------|------|
| `nurl` | 本地管理工具 | 生成 / 查看 / 撤销 Key，清理过期数据，检查数据文件健康状态 |
| `nurl-key` | 远程短链客户端 | 通过无头 API 查看、创建、删除短链 |

### 4.部署方式

项目文档包含两类部署说明：

- 原生 Docker 部署
- 1Panel 面板部署

部署时需要同时运行：

1. OpenResty：负责短链跳转、热存储、内部接口和定时清理。
2. PHP-FPM：负责 API、认证、冷存储读写和 CLI 管理能力。

详细部署步骤请查看中文文档。

> **重要：完整说明文档目前仅提供中文版本。**

---

## 六、适用场景

Short_NURL 适合：

- 个人短链服务
- 私有部署短链工具
- 小规模团队内部链接分发
- 博客、文档站、导航页的短链接管理
- 不希望引入数据库或 Redis 的轻量部署场景

不建议将其直接作为大型多租户公网 SaaS 使用。

---

### 七、License

项目使用 **Apache License 2.0** 开源。

```
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

```
Short_NURL
Copyright 2026 Uecook / 圣堂之魂
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.

Short_NURL by Uecook / 圣堂之魂
GitHub:https://github.com/UeCook/Short_NURL

NOTICE: In accordance with Section 4(d) of the Apache License 2.0, 
any distribution of this software or derivative works MUST publicly 
retain this attribution and version information in at least one of 
the following locations:
  1. A "NOTICE" text file distributed with the software
  2. Source code or accompanying documentation
  3. User interface (e.g., About page, Startup screen, Legal/Attributions section)

You may add your own attribution notices to this file, but you may not 
modify or remove the above notices in any way that alters their meaning.
```

Copyright 2026 Uecook / 圣堂之魂

---

## 鸣谢

页面前端使用了 Basecoat UI 构建。

Basecoat UI 项目地址：[https://github.com/hunvreus/basecoat](https://github.com/hunvreus/basecoat)

```
MIT License

Copyright (c) 2025 Ronan Berder

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

