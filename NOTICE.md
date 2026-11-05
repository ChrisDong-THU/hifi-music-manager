# 第三方来源与许可

项目自有代码的许可不覆盖下列第三方作品，其原有声明与许可保持有效：

| 范围 | 来源与许可 |
| --- | --- |
| 主题与部分交互设计 | [beets-flask](https://github.com/metasauce/beets-flask)，MIT；[适配说明](manager-service/UPSTREAM.md) |
| 音源分析核心移植 | [FlacCompagnon](https://github.com/craft-and-code/FlacCompagnon)，MPL-2.0；[源码与范围](manager-service/internal/audioquality/SOURCE.md) |
| React、MUI、Emotion、Lucide、字体 | 锁定依赖及完整声明见 [manager-service/licenses/](manager-service/licenses/) |
| Go 音频、存储与平台依赖 | 声明见 [管理服务](manager-service/THIRD_PARTY_NOTICES.txt)与[音乐服务](music-service/THIRD_PARTY_NOTICES.txt) |
| UPnP 分类图标 | Google Material Icons，Apache-2.0；[来源清单](music-service/browsebridge/assets/sources.json) |

构建自动汇总前端许可，并将改编的 MPL 分析源码随管理程序提供。分发二进制时须保留相应声明、许可证与源码义务。

MinimServer、Java 和可选 BubbleUPnP 由使用者按各自许可另行安装，本仓库不包含这些厂商程序或其许可凭据。
