# 设计来源

[metasauce/beets-flask](https://github.com/metasauce/beets-flask)，参考提交 `00b246d`，MIT。完整许可位于 [licenses/beets-flask-MIT.txt](licenses/beets-flask-MIT.txt)。

| 项目内实现 | 参考范围 |
| --- | --- |
| `frontend/src/upstream/theme.tsx`、`main.css` | 主题、字体、断点、背景网格 |
| `upstream/page.tsx`、`NavBar.tsx` | 页面宽度、导航与响应式布局 |
| `upstream/primitives.tsx` | 跨线图标、标题和卡片布局 |
| 草稿、搜索与播放器 | 目录树、多选、分类浏览和播放控件布局 |
| `GrowingRipple.tsx`、状态图标 | 等待动画及状态呈现 |

项目不运行或嵌入 beets 引擎。默认黑胶封面为项目绘制的 SVG；业务状态、盘生命周期、查询、标签写入和任务执行由本项目实现。

字体、依赖库及 MPL 音源分析代码的来源见[第三方声明](THIRD_PARTY_NOTICES.txt)和[音源分析说明](internal/audioquality/SOURCE.md)。
