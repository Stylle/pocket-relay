# 随手传 · Pocket Relay

一个只给自己发消息的轻量网页。电脑发，手机取；支持文字、表情、图片、动图、视频和任意文件。

## Docker Compose 部署

**公共镜像：`stylle/pocket-relay:latest`，支持 amd64 / arm64，无需登录镜像仓库。**

1. 准备 Docker 和 Compose，无需镜像仓库登录。
2. 在 NAS 的 Docker / Compose 管理界面新建项目，粘贴下面的 YAML 并启动。不同 NAS 的入口名称可能不同；如果要求上传文件，使用 [docker-compose.yml](../docker-compose.yml)。**纯镜像部署，不需要下载源码或设置构建目录。**
3. 打开 `http://NAS的IP:28182`，填写账户名并绑定验证器。**第一次初始化在可信内网完成，第一个完成绑定的账户为永久管理员。**

```yaml
services:
  pocket-relay:
    image: stylle/pocket-relay:latest # 必须保留；公共镜像，无需登录
    container_name: pocket-relay             # 可省略，也可改名
    restart: always                         # 建议保留，恢复备份时需要重启
    ports:
      - "28182:8080"                        # 左侧访问端口可改，右侧 8080 不改
    volumes:
      - relay-data:/data                    # 必须保留，用于持久化全部站点数据

volumes:
  relay-data:                               # 上面的命名卷需要此声明
```

**无需任何环境变量，没有 `.env`、初始密码或 setup-token。**账户、存储和公开注册都在网页内设置。命名卷由 Docker 管理权限，推荐直接使用；删除或重建容器不会删除此卷，但不要勾选删除数据卷，也不要执行 `docker compose down -v`。

### 希望数据放在指定 NAS 文件夹

使用 [docker-compose.nas.yml](../docker-compose.nas.yml)，示例如下。路径按你的 NAS 修改，容器内的 `/data` 不改。此写法用宿主机文件夹，不需要末尾的顶层 `volumes` 声明。

```yaml
services:
  pocket-relay:
    image: stylle/pocket-relay:latest
    container_name: pocket-relay
    restart: always
    ports:
      - "28182:8080"
    volumes:
      - /srv/pocket-relay/data:/data # 改为实际数据目录
```

镜像默认以 **UID/GID `10001:10001`** 运行。先创建专用文件夹并允许该用户读写，否则会出现 `permission denied`；只创建文件夹不一定有写权限。本镜像不识别 PUID/PGID 变量，也不需要为了部署改成 root。

下面的命令**仅用于新建的本项目空目录**，须先修改成自己的路径；不要对 NAS 其他数据目录递归改权限：

```bash
sudo mkdir -p /srv/pocket-relay/data
sudo chown 10001:10001 /srv/pocket-relay/data
sudo chmod 700 /srv/pocket-relay/data
```

### 使用 SSH / 命令行部署

把 [docker-compose.yml](../docker-compose.yml) 保存到 NAS 项目文件夹，在该文件夹中执行：

```bash
docker compose -f docker-compose.yml up -d
```

[compose.yml](../compose.yml) 是同样的命名卷模板，保留兼容已有部署。**不要把两份文件作为不同项目同时启动**，否则会占用相同端口和容器名。

### 可选：从源码构建

需要自行构建时，先取得代码仓库访问权限并下载完整源码，在源码目录执行：

```bash
docker compose -f compose.build.yml up -d --build
```

这不是 NAS 粘贴 YAML 的必需步骤；仅使用上面的现成镜像模板即可部署。

## 使用

- 首次输入账户名，用 Google Authenticator 或其他支持标准 TOTP 的验证器扫码，也能手动添加密钥。**密钥只在绑定时展示，请备份验证器。腾讯验证器需确认支持添加标准 TOTP 账户；不是 QQ 专用动态令牌。**
- 同一标签页的普通刷新恢复登录；新导航、新打开页面与后退缓存恢复重新验证。凭据只保存到标签页的 `sessionStorage`，不写入 `localStorage`。默认无操作 15 分钟退出，后台可配置分钟、天，或「不因空闲自动退出」；后台轮询不会续期。主动退出会立即撤销服务端登录和下载凭据。
- 浏览器对崩溃恢复、启动恢复和「恢复上次会话」的导航标记存在差异，网页无法可靠得知整个浏览器进程是否退出，不能承诺所有恢复方式都会重新验证。严格要求关闭重验时请关闭浏览器的会话恢复或使用隐私窗口，并保留空闲超时。
- 不因空闲退出并不绕过临时账户期限、主动退出和服务重启。为控制内存，每账户最多 64 个登录会话、整站最多 10000 个，超出时撤销最久未活动的会话。一个验证码只能用一次，多设备登录请等待下一组，设备需同步时间。
- 各设备登录同一个账户即可看到同一收件箱。不同账户不能互相查看消息、文件；管理员备份包含整个站点数据。
- 默认关闭公开注册。后台开启后，新账户默认有效 1 小时，可调整新账户有效期，也可延长已有临时账户。到期只停止访问，不自动删除永久消息。
- 文件可多选排队、拖放或粘贴剪贴板图片；`Ctrl/⌘ + Enter` 发送。支持复制文字、复制图片和下载；复制图片需要 HTTPS 或 localhost，以及浏览器支持。GIF 复制为静态首帧，下载保留动图。
- 默认永久保留；发送时可选 1 小时、1 天、7 天、30 天或自定义分钟数。到期立即停止访问，后台约一分钟清理附件；远程删除失败会重试。SSD/远程存储上的删除不等于物理安全擦除。
- 默认单文件 256 MB、每账户 5 GB，后台可修改。每次最多 4 个并发上传/远程读取任务；文件落盘传输，不整份加载到服务内存。
- 消息每 4 秒同步一次（页面可见时）。设备名手动设置，浏览器不能读取系统用户名。来源 IP 默认脱敏，反代环境显示代理的脱敏 IP，不信任客户端提供的 `X-Forwarded-For`。

## 存储与备份

后台选择本地、WebDAV 或 SMB。远程存储先通过读写测试才能保存，再选择为默认位置。切换只影响新文件；旧存储配置保留给旧附件使用。WebDAV 填完整服务地址和可选子目录；SMB 填主机 IP/名称、账户和 `共享名/子目录`，使用 TCP 445，仅建议在可信内网或 VPN 中使用。镜像内置 rclone，仅 SMB 操作时启动，无常驻副进程。

所有元数据和加密密钥在 `/data`；远程密码、TOTP 密钥使用 AES-256-GCM 加密保存。**master.key 与数据库同卷保存，因此不能防止拿到整个 data 的人解密；这不是端到端加密。**本地附件不做内容加密，请保护宿主机、NAS 权限并用 HTTPS 访问。

后台可下载密码加密 `.prb` 备份，包含数据库、密钥、所有用户数据和本地附件。远程附件仅备份连接配置及索引，**远程文件本体需另行备份**。备份密码至少 12 字符，丢失无法恢复。格式使用 PBKDF2-SHA256（600,000 次）和分块 AES-GCM，校验篡改、截断及本地附件 SHA-256。

恢复前先验证备份，重启时替换数据；旧数据留在 `/data/previous-*`，确认恢复无误后自行清理。Docker 必须保留 `restart: always`。单份恢复上限 20 GB，需要足够临时磁盘空间。浏览器支持文件流保存时备份直接落盘，否则下载会占用浏览器内存；大量数据也可停容器后直接备份整个 data 卷。远程文件预览/下载暂存到磁盘以支持视频拖动，完成后清理。

## 微信

当前版本不直接登录个人微信，也未接入企业微信消息回调。个人微信非官方机器人存在账号风险；普通企业微信群 Webhook 主要用于发往群聊，不能当作接收任意私人微信消息的接口。后续可做独立的官方企微回调适配器，不影响站点登录和数据隔离。

## 更新与开发

```bash
docker compose -f docker-compose.yml pull
docker compose -f docker-compose.yml up -d
```

NAS 图形界面部署则更新镜像并重新创建原项目，保留原数据卷或文件夹挂载。若使用 `compose.yml` 或 `docker-compose.nas.yml`，命令中的文件名对应替换即可。

Go 1.26+，无前端构建步骤：`go test ./... && go vet ./... && go run .`。非 Docker 默认数据目录 `./data`；可选 `DATA_DIR`、`LISTEN_ADDR`，默认端口 8080。没有其他必填变量。

GitHub Actions 执行自动测试、真实 SMB 回环测试、Docker 启动/重启测试，再发布多架构 GHCR 镜像。新仓库如配置 `DOCKERHUB_USERNAME` 和 `DOCKERHUB_TOKEN` Secrets，推送 `v*` 标签时额外发布 Docker Hub。
