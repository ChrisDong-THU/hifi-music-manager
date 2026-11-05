# 音乐服务

独立的 Go 服务，监督 USB 音乐盘和 MinimServer，并提供只读 UPnP 展示。停止管理 Web 不影响已就绪的播放资源。

## 运行模型

- `shared-library/` 统一资源格式、盘身份和选盘快照。
- 监督器核验真实挂载与盘内标识，已选盘缺席时等待恢复。
- 原生运行器按盘使用 `minimserver/<device-id>/`，监督进程组和目录句柄；不在多个盘之间复制播放索引。
- `browsebridge/` 处理上游发现、XML、分页、事件订阅和公共歌手资料；不提供写入 API。
- Web 在资源提交后异步通知播放刷新，投递失败不回滚资源。

设备上的 `music-service/config.json` 由部署器生成，包含 `lan_cidr`、`name`、`browse_udn`、`browse_listen` 和 `backend_description`。更新保留既有身份，示例配置见[部署指南](../deployment/README.md)。

## 检查与维护

```console
cd music-service
go vet ./...
go test ./...
```

设备上仅校验配置、不启动监听或访问数据盘：

```bash
~/hifi/music-service/hifi-music-service -home ~/hifi -check
```

安全换盘先暂停，待界面显示离线后由系统安全弹出；重新挂载后恢复：

```bash
python3 scripts/library-control.py pause --home ~/hifi
python3 scripts/library-control.py resume --home ~/hifi
```

从另一台局域网电脑执行只读验收：

```console
python scripts/verify-single-library.py --config device-runtime-config.json --expected-tracks 300
```

验收检查发现、身份、完整分页、控制隔离和音频 Range，默认完整抽查 3 首音频；`--full-audio` 才读取全部音频。实际客户端播放及 Linux USB 热拔插仍须实机验证。

来源与许可见 [THIRD_PARTY_NOTICES.txt](THIRD_PARTY_NOTICES.txt)。
