# Yoru Sakura 字体目录

本目录存放 Yoru Sakura 设计系统自托管字体（woff2）。由下载脚本自动获取，部分字体因网络限制需手动补全。

## 字体状态

| 字体 | 文件 | 状态 | 说明 |
| --- | --- | --- | --- |
| JetBrains Mono | JetBrainsMono-Regular.woff2 | ✅ 已就位 (31 KB) | Google Fonts，latin 子集 |
| JetBrains Mono | JetBrainsMono-Medium.woff2  | ✅ 已就位 (31 KB) | Google Fonts，latin 子集（可变字体，weight 通过 font-weight 区分） |
| Noto Sans SC | NotoSansSC-Regular.woff2 | ✅ 已就位 (76 KB) | Google Fonts，CJK 最大子集切片 |
| Noto Sans SC | NotoSansSC-Medium.woff2  | ✅ 已就位 (76 KB) | 同上（可变字体） |
| Noto Sans SC | NotoSansSC-Bold.woff2    | ✅ 已就位 (76 KB) | 同上（可变字体） |
| Cabinet Grotesk | CabinetGrotesk-Medium.woff2 | ❌ 缺失 | Fontshare CDN 不可达，需手动放置 |
| Cabinet Grotesk | CabinetGrotesk-Bold.woff2   | ❌ 缺失 | Fontshare CDN 不可达，需手动放置 |

## 关于 Noto Sans SC（重要）

Google Fonts 对 CJK 字体按 `unicode-range` 切分为约 101 个子集。当前下载的是体积最大的一个子集（约 76 KB），可能仅覆盖主 CJK 表意文字范围的一部分，并非完整字体。

- 当前方案：`fonts.css` 为每个 weight 声明单一 `@font-face` 指向 `NotoSansSC-<weight>.woff2`。未覆盖到的字符会回退到系统字体（system-ui），不影响可用性。
- 如需完整中文覆盖：建议从 [Google Fonts Noto Sans SC](https://fonts.google.com/noto/specimen/Noto+Sans+SC) 或 [noto-cjk GitHub](https://github.com/notofonts/noto-cjk) 获取完整字体替换本目录文件；或将所有子集 woff2 保留并改造 `fonts.css` 为多 `@font-face` + `unicode-range` 声明。

## 手动放置 Cabinet Grotesk

Cabinet Grotesk 由 Fontshare 提供（https://www.fontshare.com/fonts/cabinet-grotesk）。当前环境无法访问 `cdn.fontshare.com`，请手动获取后放置：

1. 访问 https://www.fontshare.com/fonts/cabinet-grotesk 下载字体包。
2. 或通过 Fontshare CSS API 获取 woff2 直链：
   - CSS: `https://api.fontshare.com/v2/css?f[]=cabinet-grotesk@500,700&display=swap`
   - 在浏览器（Chrome UA）中打开上述链接，解析其中 `url('//cdn.fontshare.com/.../....woff2')` 即为字体文件地址（协议相对，需补 `https:`）。
3. 将以下两个字重重命名后放入本目录：
   - `CabinetGrotesk-Medium.woff2`（字重 500）
   - `CabinetGrotesk-Bold.woff2`（字重 700）

`fonts.css` 已预先声明对应 `@font-face`，文件就位后无需改动 CSS。在文件缺失期间，标题文本会回退到 `'Noto Sans SC'` → `system-ui`，应用可用性不受影响（`font-display: swap` 保证）。
