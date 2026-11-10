# 音源质量分析来源

位深和带宽检测从 [FlacCompagnon](https://github.com/craft-and-code/FlacCompagnon) 的分析核心移植为 Go，固定参考提交为 `7a382761a2054e07d3572736101e64560ef09057`。原作者为 FlacCompagnon contributors，核心许可为 MPL-2.0。

对应来源为 `core/src/analysis/bitdepth.rs`、`mdct.rs`、`spectrum.rs`、`analyzer.rs` 和 `detections.rs`。移植后的 `bitdepth.go`、`spectrum.go`、`analysis.go` 延续 MPL-2.0；完整许可位于 `licenses/flaccompagnon-MPL-2.0.txt`，可分发源码位于 `licenses/flaccompagnon-source.txt`。

项目适配：

- 全曲逐声道整数位深检测，包含低位补零及受限残差量化网格估计；估计不证明原始录制位深。
- 保留上游 1024 系数正弦窗 MDCT 定义和疑似升频判据：高于 48 kHz、死区窗口占比至少 70%、平均截止低于 Nyquist 的 90% 且不高于 30 kHz、死区低于峰值 75 dB。
- 用 FFT 计算等价 MDCT，避免二次复杂度及大型余弦矩阵；测试与独立直接定义对照。
- 最多 240 个 2048 样本片段分布在全曲范围，选择窗口内能量最高的声道，避免反相抵消；低于 12 个有效窗口不判断升频。无样本总数的 FLAC 先以恒定内存计数。
- 频谱截止仅提供解释数据，不单独据此判定有损转码。没有移植 MP3/AAC 重量化格点检测、播放、响度、编辑、网络或报表系统。
- FLAC 达到 STREAMINFO 声明的总样本数且音频 MD5 校验通过后结束读取，允许完整音频之后存在非音频尾部数据；未声明长度或未提供 MD5 时仍要求正常读到 EOF，避免错误的样本数掩盖后续音频。保留每帧 CRC 校验，并在提供非零 MD5 时按 [RFC 9639](https://www.rfc-editor.org/rfc/rfc9639.html#section-8.2) 校验解码后的全部 PCM；样本不足、帧越界或校验失败不发布部分分析结果。
- 静音、过短音频、不支持的格式及解码失败分别报告。分析不改写音频，不重采样，不改变已有数据库及目录标识。

FLAC 使用 `github.com/mewkiz/flac` 解码，WAV/AIFF 使用受限块遍历的整数 PCM 流式适配器。当前不分析浮点 PCM、有损编码或 ALAC/DSD。移植和解码均直接编入 Go 服务，不使用外部命令或额外运行时。

生产构建的 `frontend/scripts/notices.mjs` 自动生成随程序提供的 `licenses/flaccompagnon-source.txt`；测试核对它与上述 MPL 源码一致。修改分析源码后先运行 `npm --prefix manager-service/frontend run build` 更新内嵌副本。
