# 宝塔面板部署教程

本文说明如何用宝塔面板的 Docker 功能部署 ADTY AI。

> 正式环境按 [ADTY 部署文档](../adty/deploy.md) 使用镜像 `ghcr.io/adtyai1995/adtyai-api` 的提交摘要标签。仓库地址是 https://github.com/adtyai1995/adtyai-api 。

***

## 前置要求

| 项目    | 要求                                 |
| ----- | ---------------------------------- |
| 宝塔面板  | ≥ 9.2.0 版本                         |
| 推荐系统  | CentOS 7+、Ubuntu 18.04+、Debian 10+ |
| 服务器配置 | 至少 1 核 2G 内存                       |

***

## 步骤一：安装宝塔面板

1. 前往 [宝塔面板官网](https://www.bt.cn/new/download.html) 下载适合您系统的安装脚本
2. 运行安装脚本安装宝塔面板
3. 安装完成后，使用提供的地址、用户名和密码登录宝塔面板

***

## 步骤二：安装 Docker

1. 登录宝塔面板后，在左侧菜单栏找到并点击 **Docker**
2. 首次进入会提示安装 Docker 服务，点击 **立即安装**
3. 按照提示完成 Docker 服务的安装

***

## 步骤三：安装 ADTY AI

> ⚠️ **ADTY AI 启动强依赖 PostgreSQL 18.x**：`SQL_DSN` 为 PostgreSQL 18.x 必填连接串，程序在连库与迁移前就会检查 `SQL_DSN`（及已配置的 `LOG_SQL_DSN`），未配置或非 PostgreSQL 会在启动时直接退出，不支持 SQLite 单机回退。下方方法二仅用于本地试用，正式环境请按 [ADTY 部署文档](../adty/deploy.md) 使用 `deploy/adty/` 编排，并配置 PostgreSQL 18.x、Redis、`SESSION_COOKIE_SECURE` 与 `SESSION_COOKIE_TRUSTED_URL`。

### 方法一：不要用应用商店里的旧项目

宝塔应用商店里的 New-API 不是本产品，也不要把它的镜像当作 ADTY AI。本产品的容器镜像是 `ghcr.io/adtyai1995/adtyai-api`，生产标签必须是提交摘要，例如 `ghcr.io/adtyai1995/adtyai-api:sha-1a2b3c4`。本地试用用下面的方法二，从本仓库构建。

本地试用按方法二使用仓库根目录的 `docker-compose.yml` 构建启动（内置 PostgreSQL 18 与 Redis）。生产按 [ADTY 部署文档](../adty/deploy.md) 填写 `ghcr.io/adtyai1995/adtyai-api` 的提交摘要标签。无论试用还是生产，都必须提供指向 PostgreSQL 18.x 的 `SQL_DSN`，面板里还需设置 `SESSION_SECRET`（必填；多机部署时各机必须一致）和 `CRYPTO_SECRET`（加密敏感配置与使用 Redis 时必填）。容器名可以自定。不要在应用商店搜索或安装 New-API。

### 方法二：使用仓库根目录的 Docker Compose（仅本地试用）

仓库根目录已提供包含 **PostgreSQL 18 + Redis** 的本地试用编排文件 [`docker-compose.yml`](../../docker-compose.yml)（请勿移除 `postgres` 服务或清空 `SQL_DSN`，否则 ADTY AI 启动时会直接退出）。

1. 在宝塔面板中克隆或上传本仓库到服务器目录（如 `/www/wwwroot/adtyai-api`），确认目录下包含仓库根目录的 `docker-compose.yml` 与 `Dockerfile`。
2. 设置必填环境变量 `POSTGRES_PASSWORD` 与 `REDIS_PASSWORD`（可在终端 `export`，或在同目录写入 `.env` 文件；建议同时配置 `SESSION_SECRET`），然后启动：

```bash
cd /www/wwwroot/adtyai-api
export POSTGRES_PASSWORD="请替换为随机数据库密码"
export REDIS_PASSWORD="请替换为随机Redis密码"
docker compose up -d --build
```

启动后该编排会自动拉起 `postgres:18`、`redis:7.4.4-alpine` 与本地构建的 `adtyai-api:local`，并自动注入 `SQL_DSN` 与 `REDIS_CONN_STRING`，在 `127.0.0.1:3000` 提供本地访问。

> 注意：仓库根目录 `docker-compose.yml` 仅供本地试用，不可直接用于生产。正式环境请按 [ADTY 部署文档](../adty/deploy.md) 使用 `deploy/adty/` 编排，镜像使用提交摘要标签，并同时配置 `SESSION_COOKIE_SECURE=true`、`SESSION_COOKIE_TRUSTED_URL`（正式 https origin，不带路径）与 `ADTY_FLUSH_TOKEN`。

***

## 配置说明

### 必要环境变量

| 变量名                          | 说明                                                                 | 是否必填               |
| ---------------------------- | ------------------------------------------------------------------ | ------------------ |
| `SESSION_SECRET`             | 会话密钥，多机部署必须一致                                                      | **必填**             |
| `CRYPTO_SECRET`              | 加密密钥（Cloudflare Token、Turnstile secret 等敏感值）；不设时等于 `SESSION_SECRET` | 生产必填               |
| `SQL_DSN`                    | PostgreSQL 18.x 数据库连接串（`postgresql://…`）；未配置或非 PostgreSQL 会在启动时直接退出，不支持 SQLite 单机回退 | **必填**             |
| `REDIS_CONN_STRING`          | Redis 连接串，缓存、批量更新和限流都依赖它                                           | 生产必填（试用可空）         |
| `SESSION_COOKIE_SECURE`      | 生产设 `true`，与下一项必须同时设                                               | 生产必填               |
| `SESSION_COOKIE_TRUSTED_URL` | 正式 https origin，多个用英文逗号分隔，不带路径或通配符                                  | 生产必填（与上一项成对）       |

### 生成随机密钥

```bash
# 生成 SESSION_SECRET
openssl rand -hex 16

# 或使用 Linux 命令
head -c 16 /dev/urandom | xxd -p
```

***

## 常见问题

### Q1：无法访问 3000 端口？

1. 检查服务器防火墙是否开放 3000 端口
2. 在宝塔面板 **安全** 中放行 3000 端口
3. 检查云服务器安全组是否开放端口

### Q2：登录后提示会话失效？

确保设置了 `SESSION_SECRET` 环境变量，且值不为空。

### Q3：数据如何持久化？

使用 Docker 卷映射数据目录：

```yaml
volumes:
  - ./data:/data
```

### Q4：如何更新版本？

```bash
# 用当前源码重新构建后重启。不要拉取上游浮动标签。
docker compose up -d --build
```

***

## 相关链接

- [ADTY 部署文档](../adty/deploy.md)
- [环境变量示例](../../deploy/adty/.env.example)
- [功能说明](../adty/features.md)
- [GitHub 仓库](https://github.com/adtyai1995/adtyai-api)

***

## 截图示例

![宝塔面板 Docker 安装](https://github.com/user-attachments/assets/7a6fc03e-c457-45e4-b8f9-184508fc26b0)

> ⚠️ 注意：密钥为环境变量 `SESSION_SECRET`，请务必设置！

