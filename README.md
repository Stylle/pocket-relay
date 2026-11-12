# 随手传 · Pocket Relay

在设备之间，随手发，随时取。

轻量自部署消息收件箱，用浏览器同步文字、图片、动图、视频和文件。适合 NAS、本机电脑和小型 VPS。

## 能做什么

- 同一账户、多设备同步，消息互相隔离；支持拖放、多选与粘贴图片。
- 复制文字和图片、下载文件，消息默认永久保留，也可设置有效期。
- 标准 TOTP 验证器登录；刷新保留登录、可配置空闲超时、主动退出；公开注册默认关闭。
- 本次消息与历史折叠；全部记录文字/时间范围搜索；按需加载图片；隐私正文点击显示、刷新重新遮挡（非内容加密）。
- Telegram 机器人双向同步，后台独立开启接收/发送，绑定私聊和归属账户；默认关闭。
- 本地、WebDAV、SMB 存储；密码加密的整站备份与恢复。

## 最简部署

支持 `linux/amd64` / `linux/arm64`。[Docker Hub 公共镜像](https://hub.docker.com/r/stylle/pocket-relay)，可直接拉取，无需登录。

将以下内容保存为 `docker-compose.yml`，或直接粘贴到 NAS 的 Compose 管理页面：

```yaml
# 飞牛 / 通用 NAS：可直接上传此文件或复制到 Compose 项目中。
# Docker Hub 公共镜像，可直接拉取，无需登录。
services:
  pocket-relay:
    image: stylle/pocket-relay:latest # 必填；公共镜像，无需仓库凭据
    container_name: pocket-relay             # 可省略，也可改名
    restart: always                         # 建议保留，恢复备份时需要重启
    ports:
      - "28182:8080"                        # 左侧为 NAS 访问端口，可改；右侧不改
    volumes:
      - relay-data:/data                    # 必须保留；数据由 Docker 命名卷保存

volumes:
  relay-data:                               # 必填，与上面的卷名一致

# 不需要 environment 或 .env；第一次打开网页绑定验证器。
# 请保留数据卷；删除卷会删除账户、密钥、配置和本地附件。
```

```bash
docker compose -f docker-compose.yml up -d
```

打开 `http://服务器IP:28182`，在可信内网绑定验证器。**第一个完成绑定的账户获得永久管理员权限。**无需环境变量、`.env` 或 setup-token；命名卷由 Docker 管理权限。

## 使用与维护

| 想了解 | 说明 |
| --- | --- |
| 指定宿主机文件夹、权限、源码构建 | [部署指南](docs/guide.md#docker-compose-部署) · [文件夹挂载模板](docker-compose.nas.yml) |
| 登录、临时账户、上传、复制、保留时间 | [使用指南](docs/guide.md#使用) |
| WebDAV / SMB、加密边界、备份与恢复 | [存储与备份](docs/guide.md#存储与备份) |
| 升级镜像、开发与测试 | [更新与开发](docs/guide.md#更新与开发) |

更新前备份数据，再执行 `docker compose -f docker-compose.yml pull` 和 `docker compose -f docker-compose.yml up -d`。保留原卷，不要执行 `down -v`。

## 使用边界

同一标签页刷新保留登录，默认无操作 15 分钟后重新验证；后台可设置分钟、天或关闭空闲超时。新导航、标签页重新打开和后退缓存恢复需验证；浏览器的自动会话恢复存在平台差异，详见指南。复制图片依赖 HTTPS 或 localhost 与浏览器支持。文件不是端到端加密；管理员整站备份包含全部账户数据，远程附件本体需另行备份。当前未接入微信机器人。访问与备份安全详见指南。
