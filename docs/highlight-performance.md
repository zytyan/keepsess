# 网页高亮引擎评估

本文记录指定日期、构建和环境下的性能评估及阶段实现，数值与限制不作为当前接口规范；现行 Web 行为见 [使用参考](usage.md#人类-web-终端与持久历史) 和 [内嵌资源说明](../internal/web/assets/vendor/README.md)。未在本次文档校准中重新测量。

2026-11-09，Linux x86_64、无头 Chromium 158.0.8127.18，同一浏览器 Worker 比较原生 `RegExp` 和官方 npm `re2-wasm@1.0.2`。结论：当前默认高亮优先采用原生 JavaScript 正则。

输入为交换机日志、接口状态、IPv4/MAC、中文描述、普通日志与无关键词文本，共 200 行短文本；使用本项目 7 条默认规则。引擎预热后各取 5 次中位数，每次处理 10 批。匹配阶段复用已编译规则；两种引擎的匹配计数相同。管线比较包括同一 Worker 消息往返、规则匹配、重叠优先级和结果构造，不含 xterm 装饰绘制。

| 默认场景 | 原生 JS | re2-wasm | 原生 JS 相对速度 |
| --- | ---: | ---: | ---: |
| 14,000 次规则/短行扫描 | 16.9ms | 150.1ms | 8.9 倍 |
| 2,000 行完整 Worker 管线 | 48.3ms | 245.2ms | 5.1 倍 |

重复构造 350 个规则的测量分别约 0.1ms / 389.8ms，但 JS 引擎有编译缓存，因此此项不代表完全冷启动，不作为主要决策依据。以上也不包含 WASM 下载/初始化；实际产品统一复用规则，默认场景更应看热匹配和完整管线。

这是本机、小样本的实测，不代表所有浏览器或规则。这里只据正常默认负载选择，未把恶意或病态规则作为性能结论依据。保留 Worker 与扫描/输出上限是为了维持页面响应与资源边界。

## 高速滚轮与主线程重绘

2026-11-09，使用打包字体、密集模拟日志、Chromium CPU 采样和长动画帧观察排查。主线程热点包括 xterm DOM renderer 的 `createRow`、`replaceChildren`、装饰节点注册/释放和浏览器布局；Worker 正则匹配不是这部分主线程工作的替代品。关闭高亮后，大视口 DOM 绘制本身仍有成本，因此不能把所有长帧归因于规则匹配。

旧调度器在首次变化后启动固定 80ms 定时器，后续滚轮输入不会把它延后。持续滚动时仍会扫描、应用装饰，再在下一次视口渲染回调中清理。清理与再次应用会触发额外重绘，密集命中还会创建大量 marker 和 DOM 节点；旧功能测试只核对滚动后恢复高亮，没有约束滚动过程中重复重建。

1b7cb95 的方案在连续滚动期间只清理一次，最后一次输入和视口变化后静默 140ms 再扫描。这减少了装饰反复重建，但用户仍报告瞬时卡顿和高亮延迟；140ms 并非全部根因。该路径保留为不支持原生文本高亮时的兼容回退。

专项浏览器回归使用 35 行、350 处命中的模拟视口，向 xterm 的实际 wheel listener 每 12ms 发送一次输入，共 40 次。同步高亮路径要求滚动期间继续更新颜色，不注册 xterm 装饰。刻意将 Worker 回复延迟 40ms，在每次 xterm 渲染回调检查首行已着色；截图像素检查确认实际绘制，另验证可信浏览器滚轮输入、原生滚动条拖动、ANSI 优先级与显式覆盖、中文/emoji/组合字符、换行、复制原文、总览和资源释放。不用依赖机器负载的绝对帧时长作为断言，也不承诺所有硬件上的固定帧率。

复现：设置 `KEEPSESS_BROWSER_TEST=1`、可用的 `KEEPSESS_NODE` 与 `PLAYWRIGHT_MODULE`，运行 `go test ./internal/web -run 'TestBrowser(Wheel|Appearance)$' -count=1 -v`。测试只使用本机模拟器，不连接真实网络设备。

## Windows 有界面 Edge trace

2026-11-09，在应用实验补丁前采集基线，再依次采集可变 Range 原型和 StaticRange 版本。Edge 157.0.4341.42、Windows amd64、RTX 4060 Ti，DevTools `SystemInfo.getInfo` 确认 GPU compositing/rasterization 开启、ANGLE D3D11 使用 NVIDIA；未强制软件渲染。Node 24.16.0、Playwright 1.65.0。页面使用打包字体，字体加载后测试首次滚轮和重复滚轮；这不是未加载字体的冷启动测试。

对照页 1500×1000 CSS 像素、160×45 字符、14px 字体、5000 行 scrollback，每行 12 个 ERROR。每轮 45 次浏览器滚轮输入，间隔至少 8ms（自动化调用本身也耗时），随后等待 600ms。保存的 DevTools trace 含 CPU 采样、布局、绘制和输入时间标记；不是 Playwright 操作回放文件。

| 关键词高亮实现 | 首轮/重复轮最大 RAF 间隔 | 重复轮 FunctionCall 峰值 | 重复轮 Layout 峰值 | 停稳后的文字 span |
| --- | --- | --- | --- | --- |
| 已推送装饰基线 | 33.3 / 33.4ms | 12.2ms | 13.0ms | 3240 |
| 未修正的可变 Range 原型 | 133.5 / 150.2ms | 86.2ms | — | 45 |
| StaticRange（仍被手动验收否决） | 16.9 / 16.9ms | 2.3ms | 1.2ms | 45 |

同次无高亮对照约 16.9–33.3ms，ANSI 对照约 16.9–17.3ms。各轮并非同一时刻，最大 RAF 间隔易受环境影响，只作本机诊断证据。基线滚动期间清空高亮，布局/重绘峰值主要集中在开始清理和停止后重新应用；可变 Range 原型的 CPU 热点是 `replaceChildren`，说明只减少 DOM 节点还不够。

CSS Custom Highlight 的 [规范](https://drafts.csswg.org/css-highlight-api-1/)允许 `AbstractRange`，包括不可变的 `StaticRange`；可变 `Range` 会随着 DOM 修改更新边界，大量存活范围使 xterm 替换子节点的成本显著增大。StaticRange 消除了该峰值，但用户在本机手动滚动仍观察到白色闪帧。随后增加有界预取再手动录制：222 条滚轮/渲染诊断记录中，没有缓存覆盖缺失的渲染帧，也没有零高亮范围的渲染帧，仍有视觉延迟。因此“范围已注册”不能作为当前帧已染色的验收证据；最终产品没有采用 CSS Custom Highlight 或 Range 方案。

仅在原有公开 onRender 回调补色仍不够。连续画面曾采到 50 帧中 25 帧白字，调用栈定位到滚动之后 SelectionService 又直接调用 DomRenderer.handleSelectionChanged/renderRows；另外 redraw-only 和链接下划线也存在绕过原通知的路径。vendored 补丁在 DOM renderer 实际替换行的出口同步通知补色，并覆盖 RenderService 的全部重绘。回归直接调用选择区/链接路径，不再只验证公开滚动事件。修补后同一自动滚轮场景的 29 个采样画面均保持红色。

最终路径在 xterm 的同一 render 回调内同步包装命中的文字节点并设置颜色。保留 xterm 原有 ANSI/宽字符 span，只为命中文本添加一个简单 span；不创建每字符装饰或独立装饰 div，也不修改 buffer、复制原文或应用输入。不变的行与着色区间直接复用；xterm 替换行内容后重新绑定。失效或销毁时解包并清理引用。补丁的精确可重放替换脚本为 `scripts/patch-xterm.cjs`，说明见 `internal/web/assets/vendor/README.md`。

纯字面关键词和精确等同于内置默认表达式的规则使用同步匹配，在 DOM 提交前准备当前视口，不预读历史；内置表达式清单来自服务端固定的 `settings.DefaultAppearance()`，不是用户保存的配置。同步与 Worker 复用同一 matcher，保持 Unicode、优先级、0 errors 和匹配上限语义。两条路径都限制为最多 200 行、32768 字符、1024 个着色片段。任意自定义正则继续放在带截止时间的 Worker，预取上下各一屏，优先可见区，再按滚动方向与距离分配预算。

拖动到未匹配区域时，新 DOM 提交过滤器可暂留上一帧，结果就绪后一次提交带颜色的新视口，错误/禁用则释放等待。这消除了白色中间帧（一次滚动条录像 60 帧均为有色首行），但仅靠 Worker 等待的版本被用户反馈跟随太慢；因此增加了上述同步快路径，用户随后确认滚轮、拖动均不闪白且跟随可接受。复杂自定义规则仍可能有一次有界匹配等待。不扫描全部历史，预取缓存只存行号和着色区间；写入/缩放/规则变化使旧结果失效，配置/不可见/备用屏幕/销毁清理着色和缓存。非 DOM renderer 保留旧装饰回退。

真实 Web/SSH 模拟器页面补充 trace 使用 1500×1000、100×30 字符、每行 8 个 ERROR，含停顿和原生滚动条拖动。早期同步 DOM 版本首次/重复轮最大 RAF 间隔为 16.9/16.9ms，停稳文字 span 270 个；同轮无高亮为 50.0/17.4ms，ANSI 为 16.9/17.5ms。这组尚未包含最终渲染提交/同步匹配补丁，只作阶段对照。参数与上表不同，不直接计算跨表性能提升。另一次有后台节流（ANSI 约 1s）的采样不用于帧率结论。

最终前台采样（同一 Web fixture 参数，各阶段检查页面 visible）：关键词首次/重复轮最大 RAF 间隔 17.1/17.1ms，全部 7 条内置规则 17.4/17.3ms；ANSI 对照 17.6/17.0ms，无高亮 50.0/22.8ms。关键词和默认规则视口均 270 个 span。该结果结合用户在本机 125% 缩放下对滚轮、滚动条拖动的最终确认，只说明本机该负载满足流畅与不闪白的目标，不承诺任意硬件/自定义正则固定 60fps。

测试的超长缓冲场景显式设置 DPR=1，并使用两个连续滚轮事件。排查中观察到约 1.000000015 的比例值与约 22.9999997px 行高，xterm 5.5 在百余万像素滚动位置可能忽略首个原生滚动事件；兼容装饰路径也观察到同样现象。这不表示已修复 xterm 的所有系统缩放问题；手动对照页保留系统实际缩放（本机 125%）。

真实 Web 页面也提供可选采样入口（由本机 SSH 模拟器驱动，不访问实际设备）：

```powershell
$env:GOTOOLCHAIN='go1.22.1'
$env:CGO_ENABLED='0'
$env:KEEPSESS_BROWSER_TEST='1'
$env:KEEPSESS_BROWSER_CHANNEL='msedge'
$env:KEEPSESS_BROWSER_HEADED='1'
$env:PLAYWRIGHT_MODULE='C:/path/to/playwright'
$env:KEEPSESS_PERF_DIR='C:/path/to/local-traces'
go test ./internal/web -run '^TestBrowserPerformance$' -count=1 -v
```

该入口记录首次/重复滚轮、停顿与原生滚动条拖动，并保存环境与帧间隔。可设置 `KEEPSESS_PERF_APPEARANCE` 为本机保存的基线 appearance.js 做同参数对照；无需将旧实现并入产品。普通回归不设置 `KEEPSESS_PERF_DIR`，不会采样或输出 trace。运行全套浏览器回归可使用 `-run '^TestBrowser'`，兼容回退由 `TestBrowserAppearanceFallback` 单独覆盖。采样时不要同时编译或运行 race。

`KEEPSESS_PERF_SCENARIOS` 可指定 `off,ansi,keyword,builtin` 的子集，默认全部采集。性能入口要求有界面、可见页面，原始 trace 与 PNG 连续画面只保留在本地忽略目录，禁止把真实终端历史、令牌或本机配置带入提交。

最终验证使用 Go 1.22.1：Windows 有界面 Edge 的全部 Web 浏览器验收及共享 daemon 多实例/重启/归档验收通过；Debian WSL `go test ./...`、`go test -race ./...` 通过；CGO_ENABLED=0 的 linux/amd64、linux/arm64、windows/amd64 构建通过。Windows 原生全量仍有 TCP 满缓冲、Unix Bash 路径及 0600 权限断言失败，已在未修改的 1b7cb95 基线复现，未扩展本轮 Web 修复去改动协议或权限实现。
