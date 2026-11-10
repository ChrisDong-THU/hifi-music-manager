# Nano 公网访问

ChmlFrp 用一条 HTTPS 隧道把公网域名转发到 Nano 的 `127.0.0.1:18787`。管理 Web 使用域名根路径，WebDAV 使用同一域名的 `/webdav/`。

1. 先按[设备部署指南](README.md)配置局域网入口。在 Nano 的服务目录 `config/` 放置 `manager-public-tls-cert.pem`（完整证书链）和权限为 `600` 的 `manager-public-tls-key.pem`。证书须覆盖公网域名。
2. 在本机设备配置的 `profile` 中设置域名，并按需启用公网 WebDAV。已有管理隧道可继续使用。

   ```json
   {
     "manager_public_domain": "manage.example.net",
     "webdav_public": true
   }
   ```

3. 运行部署器的 `--check` 和 `--apply`。在 ChmlFrp 控制台配置公网域名和 HTTPS 隧道，目标为 `127.0.0.1:18787`。按面板提示添加 DNS 记录，确保 TLS 到达 Nano。
4. 将节点配置存放在 Nano 的 `~/.config/chmlfrp/hifi.ini`（权限 `600`）。如果尚未安装项目的隧道 unit，把 [hifi-chmlfrp@.service](hifi-chmlfrp@.service) 复制到 Nano 后运行：

   ```bash
   install -D -m 0644 hifi-chmlfrp@.service ~/.config/systemd/user/hifi-chmlfrp@.service
   systemctl --user daemon-reload
   systemctl --user enable --now hifi-chmlfrp@hifi.service
   ```

从外网登录管理 Web，点击「WebDAV 连接」。在播放器添加显示的 HTTPS 地址和专用凭据，确认可以浏览和播放。对 `/webdav/` 发出未认证的 `PROPFIND` 应返回 `401`。公网 WebDAV 关闭时该路径返回 `404`。取消 `webdav_public` 并重新部署即可停止公网曲库共享。

## 退役旧播放入口

如果从旧部署切换，先确认新管理和 WebDAV 可访问，再移除 ChmlFrp 配置与面板中指向 `127.0.0.1:18443` 和 `127.0.0.1:18080` 的旧播放隧道，保留 `18787` 的管理隧道。更新隧道 unit 后重新加载 systemd 并重启隧道。旧音乐域名不再需要时，可以移除相应 DNS 记录。

若设备安装了专供旧中继使用的系统服务 `hifi-bubble-wan-peer.service`，确认用途后以管理员身份停用并移除该 unit，再移除它在回环接口添加的 `192.0.2.1/32`。若 `/etc/authbind/byport/80` 只用于旧播放服务，也可移除这一授权文件。保留厂商配置、已有证书和音乐盘上的旧数据目录。
