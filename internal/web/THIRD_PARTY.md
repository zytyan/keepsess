The embedded terminal uses @xterm/xterm 5.5.0 (MIT).

Upstream: https://github.com/xtermjs/xterm.js/tree/5.5.0
Package: https://registry.npmjs.org/@xterm/xterm/-/xterm-5.5.0.tgz
Bundled files: assets/vendor/xterm.js, assets/vendor/xterm.css.
The complete license is in assets/vendor/XTERM-LICENSE.

The bundled xterm JavaScript carries the DOM rendering/measurement patch described
in assets/vendor/README.md; it is reapplied with scripts/patch-xterm.cjs. The
stylesheet and license originate upstream. The offline MIT addons
@xterm/addon-webgl 0.18.0 and @xterm/addon-search 0.15.0 are also embedded, with
licenses assets/vendor/ADDON-WEBGL-LICENSE and ADDON-SEARCH-LICENSE. WebGL is
preferred with DOM fallback; search covers loaded scrollback. The application
needs neither a CDN nor a JavaScript build at runtime or Go build time.
The optional Playwright test uses a separately installed Node/Playwright runtime.

The interface and all terminal views also embed these SIL Open Font License 1.1
fonts; they are served from the same local origin, with no CDN or installed font
requirement:

- JetBrains Mono 2.304, variable weight 100–800 (unmodified TTF), from
  https://github.com/JetBrains/JetBrainsMono/tree/v2.304 . License:
  assets/fonts/JetBrainsMono-OFL.txt.
- Noto Sans SC 2.004, variable weight 100–900, from
  https://github.com/notofonts/noto-cjk/tree/Sans2.004/Sans/Variable/TTF/Subset .
  The official NotoSansSC-VF.ttf was compressed to WOFF2 without subsetting or
  changing its character map, font names or weight axes. License/copyright:
  assets/fonts/NotoSansSC-OFL.txt.

See assets/fonts/README.md for exact source URLs and checksums. Python/fontTools
were used only to prepare the checked-in WOFF2; normal Go builds never regenerate
fonts or require those tools.
