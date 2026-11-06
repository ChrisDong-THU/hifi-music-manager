# Nano 公网访问

先按[设备部署指南](README.md)准备 Nano、MinimServer 和音乐盘。下文以 `manage.example.net` 和 `music.example.net` 为例，分别替换成已备案域名下的两个子域名。

| 用途 | 公网地址 | ChmlFrp 类型 | Nano 上的隧道目标 |
| --- | --- | --- | --- |
| 管理 Web 和浏览器试听 | `https://manage.example.net` | HTTPS | `127.0.0.1:18787` |
| BubbleUPnP 登录和浏览 | `https://music.example.net` | HTTPS | `127.0.0.1:18443` |
| BubbleUPnP 媒体流 | `http://music.example.net` | HTTP | `127.0.0.1:18080` |

在局域网通过 `https://<Nano-IP>:8787` 使用管理 Web，原生 UPnP 继续由局域网发现。外网的 BubbleUPnP 客户端通过 BubbleUPnP Server 访问同一个音乐库。公网音乐入口的 HTTP 端口用于带令牌的媒体流和拖动进度，BubbleUPnP 的连接设置使用 HTTPS。不要映射 UPnP 的发现端口、MinimServer 原生端口或 BubbleUPnP 的配置端口。[BubbleUPnP 连接说明](https://www.bubblesoftapps.com/bubbleupnpserver2/docs/connect_with_bubbleupnp.html) · [媒体流与认证](https://www.bubblesoftapps.com/bubbleupnpserver2/docs/FAQ.html)

## 1. 在局域网完成初始设置

1. 按[官方安装指南](https://www.bubblesoftapps.com/bubbleupnpserver2/docs/any_install.html)把 BubbleUPnP Server 安装到 `~/hifi/bubbleupnp/`。先用 `manage_bubble: true` 的设备配置部署局域网服务，再打开 `http://<Nano-IP>:58050/` 完成 Bubble 的初始设置。
2. 设置 Bubble 的远程登录凭据。启用远程访问，关闭从外网打开配置界面的选项。在「Media Servers」中选中 HiFi 的 UPnP 服务，打开该服务的 Internet 访问选项，并在本地导入一首测试曲目。Bubble 的远程账号和管理 Web 账号独立。[网络与安全设置](https://www.bubblesoftapps.com/bubbleupnpserver2/docs/config_network_and_security.html) · [媒体服务器设置](https://www.bubblesoftapps.com/bubbleupnpserver2/docs/config_media_servers.html)
3. 用普通服务用户在 `~/hifi/config/` 放置下表文件。公网证书使用浏览器和 Android 信任的 CA，证书文件包含完整证书链。可用 DNS 验证先签发两个子域名的证书，续期后重新启动对应服务。

   | 入口 | 证书 | 私钥，权限 `600` |
   | --- | --- | --- |
   | 局域网管理 Web | `manager-tls-cert.pem` | `manager-tls-key.pem` |
   | 管理域名 | `manager-public-tls-cert.pem` | `manager-public-tls-key.pem` |
   | 音乐域名 | `bubble-public-tls-cert.pem` | `bubble-public-tls-key.pem` |

   局域网管理证书须覆盖 Nano 的局域网 IP。使用私有 CA 时，浏览器须信任该 CA，并在 Nano 的同一目录保存 `manager-tls-ca.pem`。

## 2. 安装 Bubble 中继的系统前置配置

BubbleUPnP Server 根据连接的源 IP 决定是否要求远程认证。中继用 `192.0.2.1/32` 作为 Nano 上的本地源地址，让外网请求进入 Bubble 的远程认证流程。Bubble 生成的媒体链接会使用自己的 HTTP 端口，因此公网 HTTP 为 80 时，Bubble 的本地 HTTP 端口也配置为 80。

在 Nano 上以实际运行 HiFi 服务的普通用户执行下列命令。先把本目录的 `hifi-bubble-wan-peer.service` 复制到 Nano 的当前目录。

```bash
sudo apt-get install authbind
if ! sudo test -e /etc/authbind/byport/80; then
    sudo install -o "$(id -un)" -g "$(id -gn)" -m 0500 /dev/null /etc/authbind/byport/80
fi
test -x /etc/authbind/byport/80
sudo install -m 0644 hifi-bubble-wan-peer.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now hifi-bubble-wan-peer.service
```

若端口 80 或 `192.0.2.1` 已在用，先核对现有用途。`authbind` 仅为指定用户授予绑定端口 80 的权限，Bubble 的 systemd 服务仍用普通用户运行。启用后，Bubble 的局域网配置页面迁至 `http://<Nano-IP>/`。

在本机设备配置的 `profile` 里加入以下字段，再运行部署器的 `--check` 和 `--apply`。部署验收使用真实域名校验两条回环 HTTPS 入口的证书，并要求 Bubble 的匿名连接测试返回 Digest 认证挑战。

```json
{
  "manage_bubble": true,
  "manager_public_domain": "manage.example.net",
  "bubble_public_domain": "music.example.net"
}
```

## 3. 创建隧道和 DNS 记录

在 ChmlFrp 控制台按表创建三个隧道。音乐域名的两个隧道选同一节点，使用同一个自定义域名。把两个子域名按控制台提示添加 DNS 记录。面板提供节点主机名时使用 CNAME，使用第三方 DNS 时选择仅 DNS 解析。HTTPS 隧道把 TLS 交给 Nano 的本地入口处理。[ChmlFrp 映射教程](https://docs.chmlfrp.net/docs/use/mapping) · [CNAME 教程](https://docs.chmlfrp.net/docs/dns/cname.html)

在 Nano 上用 `uname -m` 确认架构，从 [ChmlFrp 下载中心](https://panel.chmlfrp.net/tunnel/download)下载对应的官方 Linux 核心客户端，将解压出的 `frpc` 安装为 `~/.local/bin/chmlfrp`。按节点从面板导出 INI，保存为 `~/.config/chmlfrp/hifi.ini`，权限设为 `600`。使用面板生成的节点和凭据字段。INI 与私钥保存在设备上。

把本目录的 `hifi-chmlfrp@.service` 复制到 Nano 的当前目录，以普通用户运行下列命令。

```bash
install -D -m 0644 hifi-chmlfrp@.service ~/.config/systemd/user/hifi-chmlfrp@.service
systemctl --user daemon-reload
systemctl --user enable --now hifi-chmlfrp@hifi.service
systemctl --user status hifi-chmlfrp@hifi.service hifi-manager.service hifi-bubble-relay.service
```

如果管理隧道使用另一节点，为它保存单独的 INI，例如 `manager.ini`，再启用 `hifi-chmlfrp@manager.service`。

## 4. 从外网验收

手机关闭 Wi-Fi 后完成以下验收。

1. 在 `https://manage.example.net` 登录管理 Web，播放一首曲目并拖动进度。管理 Web 在局域网用 Nano IP 单独登录。
2. 在 Android BubbleUPnP 的「Internet access with BubbleUPnP Server」添加 `https://music.example.net`，填入 Bubble 的远程账号，运行连接测试。选中 HiFi 媒体库，播放同一首曲目并拖动进度。
3. 匿名访问 `https://music.example.net/res/connectiontest` 应返回 `401` 和 Digest 挑战。公网音乐入口应拒绝访问 Bubble 配置页面。`http://music.example.net/res/connectiontest` 应拒绝访问，媒体链接由登录后的浏览结果取得。

若只有浏览成功，检查 Bubble 生成的媒体链接是否指向 `http://music.example.net` 的 80 端口，并确认 ChmlFrp 的 HTTP 隧道在线。若管理页面能开但试听失败，检查请求是否返回 `206 Partial Content`。在外网验收前不要复用旧的 TCP 隧道配置。

临时停止公网入口时，停用对应 `hifi-chmlfrp@...` 用户服务。若要撤销项目内的公网入口，从设备配置移除 `manager_public_domain` 和 `bubble_public_domain` 后重新部署。
