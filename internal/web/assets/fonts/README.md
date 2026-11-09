# Embedded sans-serif fonts

Latin glyphs use JetBrains Mono; Chinese glyphs fall back to Noto Sans SC. Both
are variable sans-serif fonts, including the normal/bold weights used by xterm.
The final generic fallback is `sans-serif` for every visible text surface.

## JetBrains Mono 2.304

Source (unmodified):
https://raw.githubusercontent.com/JetBrains/JetBrainsMono/v2.304/fonts/variable/JetBrainsMono%5Bwght%5D.ttf

Bundled file: JetBrainsMono-Variable.ttf
SHA256: 662a196d58f1183bf2d77428b6d5283fe3f45161ab021bea4036bc98e5cac016
Weight axis: 100–800. License: JetBrainsMono-OFL.txt.

## Noto Sans SC 2.004

Source:
https://raw.githubusercontent.com/notofonts/noto-cjk/Sans2.004/Sans/Variable/TTF/Subset/NotoSansSC-VF.ttf

Source SHA256: d68bafcb48a2707749396aa12bbbd833cb70401f3a9a689fd2902c7e0d295964
Bundled file: NotoSansSC-Variable.woff2
Bundled SHA256: 2057fd5e15729286c5585a2d36b799c010f9e1cf9ad3604dd238cc0feca6cacf
Weight axis: 100–900. License: NotoSansSC-OFL.txt.

Only the format was compressed to WOFF2, using fontTools 4.55.3 with Brotli 1.1.0.
The resulting character map was checked against the original font. The original
font names and copyright/license metadata remain embedded in the font.

The WOFF2 is already checked in. Building or running keepsess needs no Python,
font compiler, npm, CDN, or system font installation.
