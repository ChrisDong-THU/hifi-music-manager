# 管理 API

API 与内嵌前端共同构建。接口返回 JSON，错误为 `{error}`；后台命令返回 202 `{job}`，表示已受理，终态从控制状态读取。

## 会话

`GET /api/auth/status` 返回 `{authenticated}`。`POST /api/auth/register` 接受 `{username, password, invite_code}`，注册时核销绑定账号且未过期的邀请码。`POST /api/auth/login` 接受 `{username, password}`。注册和登录成功后，服务通过 HttpOnly Cookie 建立会话。认证请求使用同源 JSON，局域网管理使用 HTTPS。

密码按原样作为字符串传递。新密码至少 8 个字符，最多 256 字节。同一账号在 5 分钟内登录失败 5 次后，该账号暂停登录 5 分钟；其他账号及已有会话不受影响。限流响应返回 429 和 `Retry-After`。

管理 API、封面和音频要求有效的登录 Cookie。登录后 `GET /api/state` 返回 CSRF Cookie、令牌和 `library_session`。修改请求携带 Cookie、`X-CSRF-Token` 与 `X-Library-Session`，查询也绑定当前盘上下文。原生音频和封面请求使用 `library_session` 查询参数。

换盘或重挂会更换曲库会话，服务重启会清除登录会话。`GET /healthz` 仅在本机和局域网入口可用，无需登录，提供服务名、任务是否繁忙、存储是否就绪和当前数据盘标识，用于启动与安全更新检查。

## 控制与草稿

| 方法与路径 | 正文或返回 |
| --- | --- |
| `GET /api/state` | 存储、统计、运行任务、有限操作摘要 |
| `GET /api/system/disks` | 可选择的数据盘 |
| `POST /api/system/disks` | 选盘及显式初始化确认 |
| `GET /api/drafts` | `{drafts, revision}`；摘要不含歌词 |
| `GET /api/drafts/{id}` | 完整编辑快照与 `edit_token` |
| `POST /api/drafts` | `{name}` → 200 `{id}` |
| `POST /api/drafts/{id}/files?path=...` | 原始音频请求体；同名重试须内容一致 |
| `POST /api/drafts/{id}/prepare` | `{}`；读取、分组并检测 |
| `POST /api/drafts/{id}/preview` | `{}`；重新读取文件标签 |
| `POST /api/drafts/{id}/quality` | `{}`；重新检测音源 |
| `POST /api/drafts/{id}/tags` | `{tags}` 或包含 `tags` JSON 与 `cover` 的表单 → 200 `{tags}` |
| `POST /api/drafts/{id}/import` | `{ids, duplicate_policy}` |
| `POST /api/drafts/{id}/delete` | `{ids}` |
| `POST /api/drafts/{id}/discard` | `{}`；仅移除完全空的草稿 |

标签保存、导入和删除须把读取到的 `edit_token` 原样放入 `If-Match`。缺少返回 428，过期返回 412；排队命令执行前再次核验。`ids` 必须非空，重复策略为 `keep`、`skip` 或 `reject`。

状态与草稿摘要支持 ETag / 304。`revision` 是失效提示，不能替代编辑令牌或跨接口事务。详情用于初始化编辑表单，不能使用剔除歌词的摘要代替。

## 曲库与资料

`GET /api/tracks`、`/api/albums` 支持分页和筛选；响应包含对应数组、`total` 和 `revision`，追加页必须属于同一曲库代次。`GET /api/artists` 返回歌手摘要。

曲目修改与删除使用 `POST /api/tracks/edit`、`/api/tracks/delete`；专辑使用 `/api/albums/detail`、`/api/albums/edit`、`/api/albums/delete`；歌手资料使用 `/api/artists/detail`、`/api/artists/edit`。

音频支持 HTTP Range。修改是直接提交，部分成功与清理规则见[存储与操作](STORAGE.md)。
WebDAV 连接信息由 `GET /api/system/webdav` 提供，需要管理会话，不要求有活动盘。启用时返回 `enabled`、`url`、`username`、`password`，禁用时仅返回 `{"enabled":false}`。该响应不得缓存。播放器使用返回的独立凭据连接 `/webdav/`。
