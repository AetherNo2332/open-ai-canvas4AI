# 首页字体

首页英文使用 Mona Sans，中文使用思源黑体（Source Han Sans 简体中文子集），字体文件由本站提供。

- `mona-sans-variable.woff2`：GitHub 官方 Mona Sans，`fonts/webfonts/variable/MonaSansVF[opsz,wght].woff2`，字重 200–900。
  来源：https://github.com/github/mona-sans
  许可证：`MonaSans-OFL.txt`。
- `source-han-sans-cn-variable.woff2`：Adobe 官方 Source Han Sans，`release/Variable/WOFF2/TTF/Subset/SourceHanSansCN-VF.ttf.woff2`，字重 250–900。
  来源：https://github.com/adobe-fonts/source-han-sans
  许可证：`SourceHanSans-OFL.txt`。

CSS 中将 Mona Sans 放在思源黑体之前，使英文与数字使用 Mona Sans，中文及中文标点由思源黑体补齐。两者都使用 `font-display: swap`，不阻塞页面文字展示。
