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

在项目根目录运行统一检查，不启动管理实例、不连接设备、不读写实际音乐数据：

```console
python -B scripts/check.py
```

入口依次校验锁定的前端依赖，运行前端测试与构建，检查全部 Go 源码格式和包依赖边界，在两个模块中执行 `go vet`、`go test`，最后运行部署及脚本测试；Windows 的 Python 脚本测试会执行 5 项隔离的 PowerShell 更新生命周期检查，其他平台按平台条件跳过该测试并报告跳过数量。任何步骤失败都会立即以非零状态退出。依赖缺失或锁文件变化时，前端步骤使用现有的 `npm ci` 校验逻辑；缓存沿用 `downloads/` 下的目录。

Go 默认使用 `downloads/go-admin/go/bin/` 内的工具链，其次使用 PATH；也可显式指定：

```console
python -B scripts/check.py --go /path/to/go
python -B scripts/check.py --race
```

检查会打印实际 Go 版本，禁用隐式工具链下载并以本机平台运行。`--race` 要求已安装可用的 C 编译器，缺失时明确失败，不自动跳过。GitHub CI 在 Windows 和 Linux 上执行完整检查，Linux 额外启用竞态检测；平台专属测试在对应系统运行。

日常定位可使用 `--scope frontend`、`--scope go` 或 `--scope python`。Go 范围仍会校验前端依赖并重建内嵌资源，防止用过期 UI 验证；仅默认 `all` 范围代表完整检查。

可通过 `--staticcheck /path/to/staticcheck` 在两个 Go 模块中增加静态分析，发现问题同样阻断检查。入口只使用显式指定的现有工具，不自动下载安装。CI 的独立强化任务使用 Go 1.27.3 安装固定的 `honnef.co/go/tools/cmd/staticcheck@v0.8.2` 到临时工具目录；该分析器安装要求 Go 1.26 或更新版本，建议使用与该 CI 任务相同的 Go 1.27.3。项目的最低 Go 版本仍由两个 `go.mod` 定义，并保留 Windows/Linux 下限检查。

架构检查解析所有平台的 Go 源文件，包括当前系统不会编译的文件，缺失或空目录不会通过。`shared-library` 不允许反向依赖管理服务；`internal/audioquality` 只使用标准库和已采用的 FLAC 解码依赖，不能引入网络、数据库或应用业务层。新增依赖需明确修改规则并审查；规则及门禁自身也有失败用例。

## 构建

```console
python -B deployment/deploy.py --build arm64
python -B deployment/deploy.py --build amd64
```

缓存未命中时，构建入口调用同一个 `scripts/check.py` 完整门禁，成功后才交叉编译并生成离线包。输出位于 `downloads/deployment/`；相同源码和工具链复用经过校验的缓存。该命令不连接或启动设备。

Windows 管理程序可单独通过 `scripts/build-manager.ps1 -OutputDirectory downloads/manager-build` 构建。构建本身不会更新正在运行的实例。

## 维护规则

复用现有查询、表单、通知和主题实现；优先合并重复职责，避免仅为转发而增加抽象。测试应覆盖状态转换、失败边界及并发语义，不镜像样式和目录结构。

修改音源分析代码后执行前端构建，同步内嵌的 MPL 源码副本。第三方来源见 [NOTICE](../NOTICE.md)，贡献要求见[贡献指南](../CONTRIBUTING.md)。
