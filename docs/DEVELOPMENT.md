# 开发与测试

## 环境

| 工具 | 要求 |
| --- | --- |
| Go | 1.25 或更高，两个模块均有独立 `go.mod` |
| Node.js | 22.17 或更高，前端使用锁定的 npm 依赖 |
| Python | 3.8 或更高，用于部署工具和脚本测试 |
| OpenSSH | 仅设备预检、安装及验收需要 |

## 启动管理 Web

先生成内嵌前端；无需另开前端代理：

```console
npm --prefix manager-service/frontend ci
npm --prefix manager-service/frontend run build
cd manager-service
go run . -home ../downloads/manager-local -listen 127.0.0.1:8787 -data-disks
```

默认启用 USB 选盘。仅调试界面、使用独立测试目录时，可移除 `-data-disks` 并将 `-home` 指向新建的临时目录。不要用实际音乐数据做破坏性测试。

Windows 也可使用 `scripts/update-manager-local.ps1` 构建并后台更新，或 `scripts/start-manager-local.ps1` 启动已有程序。固定目录模式使用 `-FixedLocalStorage -DataDirectory 指定目录`，前台运行用 Ctrl+C 停止。

管理服务首次启动时在服务目录的 `config/manager-invite.key` 创建仅服务用户可读的签发密钥。在服务所在机器运行 `hifi-manager -home 服务目录 -invite 受邀账号` 签发邀请码，设备模式也可用 `-library-home 服务目录`。邀请码绑定账号，30 分钟有效，成功注册即核销。注册的账号拥有完整管理权限。

新密码至少 8 个字符，最多 256 字节。账号保存在服务目录的 `config/manager-auth.db`。同一账号在 5 分钟内输错密码 5 次后暂停登录 5 分钟，重启不会清除限制。会话在 12 小时后过期，服务重启也会使会话失效。

回环地址支持本机 HTTP。其他地址需要 `-allow-lan`，并用 `-tls-cert` 和 `-tls-key` 指定浏览器信任的 PEM 证书与私钥。

## 检查

```console
npm --prefix manager-service/frontend test
npm --prefix manager-service/frontend run build
python -B -m unittest discover -s deployment -p "test_*.py"
python -B -m unittest discover -s scripts/tests -p "test_*.py"
```

分别在 `shared-library/`、`manager-service/` 执行：

```console
go vet ./...
go test -count=1 ./...
go test -race ./...
```

竞态检测需要 C 编译器。Go 测试前应生成管理前端和内嵌许可。请在 Windows 和 Linux 上分别运行平台专属测试。

## 构建

```console
python -B deployment/deploy.py --build arm64
python -B deployment/deploy.py --build amd64
```

入口运行依赖校验、前端与 Go 检查、部署脚本测试，再生成离线包。输出位于 `downloads/deployment/`；相同源码和工具链复用经过校验的缓存。该命令不连接或启动设备。

Windows 管理程序可单独通过 `scripts/build-manager.ps1 -OutputDirectory downloads/manager-build` 构建。构建本身不会更新正在运行的实例。

## 维护规则

复用现有查询、表单、通知和主题实现；优先合并重复职责，避免仅为转发而增加抽象。测试应覆盖状态转换、失败边界及并发语义，不镜像样式和目录结构。

修改音源分析代码后执行前端构建，同步内嵌的 MPL 源码副本。第三方来源见 [NOTICE](../NOTICE.md)，贡献要求见[贡献指南](../CONTRIBUTING.md)。
