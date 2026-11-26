# 额度与余额查询

实时掌握 Token 消耗与剩余额度，能够避免在关键开发任务中途因额度耗尽而中断。使用 **ADTY AI** 时，你可以结合「网页控制台」和「CC Switch 内置用量查询」两种方式查看消费情况。

## 1. 在 ADTY AI 网页控制台查看（最权威）

ADTY AI 网页控制台提供完整的计费明细与实时余额：

- **调用日志与 Token 明细**：访问 [https://www.adtyai.com/logs](https://www.adtyai.com/logs)，查看每次请求的模型、输入/输出/缓存 Token 数量、耗时与精确扣费。
- **钱包余额与赠金明细**：访问 [https://www.adtyai.com/wallet](https://www.adtyai.com/wallet)，查看可用现金余额、赠金余额、充值记录与兑换码记录（详见 [余额充值、赠金与兑换码](/zh-cn/guides/wallet-and-vouchers)）。
- **团队共享钱包**：若使用的是团队 API Key，可在团队面板查看共享额度与成员使用明细（详见 [团队协作与共享钱包](/zh-cn/guides/teams)）。

## 2. 在 CC Switch 中配置用量与余额查询

CC Switch 支持为每个供应商卡片配置用量查询（通过兼容 OpenAI 账单接口或自定义 JavaScript 脚本），让你在切换档案时直观看到已用与剩余额度。

::: warning 务必请求 API 域名
以下所有账单与令牌用量接口都必须请求 **`https://api.adtyai.com`**，不可填写 `https://www.adtyai.com`（网页控制台域名会拦截中转账单路径并返回 HTTP `403`）。
:::

### 方式一：OpenAI 兼容账单接口（推荐，直接使用当前 API Key）

ADTY AI 原生兼容标准的 OpenAI 账单查询接口，直接携带当前供应商的 API Key（`Authorization: Bearer <YOUR_API_KEY>`）即可查询：

- **总额度 / 限额**：`GET https://api.adtyai.com/v1/dashboard/billing/subscription`
  - 返回 `hard_limit_usd`（按站点展示单位换算的总额度上限；不限额的 Key 会返回 `100000000`）以及 `access_until`（过期时间戳，`0` 表示永不过期）。
- **已用额度**：`GET https://api.adtyai.com/v1/dashboard/billing/usage`
  - 返回 `total_usage`（单位为**美分**，除以 `100` 即为对应金额）。

你可以先在终端用 `curl` 验证接口返回：

```bash
curl -s https://api.adtyai.com/v1/dashboard/billing/subscription \
  -H "Authorization: Bearer sk-your-api-key"

curl -s https://api.adtyai.com/v1/dashboard/billing/usage \
  -H "Authorization: Bearer sk-your-api-key"
```

#### CC Switch 自定义用量查询脚本示例

在 CC Switch 中编辑你的 **ADTY AI** 供应商卡片，打开 **用量查询 / 自定义脚本（Usage Query / Custom Script）**，参考以下脚本从当前档案上下文读取 `apiKey` 并计算余额：

```javascript
async function queryUsage({ apiKey }) {
  const apiBase = "https://api.adtyai.com";
  const headers = {
    Authorization: `Bearer ${apiKey}`,
    Accept: "application/json",
  };

  const [subRes, usageRes] = await Promise.all([
    fetch(`${apiBase}/v1/dashboard/billing/subscription`, { headers }),
    fetch(`${apiBase}/v1/dashboard/billing/usage`, { headers }),
  ]);

  if (!subRes.ok || !usageRes.ok) {
    throw new Error(`查询失败：${subRes.status} / ${usageRes.status}`);
  }

  const sub = await subRes.json();
  const usage = await usageRes.json();

  const total = Number(sub.hard_limit_usd ?? 0);
  const used = Number(usage.total_usage ?? 0) / 100;
  const isUnlimited = total >= 100000000;

  return {
    isValid: true,
    total: isUnlimited ? null : Number(total.toFixed(4)),
    used: Number(used.toFixed(4)),
    remaining: isUnlimited ? null : Number(Math.max(0, total - used).toFixed(4)),
    unit: "USD",
    extra: isUnlimited ? "当前 Key 为不限额" : undefined,
  };
}
```

::: tip 根据 CC Switch 版本微调返回字段
不同版本的 CC Switch 对自定义脚本的函数签名和返回字段名（如 `remaining`、`used`、`total`、`unit`、`extra`）可能略有差异，请对照 CC Switch 脚本编辑器内自带的默认模板映射字段名。
:::

### 方式二：ADTY AI 令牌用量接口（`/api/usage/token/`）

如果你希望获取更丰富的令牌元数据——例如是否开启不限额、模型白名单、过期时间，以及（在令牌开启权限后）关联的账户余额或团队共享余额——可以调用 ADTY AI 的只读令牌用量接口：

```bash
curl -s https://api.adtyai.com/api/usage/token/ \
  -H "Authorization: Bearer sk-your-api-key"
```

返回结果中 `data` 的典型结构如下：

```json
{
  "code": true,
  "message": "ok",
  "data": {
    "object": "token_usage",
    "name": "Claude Code 专用 Key",
    "total_granted": 5000000,
    "total_used": 1250000,
    "total_available": 3750000,
    "unlimited_quota": false,
    "model_limits_enabled": false,
    "expires_at": 0
  }
}
```

- **令牌级额度**：`total_granted`、`total_used`、`total_available` 以平台内部额度单位返回当前 API Key 的总额度、已用额度与剩余额度。
- **账户 / 团队余额扩展字段**：如果在 ADTY AI 控制台中为该令牌开启了「允许查询用户额度」，响应还会附带 `user_quota`（个人 Key）或 `team_quota`（团队 Key；团队 Key 仅展示团队共享钱包余额，绝不会泄露成员个人钱包）。

### 方式三：使用最小权限 PAT 进行账户级查询

如果你编写的自建监控脚本需要查询单个 API Key 以外的账户级信息，请在控制台安全设置中创建一个**最小只读权限的个人访问令牌（PAT）**，切勿直接将浏览器登录 Cookie 或账号密码写入第三方脚本。创建与轮换方法详见 [账号安全与访问令牌](/zh-cn/guides/security)。

## 安全使用规范

- **按客户端隔离 API Key**：尽量为 CC Switch 中的每个档案分配带额度上限的独立 API Key，避免在多个工具和脚本里反复填写无限额主 Key。
- **不要在可分享脚本中硬编码密钥**：始终通过 CC Switch 注入的 `apiKey` 参数读取密钥，不要把真实 `sk-...` 字符串或 PAT 直接写死在脚本正文里。
- **控制查询频率**：手动刷新或保持 5–15 分钟以上的定时刷新间隔即可，避免高频轮询触发速率限制。
- **导出与截图前脱敏**：导出 CC Switch 配置文件或截取排查画面前，务必打码隐藏 API Key 与 PAT。

## 常见排查

- **返回 HTTP `401 Unauthorized`**：检查 API Key 是否已被禁用/删除，以及请求头是否包含 `Bearer ` 前缀。
- **返回 HTTP `403 Forbidden`**：确认脚本请求的是 `https://api.adtyai.com`，而非控制台域名 `https://www.adtyai.com`。
- **总额度显示 `100000000`**：说明该 API Key 勾选了「无限额度」。如需在 CC Switch 中直观展示可用余量，可为该 Key 设定合理的额度上限，或直接前往 [https://www.adtyai.com/wallet](https://www.adtyai.com/wallet) 查看账户钱包余额。
