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

> ⚠️ **方法一和方法二只用于本地试用或单机试跑**：不设 `SQL_DSN` 时数据库退回 SQLite，也不会有 Redis，属于单机形态，不能作为生产部署。正式环境必须使用 PostgreSQL + Redis，并配置 `SESSION_COOKIE_SECURE`、`SESSION_COOKIE_TRUSTED_URL`（`deploy/adty/docker-compose.yml` 把 PostgreSQL 和 Redis 都列为必需依赖）。生产按 [ADTY 部署文档](../adty/deploy.md) 的 `deploy/adty/` 编排做，不要用本页的两段示例。

### 方法一：不要用应用商店里的旧项目

宝塔应用商店里的 New-API 不是本产品，也不要把它的镜像当作 ADTY AI。本产品的容器镜像是 `ghcr.io/adtyai1995/adtyai-api`，生产标签必须是提交摘要，例如 `ghcr.io/adtyai1995/adtyai-api:sha-1a2b3c4`。本地试用用下面的方法二，从本仓库构建。

本地试用按方法二从本仓库构建。生产按 [ADTY 部署文档](../adty/deploy.md) 填写 `ghcr.io/adtyai1995/adtyai-api` 的提交摘要标签。面板里需要的环境变量是 `SESSION_SECRET`（必填；多机部署时各机必须一致）和 `CRYPTO_SECRET`（使用 Redis 时必填）。容器名可以自定。不要在应用商店搜索或安装 New-API。

### 方法二：使用 Docker Compose（仅本地试用）

1. 在宝塔面板中创建网站目录，如 `/www/wwwroot/new-api`
2. 创建 `docker-compose.yml` 文件：

```yaml
services:
  new-api:
    image: adtyai-api:local
    build: .
    container_name: new-api
    restart: always
    ports:
      - "127.0.0.1:3000:3000"
    volumes:
      - ./data:/data
    environment:
      - SESSION_SECRET=your_session_secret_here  # 请修改为随机字符串
      - TZ=UTC
```

这段示例没有 `SQL_DSN` 和 `REDIS_CONN_STRING`，起的是 SQLite 单机形态，正式环境不要用。生产编排在 `deploy/adty`，镜像由本仓库构建，数据库必须 PostgreSQL、缓存必须 Redis，还要同时配 `SESSION_COOKIE_SECURE=true` 和 `SESSION_COOKIE_TRUSTED_URL`（正式 https origin，不带路径）。`TZ` 按实际网络设置，不要把某个地区时区写成默认值。

1. 在终端中进入目录并启动：

```bash
cd /www/wwwroot/new-api
docker-compose up -d
```

***

## 配置说明

### 必要环境变量

| 变量名                          | 说明                                                                 | 是否必填               |
| ---------------------------- | ------------------------------------------------------------------ | ------------------ |
| `SESSION_SECRET`             | 会话密钥，多机部署必须一致                                                      | **必填**             |
| `CRYPTO_SECRET`              | 加密密钥（Cloudflare Token、Turnstile secret 等敏感值）；不设时等于 `SESSION_SECRET` | 生产必填               |
| `SQL_DSN`                    | 数据库连接串，生产必须是 `postgresql://…`；不设时退回 SQLite 单机形态，仅供试用                  | 生产必填（试用可空）         |
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

