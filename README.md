# HiFi Music Manager

面向本机和局域网的音乐库管理工具。上传音乐、整理标签与封面、检测音源，并通过独立的 MinimServer 适配服务提供 UPnP 浏览和播放资源。

Go 服务内嵌 React 界面、字体和许可证，运行时无需 Node.js 或数据库服务器。

## 功能

- 文件、文件夹、拖放和粘贴上传，按专辑整理草稿。
- 歌曲多选、手动标签与封面编辑、重复音频提示和选择性导入。
- FLAC、MP3 标签与封面写入；其他支持格式可原样导入。
- FLAC、WAV、AIFF 整数 PCM 位深和频谱分析，结果用于提示，不证明母带来源。
- 专辑、歌手、曲目搜索，浏览器试听，歌手头像与简介管理。
- USB 数据盘选择、按盘索引、挂载校验和独立的播放索引同步。

删除为永久删除，批量操作按歌曲逐项提交。活动草稿不按年龄清理。详见[存储与操作](docs/STORAGE.md)。

## 快速开始

构建环境：Go 1.25+、Node.js 22.17+；离线部署工具另需 Python 3.8+ 和 OpenSSH。

Windows 在项目根目录执行：

```powershell
powershell -File scripts/update-manager-local.ps1
```

打开 <http://127.0.0.1:8787>，在首页设置中选择系统已挂载的 USB 数据盘。初始化需要明确确认，不格式化磁盘，也不清理盘内其他目录。

仅构建、不启动服务：

```powershell
powershell -File scripts/build-manager.ps1 -OutputDirectory downloads/manager-build
```

Linux 或其他开发环境的直接启动方式见[开发指南](docs/DEVELOPMENT.md)。默认仅监听回环地址；局域网访问需显式配置可信网段，管理端口不应公开到互联网。

## 设备部署

目标支持 Linux ARM64 和 x86_64。音乐服务需要已安装并初始化的 MinimServer、Java 17、systemd 用户服务及可写 USB 音乐盘。

```console
python deployment/deploy.py --build arm64
python deployment/deploy.py --device config/devices/local.json --check
python deployment/deploy.py --device config/devices/local.json --apply
```

将 [example.json](config/devices/example.json) 复制为本机的 `local.json` 并填写设备配置。完整步骤见[部署指南](deployment/README.md)。构建不会连接设备；更新保留既有数据、配置和身份。

## 项目结构

| 路径 | 职责 |
| --- | --- |
| `manager-service/` | Go 管理 API、React/MUI 前端、音源分析 |
| `music-service/` | 数据盘监督、MinimServer 运行适配、UPnP 展示 |
| `shared-library/` | 共享资源格式、盘状态和安全文件访问 |
| `deployment/` | 离线构建、预检、安装与失败回退 |
| `scripts/` | 本机启动与更新、设备维护及只读验收 |
| `docs/` | 开发、接口与数据规则 |
| `config/devices/` | 通用配置示例；个人设备配置不入库 |

`downloads/`、`diagnostics/`、`secrets/` 是本机目录，不进入仓库。

## 文档与贡献

- [系统架构](ARCHITECTURE.md)
- [开发与测试](docs/DEVELOPMENT.md)
- [管理 API](docs/MANAGEMENT-API.md)
- [存储与操作](docs/STORAGE.md)
- [贡献约定](CONTRIBUTING.md) · [安全说明](SECURITY.md)
- [第三方来源与许可](NOTICE.md)

项目自有代码许可见 [LICENSE](LICENSE)；第三方代码、字体、图标及音源分析代码保留各自许可。
