# Bundled typefaces

These unmodified font files are loaded locally by `../typography.css`. Camp does not request a font CDN or depend on locally installed classic Apple fonts. The licenses are also retained as a CSS license comment in production builds.

| Files | Author and upstream source | License |
| --- | --- | --- |
| `ChicagoKare-Regular.woff2` | Duane King, [Chicago Kare](https://github.com/KingDuane/Chicago-Kare/tree/dca7a9e4f2b971e39cf3a5e8a3f9309d4293d56d) | [MIT](ChicagoKare-LICENSE.txt) |
| `Inter-Regular.woff2`, `Inter-SemiBold.woff2` | The Inter Project Authors, [Inter 4.1](https://github.com/rsms/inter/tree/v4.1/docs/font-files) | [SIL Open Font License 1.1](Inter-LICENSE.txt) |

Chicago Kare is a community recreation used for compact interface chrome. It is not Apple's Charcoal font or a claim of pixel-identical Mac OS 9 typography. Inter is the readable text face for documents, descriptions, small status text and fields. The single Chicago Kare face is mapped to the UI's normal and bold weights to avoid synthetic bold outlines. Its CSS size adjustment aligns its small cap height with the compact 12-pixel UI without modifying the redistributed font binary.

Neither family covers Chinese. The shared stack explicitly falls back to PingFang SC on macOS, Microsoft YaHei on Windows, and Noto Sans CJK SC where available, followed by the browser's sans-serif fallback. CJK glyphs can therefore differ by host; Latin UI lettering does not depend on the host font installation.
