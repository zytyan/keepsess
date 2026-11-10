# xterm.js 5.5.0

The embedded `xterm.js` and `xterm.css` originate from xterm 5.5.0 (MIT; see
`XTERM-LICENSE`). Keepsess carries a targeted DOM rendering patch in `xterm.js`:

In upstream `src/browser/Terminal.ts`, forward `RenderService.onRender` to the
public `Terminal.onRender`, rather than `RenderService.onRenderedViewportChange`.
The latter suppresses redraw-only events, although the DOM renderer still calls
`replaceChildren` on those rows. Cursor, selection and link redraws can therefore
remove keyword text styling without notifying the external color layer.

`RenderService._renderRows` emits `onRender` synchronously after renderer and
selection updates. But selection scrolling also calls `DomRenderer.renderRows`
directly, and link underlining replaces rows directly. Both DOM exits now notify
the core terminal's existing render emitter after their last row replacement.
This covers actual DOM mutations rather than only scheduled RenderService work.
Keepsess's color callback does not request another render; repeated notifications
reuse unchanged row styles. Local echo also receives all actual redraw events.

The public wrapper additionally exposes `registerDOMRenderFilter(callback)`.
One filter per terminal can return false before DOM row replacement, preserving
the previous frame until bounded asynchronous matching is ready. Its disposable
only removes that same registration. Both normal rows and link underline rows
honor the filter. Literal keywords and exact bundled default patterns match
synchronously in this filter, so their uncached thumb jumps commit immediately.
Arbitrary custom regex keeps its Worker deadline; completion requests one new
render, and failure/disable releases the held frame. This hook only gates the
DOM projection; it does not parse input, change buffers or replay commands.

Reapply with `node scripts/patch-xterm.cjs` from the repository root. The script
requires exactly one of each known call site and is idempotent; it fails on an
unfamiliar upstream build. `TestBrowserWheel` exercises redraw-only rendering,
direct selection/link rendering, delayed custom matching, synchronous literal
jumps and actual painted keyword pixels. Recheck these when
upgrading xterm; do not silently copy the substitution into a different version.

Normal Go builds embed the committed asset and require no npm or Node tooling.

Cell metrics use the existing DOM measurement strategy rather than OffscreenCanvas.
The renderer is DOM based: browser font substitution, minimum font sizes and font
loading can otherwise produce different Canvas metrics and visible glyph widths.
The 32-character DOM probe measures in CSS pixels (no extra devicePixelRatio
scaling). This keeps cell spacing on the same font/layout path as the rendered
rows. The patch script includes this substitution and remains idempotent.

## Offline renderer and search addons

Embedded official MIT packages compatible with xterm 5.5.0:
- `@xterm/addon-webgl@0.18.0` (`ADDON-WEBGL-LICENSE`)
- `@xterm/addon-search@0.15.0` (`ADDON-SEARCH-LICENSE`)

Assets are copied from the packages' `lib/` directories. No CDN or npm runtime
is needed. WebGL is preferred; initialization failure or context loss disposes
the renderer and falls back to DOM without replaying output/input. Search is
literal, bounded to the current loaded scrollback and 1000 decorated matches.
Keyword colors use xterm decorations in WebGL and the existing DOM fallback.
