# 轻量 Web 项目复用与交接手册

本手册是设计/开发契约，不是凭据保险箱。后续 Agent 应先阅读本文件、README、实际源码和最新提交；不能凭项目名称推断功能。

## 1. 可复用优势与技术取舍

- Go 单进程服务 + SQLite + 静态前端：不新增 Node 运行进程、Redis、数据库容器或 UI 框架。
- Vue 3 适合已有 Vue 项目；原生前端如果认证、上传和隐私边界稳定，先复用样式，不为技术栈一致而重写。
- 数据统一持久化到 /data。账户/密钥、数据库、附件索引、缓存必须在升级后保留；不把数据或凭据打进镜像。
- 所有项目使用 appearance.js + design-system.css，三配色及关于入口共用契约，各自独立发布，不依赖另一个仓库的运行服务。
- 不引入外链字体、追踪、远程 JS、CDN UI 库；移动端优先保证可读、可点、可配置。

## 2. UI 契约 v1

主题：light / dark / green；system 是跟随系统模式。html[data-theme] 控制 CSS；localStorage 使用站点命名空间。电影继续兼容 trm_theme；其他站点使用 relay_theme / hub_theme。主题切换不得重新挂载页面、清空表单、重新上传、重新登录或读取私密内容。

变量：--theme-bg、bg-secondary、surface-muted、text、text-secondary、primary、primary-hover、primary-active、primary-soft、on-primary、secondary、border、border-strong、disabled-bg、disabled-text、success、warning、danger、overlay、shadow、radius、motion（以上均加 --theme- 前缀）。

圆角0；4/8间距；系统字体；正文14px、手机输入16px；交互热区≥44px；正文/辅助/分组/页面标题层级清晰。普通卡片无阴影，悬浮层轻微阴影。不得新增胶囊、大渐变、毛玻璃或发光卡片。

断点：375 / 768 / 1024；移动表单单列、导航抽屉、记录卡片或局部滚动表格。抽屉支持关闭、遮罩、Escape、焦点循环和返回；关闭时不可Tab到屏幕外。原图、弹窗与保存栏不得互相遮挡。

组件状态：default / hover / active / disabled / loading / focus-visible / error / empty。加载不能当空记录，错误不能伪装成成功。危险操作确认，保留用户输入方便重试。

Vue 项目 AppearanceControls.vue 仅提供主题/关于入口；所有业务仍使用原组件、状态、接口。appearance-ready 用于动态挂载后同步选择器；appearance-change 仅通知外观变化。公共脚本不发业务请求、不读取令牌、不写业务状态。

源码与静态关于页的 design-system.css 必须相同（Vue 版本一份随业务打包，一份供独立关于页使用）；脚本修改需同步三个项目并运行 tests/appearance.test.cjs。

## 3. 通用功能的统一边界

| 能力 | 电影 | 随手传 | 固件中心 |
|---|---|---|---|
| 数据隔离 | 管理员私有媒体配置 | 按账户隔离消息/文件 | 按项目与成员角色隔离 |
| Telegram | 随机选片、菜单、规则通知 | 双向消息与附件 | 版本发布通知 |
| Webhook | 自定义模板与自动规则 | 当前无通用Webhook设置 | 发布事件与投递记录 |
| 登录 | 账号密码 | TOTP，可记住7天 | 账号密码、可选TOTP |
| 定时/后台任务 | 同步与推荐调度 | 消息到期/传输/TG处理 | 通知重试、状态超时 |
| 关于与主题 | 三主题、许可、关于页 | 同左 | 同左 |

统一的是配置含义、脱敏、测试按钮、反馈与设计，不能强行统一接口或改变既有安全模型。随手传目前没有通用Webhook；不得在README谎称已有。若后续增加，用独立适配器与事件队列，先定义兼容的数据迁移和测试。

### Telegram 接入规范

- 默认关闭；每个应用使用独立 Bot Token 和接收实例，长轮询不需要暴露公网回调。
- 明确允许的私聊/归属用户，不能把管理员机器人变成所有用户共享收件箱。
- Token 加密保存在服务端；读取只返回 hasToken/configured，输入留空不修改；不在日志/HTML/关于页回显已保存凭据。
- 保存、检测连接、发送测试为不同动作；检测失败保留输入，显示可操作原因。
- 接收和发送语义按项目保留；隐私内容是否转发必须显式配置。Telegram 不是端到端加密通道。
- 幂等/去重、超时、取消、退避、有限重试和投递状态必须有测试；不能只测“按钮能点”。

### Webhook 接入规范

- HTTP(S)、超时、请求体/响应体上限、重定向和目的地址限制按应用真实边界保留；不为方便统一而移除SSRF保护。
- 私网接收端若为产品允许场景，使用清晰的显式配置，不允许任意绕过协议/凭据检查。
- 密钥不回显，错误信息脱敏；签名、重试、部分失败等按原有协议，不破坏现用接收端。
- 测试与正式事件标识清楚；双渠道分别记录，禁止因一条失败而盲目重复成功渠道。

## 4. 网站与容器安全基线

认证、会话撤销、CSRF、权限、输入/上传限制、Markdown/链接净化、路径边界不因改UI而调整。不要将“隐私显示”写成“加密传输”，也不要将SHA-256写成发布者身份验证。

默认不需要 privileged、host网络、Docker Socket。初始化进程可仅为专用 /data 准备UID/GID 10001权限，随后服务降权；不递归操作宿主机其他目录。拒绝符号链接、硬链接、特殊文件和嵌套挂载。指定Compose user时尊重该用户，目录可写性由部署者保证。NAS ACL/只读挂载不能由应用绕过。

依赖锁定、npm审计、Go测试/vet、许可证保留；升级基础镜像或依赖要单独验证，不把自动升级所有主版本混入UI提交。备份是敏感数据，需要加密保管；不能因为清理用户画像而删除上游贡献者署名。

## 5. README 统一写法

顺序：一句话用途→最简Compose→首次使用→功能与边界→升级/备份/恢复→常见故障→文档索引→许可。

Compose直接写参数，不要求.env。镜像名称和公开性必须实际验证；未发布不能写“可匿名拉取”。注释区分必须/建议/可省略；默认不增加无用安全参数，不给新用户一屏几十个变量。所有项目固定内部路径 /data，端口左侧可改，右侧说明不可随意改。

只记录NAS/本机/小型VPS等通用适用领域，不写真实设备、私人路径/IP、习惯、作者私人邮箱或截图凭据。不提交账户、TOTP、数据库、备份、文件名隐私、私钥、Compose真实密码、Docker配置或认证输出。

长说明拆docs：部署、API、通知、安全、升级。示例使用 example.com、占位账号与虚构数据；不要把测试凭据介绍成生产默认密码。关于页展示版本、原始版权、许可、源码/文档和数据边界，不展示内部路径或Token。

## 6. 发布凭据登记（只登记名字，不登记值）

| 凭据 | 名称/位置 | 权限/用途 |
|---|---|---|
| Docker Hub 用户名 | Actions Secret: DOCKERHUB_USERNAME | 发布账户 |
| Docker Hub PAT | Actions Secret: DOCKERHUB_TOKEN | 发布所需读写；不授予不需要的删除权限；设置有效期 |
| GitHub 自动令牌 | Actions自动提供的 GITHUB_TOKEN | checkout用contents:read；发布GHCR才加packages:write |
| 本机GitHub访问 | gh auth login / 系统凭据管理器 | 只选目标仓库，按需授予Contents、Workflows、Actions Secrets权限 |

不要把真实 Docker Hub Token、GitHub Token 写到本文档；也不要从现有凭据管理器导出到可复用报告。需要提供凭据时，通过官方登录/密码管理器/Actions Secrets输入，禁止粘贴到聊天、命令行参数、镜像层或README。

配置步骤：GitHub仓库 Settings→Secrets and variables→Actions→添加上述DOCKERHUB_*；本机执行交互 docker login -u <用户名> 和 gh auth login。各仓库单独检查Secrets是否存在，权限与仓库可见性是否正确；不要依赖“其他仓库能发布”推断本仓库已配置。

如果后续Agent需要协助发布，只报告凭据名称、是否存在、到期/轮换需求，不输出内容。已泄露Token应撤销并更换，清理Git历史不能替代轮换。

官方依据：
- https://docs.docker.com/security/access-tokens/personal-access-tokens/
- https://docs.docker.com/security/access-tokens/reference/
- https://docs.github.com/en/actions/tutorials/authenticate-with-github_token
- https://docs.github.com/en/packages/managing-github-packages-using-github-actions-workflows/publishing-and-installing-a-package-with-github-actions

## 7. 向后兼容与迁移

本次UI更新不改业务API、数据库schema、会话数据、密钥或附件布局。不要删除data重新初始化。电影旧light/dark/system偏好继续可读，新增green；light现在是浅灰蓝。已有表单/API仍原样使用。

升级前做应用支持的备份或停机复制完整data（包括密钥），记录旧镜像digest。拉取/构建新镜像后只重建同一服务，挂载同一个data；不要down -v。确认登录、设置、历史和附件后再删除旧镜像。

电影新版服务改为UID/GID10001，入口先检查并准备专用data。普通独立数据目录自动处理；若有只读ACL、symlink、硬链接、特殊文件或嵌套挂载，先停机备份，再把数据复制到独立普通目录/命名卷并更新挂载。不要chmod 777，不要对NAS总目录递归chown。无法自动处理的结构需要人工迁移，不能宣称完全无条件兼容。

显式user:"0:0"不再需要，但默认入口仍会降权；自定义user必须对data有读写权限。如果使用cap_drop:[ALL]，root入口将无法完成所需的chown/setuid/setgid；应先准备专用卷权限，再显式使用非root user跳过初始化。no-new-privileges本身不禁止主动降权，不要误将它当作权限问题的根因。备份目录、旧镜像及原卷保留到验证结束。

本次不会把随手传的TOTP替换为账号密码，也不会把固件签名项目降级。未来如迁Vue，先建立旧页面行为测试、同构认证适配、附件/隐私对照，保留接口和数据，不接受“清库重装”迁移。

## 8. 后续Agent执行清单

1. 读取AGENTS、手册、代码，git fetch核对远端，保护已有工作区。
2. 列出真实页面/权限/API/数据/通知能力与差异，再改样式。
3. 小步改动；主题/关于模块不得发请求或接触认证状态；共享文件同步。
4. 运行appearance测试、原有前端/后端测试、构建；以真实浏览器检查375/768/1024/1440及三主题，验证长文本、错误、空态、未保存表单与上传。
5. Docker真实启动并检查PID1非root、健康、重启持久化；无真实凭据/数据进入镜像。
6. 记录提交、测试、镜像digest、构建产物、部署状态和未测项；CI运行中不能称通过，未推镜像不能称已发布。
7. 报告不含凭据值，不含设备画像；完成后保留可用本地演示，提供升级/回退方法。

