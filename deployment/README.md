# 设备部署

部署入口为 `deployment/deploy.py`。安装包包含管理服务、音乐服务和 SHA-256 清单；页面、字体、图标及许可随程序内嵌。更新保留音乐、草稿、数据库、配置和设备身份。

## 前置条件

构建电脑需要 Go 1.25+、Node.js 22.17+、Python 3.8+ 和 OpenSSH。

Linux 目标设备需要：

- ARM64 或 x86_64，Python 3.6+、systemd 用户会话、`ip`、`lsblk`。
- 普通用户 SSH 密钥访问，已核验的主机密钥。
- Java 17，以及按[官方说明](https://minimserver.com/install-linux-mserver.html)安装并初始化的 MinimServer。
- 系统正常挂载、普通用户可写的 USB 音乐盘。

管理员可执行 `sudo loginctl enable-linger 用户名`，允许用户服务在登出后运行。本项目不格式化磁盘、不修改 fstab、不替换文件系统驱动。

## 首次准备

1. 先初始化音乐盘。可在运行管理 Web 的电脑上选择该盘；也可从离线包解出对应架构的 `hifi-manager`，复制到设备临时目录，以普通用户前台运行：

   ```bash
   ./hifi-manager -home ~/hifi -listen 127.0.0.1:8787 -data-disks
   ```

   通过 SSH 本地转发访问设置（例如 `ssh -L 18787:127.0.0.1:8787 music-server`）。注册新账号时，在设备上运行 `./hifi-manager -home ~/hifi -invite 受邀账号`，然后在 <http://127.0.0.1:18787> 点击「注册」并填入指定账号和邀请码。登录后确认初始化并选盘，再用 Ctrl+C 停止这个临时管理实例。不要手动创建未带盘标识的项目子目录。
2. 创建指向已初始化音乐目录的 `~/hifi/music` 符号链接；已有入口应先检查，不覆盖普通目录。将 MinimServer 官方程序安装到 `~/hifi/minimserver/`，运行厂商 `bin/startc`，将内容目录设为 `~/hifi/music` 的完整绝对路径，完成许可和初始配置后正常退出。不要复制其他设备的数据目录或身份，也不另启厂商自动启动方式。如使用独立 Java，配置 `java_home`。
3. 从 [example.json](../config/devices/example.json) 复制本机设备配置，例如 `config/devices/local.json`：

```json
{
  "ssh": "music-server",
  "profile": {
    "root": "~/hifi",
    "volume": "/media/music-disk",
    "name": "hifi-music",
    "manage_minim": true,
    "manage_bubble": false
  }
}
```

`ssh` 可使用本机 SSH 别名。`root` 是设备程序目录，`volume` 是首次准备的实际挂载路径；已有设备可从当前选盘状态识别。配置和凭据不提交仓库。

4. 在构建电脑执行：

```console
python deployment/deploy.py --device config/devices/local.json --prepare-minim
python deployment/deploy.py --device config/devices/local.json --check
python deployment/deploy.py --device config/devices/local.json --apply
```

局域网管理使用 HTTPS。启动设备服务前，在设备服务目录的 `config/` 放置 `manager-tls-cert.pem` 和仅服务用户可读的 `manager-tls-key.pem`。证书须覆盖实际访问的 IP。使用私有 CA 时，在浏览器端信任该 CA，并把 CA 证书保存为设备上的 `config/manager-tls-ca.pem`，供部署检查验证。

如果路由器会重新分配地址，可以在设备配置中设置 `manager_lan_hostname`，例如 `nano.local`。确认设备通过 Avahi 广播该名称后，使用覆盖这个 DNS 名称的 `manager-lan-tls-cert.pem`、`manager-lan-tls-key.pem` 和 `manager-lan-tls-ca.pem`，把管理地址保存为 `https://nano.local:8787`。客户端只需在首次使用时信任该 CA，后续地址变化无需重新签发证书。启用此设置前，先确认管理端没有上传或文件任务，再停止仍提供旧 IP 证书的管理进程以完成首次切换。

`hifi-lan-rebind.service` 会在开机时启动，监听 Linux 的网卡、IPv4 地址和路由变化。可信网段内的实际地址变更后，它会重启仍绑定旧地址的 HiFi 服务。恢复期间，管理连接和正在播放的流可能需要重新连接。

登录后在管理 Web 设置中选择数据盘。未初始化盘需明确确认，已选盘缺席时不会自动替换。MinimServer 未就绪或空盘未初始化时，控制页面仍可用于选盘。

## 配置项

| 配置 | 用途 |
| --- | --- |
| `java_home` | 独立 Java 安装目录 |
| `lan_ip`、`lan_cidr` | 可信局域网；未指定时从 SSH 连接识别 |
| `web_port`、`browse_port` | 管理和 UPnP 展示端口 |
| `backend_description` | 仅自动发现不可用时显式指定本机 MinimServer 描述 URL |
| `manage_bubble` | 可选管理已安装的 BubbleUPnP 服务 |
| `manager_public_domain`、`bubble_public_domain` | 可选公网域名，对应独立的回环入口 |

BubbleUPnP 如需启用，应按[官方文档](https://bubblesoftapps.com/bubbleupnpserver2/docs/any_install.html)单独安装到 `~/hifi/bubbleupnp/`。厂商许可和凭据由使用者准备。

通过 ChmlFrp 发布管理 Web 或 BubbleUPnP 远程播放时，见[公网部署与验收](CHMLFRP.md)。

## 日常更新

```console
python deployment/deploy.py --device config/devices/local.json --check
python deployment/deploy.py --device config/devices/local.json --apply
```

`--check` 只读，不构建或上传；`--apply` 先预检，再构建与安装。预检拒绝活动上传和文件任务，校验架构、网段、端口、目录及挂载代次。未变化的程序不重复上传；验收失败恢复本次替换的程序、配置和 unit，不恢复或覆盖用户数据。

需要完整重装设备上的项目部署时，将 `--apply` 换为 `--reinstall`。安装器先校验安装包，再停止并重新安装它管理的程序、启动脚本和 systemd unit，最后运行就绪检查。曲库、管理数据库、账号、证书与第三方服务配置保留。

仅构建离线包，不连接设备：

```console
python deployment/deploy.py --build arm64
python deployment/deploy.py --build amd64
```

目标服务启动时重新识别可信网段中的当前地址，不将上次 DHCP 地址固化进 unit。设备配置、UDN 和盘身份保留。

## 验收与维护

```bash
systemctl --user status hifi-music hifi-manager hifi-minimserver
journalctl --user -u hifi-music -u hifi-manager -n 80 --no-pager
```

部署器检查 Web、UPnP 描述、SCPD、实际浏览及活动盘。接盘后仍需从另一台电脑运行[只读验收工具](../music-service/README.md)，并验证实际播放和安全换盘。

测试使用临时目录与服务替身：

```console
python -B -m unittest discover -s deployment -p "test_*.py"
```

安装器语义测试在 Linux 执行，Windows 明确跳过；本机构建不能代替 Linux 挂载、进程组和 systemd 验收。
