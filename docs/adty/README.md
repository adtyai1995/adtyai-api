# adtyai-api 工程文档

adtyai-api 的公开仓库是 [adtyai1995/adtyai-api](https://github.com/adtyai1995/adtyai-api)，容器镜像是 `ghcr.io/adtyai1995/adtyai-api`，唯一创作者为 `adtyai1995`。对外产品说明仅保留简体中文与英文（根目录 `README.md`、`README.zh_CN.md`、`README.en.md`）。工程上仍尽量把自研改动收敛在独立文件里，已有上游文件仅保留最小挂载点或按清单删除（见 [conventions.md](conventions.md) 约定 B）。

## 当前产品能力与改造摘要

下列说明已同步至最新执行基线；详细规则、后台入口、数据表与算例见 [features.md](features.md)。

- **精简与移除项**：已整条删除上游订阅、旧版兑换码与 TopUpLink、旧版邀请奖励、Playground、模型部署管理、独立营销中心、第三方 OAuth 登录（GitHub、Discord、LinuxDO、微信、OIDC、自定义 OAuth、Telegram 登录）、Stripe/Creem/Waffo/旧版单通道易支付、签到、优惠码、金额档位赠金、公开状态页、六种非正式语言包，以及模型广场官方价展示、后台官方价与历史价格快照回填入口、分站开站协议、分站全局默认供货折扣/下限与旧版月消费分档。历史删除记录见 [deleted-features.md](deleted-features.md) 与 [deleted-paths.txt](deleted-paths.txt)。
- **发布工作流与版本**：镜像仅由 `.github/workflows/adty-ghcr.yml` 在双架构测试全绿后构建并推送至 `ghcr.io/adtyai1995/adtyai-api`（PR 门禁为 `.github/workflows/ci.yml`）。前后端共用构建期写入的 `VERSION`（`main-<短 SHA>`、标签名、`<分支名>-<短 SHA>` 或 `pr-<编号>-<短 SHA>`）。
- **多通道支付与场景**：统一接入易支付多通道（后台「计费与支付 → 支付网关 → Epay」顶部直接内嵌通道管理，客户端按通道平铺显示且失败不自动切通道）与 BEpusdt USDT（TRC20、ERC20、BEP20、Polygon、X Layer）。Root 配置版本化支付场景，现行覆盖普通充值（`wallet_topup`）、站长保证金在线充值（`owner_margin_topup`）与主站团队充值（`team_topup`）；原站长自用在线充值（`owner_self_use_topup`）已退役返 410，仅保留历史只读兼容。订单冻结场景、通道、显示名称（`method_label`）及网关事实，支持有效期内可信晚到回调入账与主动补查；已过期订单永久禁止非人工自动入账，统一转入异常支付队列由 Root 人工核实处理（`[R-41] #1`）。
- **零透支账本与退款**：内部固定 `1 USD = 500,000` 整数额度，钱包按「本金 → 永久赠金」顺序扣费（到期试用赠金已于 R-44 退役，仅保留历史只读账务兼容）。付费请求严格预扣、拒绝透支，结算少扣记入短差额记录与日志，不暂停后续有余额时的付费请求。人工退款仅扣回未消费本金并收回赠金，不退现不扣负，按退本比例冲回主站或分站邀请佣金，实际打款线下完成。
- **分站经营、小时结算与老站长推荐开站（`[R-49]`）**：仅 Root 可通过前置就绪检查卡片与独立「开设分站」弹窗填写 `owner_username`/`owner_password` 创建全新独立纯管理端站长账号（不再从主站已有用户挑选 `user_id`，站长账号不具备自用消费/API 功能）并一键建站，支持 `Idempotency-Key` 幂等防重、自动/自定义子域名及可选关联老站长推荐关系；主站首页顶栏与页脚提供公开「分站合作」入口（`/partner`，分站域名下严格隐藏并返回 HTTP 404），意向表提交后直接展示官方招商 Telegram 并通知 Root，Root 在「开站申请」审核一键带入推荐关系并开站（同时支持直开手填及 7 天内一次性补绑）。老站长推荐新分站永久按被推荐分站「净平台供货成本（含个人与团队消费，仅排除主站免费补偿/体验额度）」的默认 **5%**（可全局配置或单站覆写，由主站出资、0 冻结期，任一方正式关站自动停止）每小时自动结算入推荐分站收益钱包，且单级不穿透、独立考核等级，推荐人仅见脱敏分站名（`Site #ID`）与收益。全平台四条资金线统一改为每小时整点自动结算（0 冻结期 + 当小时实时预估）：① 分站供货与差价聚合入账（实时 API 请求不再逐笔行锁 `adty_site_wallets`）→ ② 老站长推荐 5% → ③ 主站推广按实付本金实际消费每小时结（取代充值返佣与 7 天冻结，采用生效中比例，风控冻结期间入账为 `risk_held` 待管理员释放/作废）→ ④ 分站客户一级邀请按实付本金实际消费每小时结（0 观察期，沿用收益优先 + Root 授权保证金 90% 周期垫付，风控冻结同样支持站长释放/作废）；跨小时退款负额自动结转抵扣，正式关站瞬间立即结清当小时未结流水。站长固定不可移交，开站无需签署开站协议；供货折扣由分站等级自动考核，分站充值赠送仅支持 USDT，站长收益提现仅支持 USDT，发放客户额度统一通过签发 `site_customer` 兑换码。
- **团队与非团队隔离**：Root「团队管理」（`/admin-teams`）与站长「团队管理」（`/site-owner/teams`）为独立入口；主站手动创建团队固定归属主站（`site_id = 0`）并支持填写「团队供货折扣」。服务端下发 `adty_has_team`，非团队账号隐藏侧边栏「我的团队」入口及全部团队信息并拦截直达 `/my-team`。活跃团队负责人可重置独立员工密码并立即吊销旧会话；关团后保留共享余额与负责人只读回显。
- **分站员工与权限**：员工登录进入 `/site-staff`，四类默认角色均只读，客户停用（`customers.disable`）等安全操作默认关闭且须显式授权；原员工调额申请（`customers.balance_request`）已退役返 410，改权、停用或删除立即失效旧会话；禁止员工查看完整客户 Key、重置客户密码、代登录、直接退款、发起客户余额调整或直接操作资金。
- **工单管理、通知与 Telegram 机器人**：平台「工单管理」（`/admin-tickets`）、站长「工单管理」（`/site-owner/tickets`）、员工工作区与客户支持中心（`/support`）共用同一工单数据流，支持多图批量上传、额度预检、私有鉴权与崩溃安全审计删图。后台「系统设置 → 运营 → Telegram 机器人」以用户机器人/管理机器人双独立卡片管理 Token、启停开关、测试与 Webhook URL，并在同页承载平台管理机器人绑定卡片。
- **数据库与本地编排**：正式运行、部署、资金测试与验收仅使用 **PostgreSQL 18.x**（主业务库与独立日志库）和 **Redis**。根目录 `docker-compose.yml` 默认项目名 `adtyai-api`、服务默认中国标准时间 `Asia/Shanghai`；生产部署见 [deploy.md](deploy.md)。
- **控制台与双语体验**：正式语言仅简体中文（`/zh-cn`）与英文（`/`，`x-default`）；全站标题、表单标签、表头、页签与导航名称均支持选中复制；系统设置页按内容自然滚动且仅模型计价区采用视口分栏滚动。

## 与 AGENTS.md 的补充约定

根目录 `AGENTS.md` 保留仓库通用工程规范，下列四项为 ADTY 明确执行的口径：

1. **数据库**：正式运行、部署、资金测试与验收统一使用 PostgreSQL 18.x 与 Redis；上游 SQLite/MySQL 兼容代码与旧单测仅作资产保留，不作为验收依据。
2. **对外身份**：公开仓库固定为 https://github.com/adtyai1995/adtyai-api ，容器镜像固定为 `ghcr.io/adtyai1995/adtyai-api`，唯一创作者为 `adtyai1995`。`LICENSE`、`NOTICE`、`THIRD-PARTY-LICENSES.md`、源码版权头及 Go 模块路径 `github.com/QuantumNous/new-api` 仅按许可证和编译要求保留。
3. **`docs/adty/` 文档**：工程文档统一放在 `docs/adty/`，随功能变更同步更新。
4. **语言范围**：仅简体中文与英文，所有新增正式界面文案与固定模板均需齐备中英文。

## 文档索引

- `README.md`：本文件，概括产品改造现状与文档索引。
- [features.md](features.md)：功能规格、后台入口、数据表、接口与算例。
- [conventions.md](conventions.md)：工程约定 A–I（代码边界、资金安全、权限、双语、测试、提交身份与产品边界）。
- [upstream-touchpoints.md](upstream-touchpoints.md)：修改过的上游文件清单。
- [deleted-features.md](deleted-features.md)：删除功能清单与保留项说明。
- [deleted-paths.txt](deleted-paths.txt)：已删除文件/目录清单（供合并与守卫检查读取）。
- [upstream-merge.md](upstream-merge.md)：上游合并流程与脚本说明。
- [deploy.md](deploy.md)：生产部署、镜像标签、Nginx/Cloudflare、备份与回滚指南。
- [launch-checklist.md](launch-checklist.md)：上线前核查清单。
- [baseline.md](baseline.md)：上游基线记录。

`docs/` 目录通过 `.dockerignore` 排除在生产镜像之外。
