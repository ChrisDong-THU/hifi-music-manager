# 管理服务

Go HTTP 服务、React/MUI 界面及 bbolt 管理数据。静态资源、字体和第三方许可嵌入可执行文件。

## 入口

```console
npm --prefix manager-service/frontend ci
npm --prefix manager-service/frontend run build
cd manager-service
go run . -home ../downloads/manager-local -listen 127.0.0.1:8787 -data-disks
```

省略 `-data-disks` 可使用 `-home` 下的固定目录测试。生产数据应通过已挂载的 USB 盘管理；局域网监听须同时指定可信网段 `-allow-lan`。

## 源码定位

| 路径 | 职责 |
| --- | --- |
| `main.go`、`data_disks*.go` | HTTP 生命周期、盘选择与会话 |
| `http.go`、`draft*.go`、`import.go` | 上传、草稿读写、导入与删除 |
| `catalog.go`、`library_index.go`、`library_query.go` | 不可变读模型与分页查询 |
| `audio*.go`、`internal/audioquality/` | 标签、音频摘要与质量分析 |
| `tasks.go`、`cleanup*.go`、`playback_sync.go` | 任务执行、清理、播放刷新 |
| `frontend/src/` | 页面、共享查询资源、上传、播放器和通知 |

前端复用条件查询和分页资源，表单只存本地修改。草稿行支持点击选择、悬停高亮和键盘焦点提示；标签弹窗不会改变勾选状态，鼠标关闭后不保留焦点高亮。

[系统架构](../ARCHITECTURE.md) · [开发与测试](../docs/DEVELOPMENT.md) · [API](../docs/MANAGEMENT-API.md) · [数据规则](../docs/STORAGE.md) · [设计来源](UPSTREAM.md)
