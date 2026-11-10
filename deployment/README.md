# 设备部署

`deployment/deploy.py` 构建一个内嵌管理 Web 和 WebDAV 的 Linux 程序。更新会保留正式音乐、草稿、数据库、账号、证书和盘标识。

构建电脑需要 Go 1.25+、Node.js 22.17+、Python 3.8+ 和 OpenSSH。目标设备需要 Linux ARM64 或 x86_64、Python 3.6+、systemd 用户会话、`ip`、`lsblk`，以及已核验主机密钥的 SSH 访问。用 `sudo loginctl enable-linger 用户名` 允许服务在登出后运行。音乐盘由系统挂载，本项目不格式化磁盘或修改挂载配置。

## 配置与安装

1. 将 [example.json](../config/devices/example.json) 复制为本地设备配置，例如 `config/devices/local.json`。修改 `ssh` 和 `profile.root`，可用 `profile.volume` 指定首次安装时要验收的实际挂载路径。
2. 把 HTTPS 证书和私钥放到设备服务目录的 `config/` 下。用 IP 访问时，证书须覆盖设备 IP，文件名为 `manager-tls-cert.pem` 和 `manager-tls-key.pem`。使用私有 CA 时，将 CA 证书保存为 `manager-tls-ca.pem`，并在浏览器和播放器中信任该 CA。
3. 若设备地址会变化，可使用由 Avahi 广播的名称（例如 `nano.local`）。设置 `manager_lan_hostname`，使用覆盖此名称的 `manager-lan-tls-cert.pem`、`manager-lan-tls-key.pem` 和可选 `manager-lan-tls-ca.pem`。监听器会随可信网段内的地址变化重新绑定。
4. 在构建电脑执行：

   ```console
   python deployment/deploy.py --device config/devices/local.json --check
   python deployment/deploy.py --device config/devices/local.json --apply
   ```

5. 新设备启动后，在设备上运行 `~/hifi/manager-service/hifi-manager -library-home ~/hifi -invite 受邀账号`。打开管理地址，注册并登录，在首页设置中选择或确认初始化数据盘，再点击「WebDAV 连接」取得播放器需要的地址、用户名和访问密码。已注册账号和选盘设置继续生效。

| 配置 | 用途 |
| --- | --- |
| `lan_ip`、`lan_cidr` | 可信局域网；缺省时从 SSH 连接和设备接口识别 |
| `web_port` | 管理 Web 与 WebDAV 共用 HTTPS 端口，默认为 8787 |
| `manager_lan_hostname` | 可选 .local 名称及对应证书 |
| `manager_public_domain`、`webdav_public` | 可选公网域名；设 `webdav_public: true` 才在该域名启用 WebDAV |

## 更新与验收

`--check` 只做预检。 `--apply` 验证包并更新改变的程序、启动器和 unit；`--reinstall` 强制重新安装这些部署文件。预检会拒绝进行中的上传和文件任务。旧部署切换时，安装器停用并移除项目的旧播放 unit 和程序，保留厂商配置及盘内数据。验收失败时恢复原程序和服务。原公网播放隧道及其系统级辅助配置的退役步骤见[公网部署与验收](CHMLFRP.md)。

```bash
systemctl --user status hifi-manager hifi-lan-rebind
journalctl --user -u hifi-manager -n 80 --no-pager
```

安装器验证管理页面、WebDAV 认证挑战、已认证的根目录浏览及活动盘身份。验收时也可用播放器逐级浏览、播放并拖动进度；WebDAV 支持 `PROPFIND` 的深度 0 和 1，以及音频的 HTTP Range。盘离线时，管理页面仍可选盘，WebDAV 曲库返回 503。安全拔盘前先等上传和文件任务结束，停止管理服务并由系统卸载，重新挂载后再启动服务。

仅构建离线包用 `python deployment/deploy.py --build arm64` 或 `--build amd64`。部署语义测试可用 `python -B -m unittest discover -s deployment -p "test_*.py"`；Linux 下会测试失败回退和旧部署清理。
