# keepsess

面向 LLM 的持久 SSH exec / SSH、Telnet 双向流 MCP 工具。纯 Go，固定 Go 1.22.1 验证，发布构建 `CGO_ENABLED=0`，支持 linux/amd64、linux/arm64、windows/amd64。

项目、Go module、MCP 注册名和 skill 名称统一为 **keepsess**，仓库地址为 [zytyan/keepsess](https://github.com/zytyan/keepsess)。工具名继续使用 `sess_*`，句柄继续使用 `s1`、`s2`；安装器直接写入 keepsess 配置，不迁移旧名称。

## Python 自动化 SDK

配套 [Python 库与完整用法](../python/README.md) 复用同一 Go 引擎，提供 30 个 MCP 同名方法、dataclass 点语法结果及同步/异步接口。脚本独立建连，默认加入现有人类 Web 界面。

```sh
python -m pip install ./python
# 或免安装：export PYTHONPATH="$PWD/python${PYTHONPATH:+:$PYTHONPATH}"
# 未将 keepsess 加入 PATH 时：export KEEPSESS_BINARY=/path/to/keepsess
```

## 启动

```sh
keepsess help
keepsess serve
keepsess serve --log /private/path/audit.jsonl
keepsess version
keepsess mcp-skill
keepsess install-skill --dir ./keepsess-skill
```

无子命令只显示帮助；stdout 专供 MCP。配置示例：

```json
{"command":"/path/to/keepsess","args":["serve"]}
```

0.3 API 保留完整 30 个工具（含 sess_help、sess_rename、sess_task_rename），`tools/list` 统一展示全部 30 个工具及完整参数说明。CLI version 与 MCP initialize 暴露相同的版本和 Git 构建标识。**这是接口迁移**：旧 sess_interact、sess_read、address/user/key_file、wait/waitfor/timeout、input_id 等参数不再使用；重新获取 tools/list 并安装新版 skill。

## 人类 Web 终端与持久历史

`keepsess serve` 默认同时启动离线打包的 xterm.js 界面，监听 `127.0.0.1:8765`。

网页“本地回显”仅以半透明文字预览输入，不进入终端输出缓冲、复制内容或输出历史。当前终端收到任意非空远程输出字节时，清除全部预览，再显示远程原文，不匹配内容；无关日志、控制序列或分包字符也会清除预览。关闭回显、锁定、切换、断连或发送失败时也会清除。预览不改变发送内容，不自动重试；开启时密码输入同样可见。

界面、单任务终端和会话总览统一使用 JetBrains Mono（英文）与 Noto Sans SC（中文），均为非衬线字体；字体内嵌，无需 CDN 或本机安装。

网页使用统一高对比主题，增强当前 session/task 的选中边框、辅助文字和 ANSI 调色板。右上角“显示与高亮”可启停高亮、编辑规则、预览、调整优先级或恢复默认。默认高亮错误、警告、明确上下文中的链路状态、IPv4、MAC 和接口名；颜色仅辅助阅读，不代表执行/认证成功。

自定义支持关键词或浏览器原生 JavaScript Unicode 正则、大小写、前景/背景色及覆盖远端 ANSI 颜色。规则从上到下优先，默认保留远端已有颜色。匹配终端解析后的可见文字，支持终端折行，不跨实际换行；备用屏幕（例如全屏程序）不应用高亮。原始历史、复制内容和 MCP 输出不变。每终端扫描最多 200 可见行、64KiB UTF-16 文本，每逻辑行片段最多 8192 UTF-16 单元，每视图最多 1024 个匹配/装饰；屏外历史在滚动进入可见区后重算；总览中整个离屏的终端卡片暂停高亮并释放装饰，进入可见区再恢复。默认规则、自定义规则与关键词匹配统一使用浏览器原生正则，规则编译后复用。所有终端共用一个 Worker，队列最多 32 项，每终端最多一个待处理扫描。引擎启动时限 10 秒、计算时限 1 秒，超时暂停高亮，修改规则或重新应用后恢复。不引入 WASM、CDN 或新 Go 依赖。后端校验配置结构和容量，浏览器通过同一匹配引擎校验表达式语法。

点击“应用预览”临时应用当前编辑，关闭窗口撤销未保存预览。“保存到本机配置”将规则写入 `~/.keepsess.toml` 的 `[web.appearance]` / `[[web.appearance.rules]]`，立即生效，其他浏览器约 10 秒内读取，无需重启 daemon。仅显示配置被修改，其他配置值和注释保留；内联或特殊格式无法安全保留时拒绝保存，可改为标准表格格式后再试。规则最多 32 条，表达式最多 512 字节；保存接口只接受通过现有 Host/Origin 检查的同源 JSON 请求。

端口占用时依次尝试后续 100 个端口，再由系统分配。实际入口写入 stderr，
模型可以从 `initialize._meta.web` 或 `sess_list` 的 `web` 查询 URL 和历史记录状态；stdout 仍只输出 MCP。

页面默认只读；手动解锁后可向 stream / interactive exec 输入，或在 exec 会话启动命令。
刷新、切换终端、WS 重连恢复只读，解锁不覆盖会话 `read_only`。
左侧列出稳定 `sN` 和当前别名，保留已关闭会话；exec 任务放在会话旁的二级侧栏，按创建顺序显示 `taskN`、别名、状态和命令摘要，独立展示输出。
停在终端尾部时自动跟随新输出，向上滚动后保持阅读位置；只有停在最新任务尾部才自动切换到新任务，查看旧任务或历史内容时保持当前选择。
自动切换仍恢复只读；尚有输入待发送或操作等待回执时不抢占终端。
非交互命令前显示 `[s1 task2 /srv/app] $ command`，路径来自请求的 `cwd`；未指定时登录模式显示 `~`，
非登录模式显示“默认目录（未探测）”，不额外执行目录探测。
WS 实时更新和断线历史补齐不消耗 MCP 输出游标，不重放任何输入。
二级侧栏的“会话总览 · 多命令同屏”可把多条命令与各自输出纵向排列，短输出可同时查看。
总览始终只读，每页最多 20 条；长输出最多展开 12 行并保留独立滚动，前端滚动历史预算按 20 个预览分配，不影响磁盘历史。
每条命令的解码、ANSI 状态和退出状态独立，不混排并发输出；点击“打开任务”回到原单任务终端。
总览尾部跟随新输出与新任务，阅读较早内容时保留位置；“更早 / 较新 / 最新”切换历史页。PTY 任务提供单任务查看入口。
前端滚动历史默认 **5000 行**，可在页面调整为 100..50000 行并保存在浏览器中；磁盘历史独立保留。

每个用户只运行一个共享 Web daemon；首个 `keepsess serve` 自动启动，后续进程主动连接同一入口，不各自监听端口。
daemon 空闲时继续运行，页面列出本用户所有活动实例及 PID，也保留退出实例的归档。
SSH/Telnet 和任务仍在各自 Agent 进程里；daemon 重启后自动注册和补齐观察，不恢复已退出进程的连接，也不重发命令/输入。
本机通信使用随机令牌认证的 WebSocket，令牌只保存在 `~/.keepsess/daemon.json`（0600）；浏览器不持有该令牌。

配置统一从 **`~/.keepsess.toml`** 读取，首次启动生成默认文件（0600）。Agent 命令固定为 `keepsess serve`；升级后重启 MCP 进程即可加入共享入口并发现全部 30 个工具：

```toml
# 可选：AI 输入审计；历史包含终端原始输入，密码也可能被保留。
# audit_log = "~/keepsess-audit.jsonl"

[web]
enabled = true
listen = "127.0.0.1:8765" # 端口占用时自动重试，0 表示系统分配。
base_path = "/"         # 保留反代前缀时设为 /keepsess/；剥离前缀时保持 /。
public_url = ""         # IP/localhost 无需填写；DNS 入口填 https://host/keepsess/。

[history]
dir = ""               # 默认 ~/.keepsess/history；检测到旧默认目录则继续使用。
max_bytes = 0          # 容量保护；0 不限，不自动删除。
```

`web.enabled=false` 禁用本进程的 Web 注册与持久历史；已运行的共享 daemon 继续供其他进程使用。
除可即时生效的 `web.appearance` 外，Web/history 设置由 daemon 启动时读取，修改后执行 `keepsess daemon stop`；活动 Agent 会重新启动并注册，也可由下一次 `serve` 启动。
改变 `history.dir` 后，已有 Agent 仍持有旧目录，需重启这些 Agent 才能迁移；不会移动或删除原有文件。
`audit_log` 在每个 Agent 启动时读取。旧配置中的 `[tools] profile` 仅兼容读取并忽略，无需手动修改即可升级。旧 Web/history 命令行参数已移除；`--log` 仍作为审计兼容覆盖。

```sh
keepsess serve          # 启动 MCP，自动加入共享入口
keepsess daemon status  # 查询实际 URL/状态，不输出内部令牌
keepsess daemon stop    # 停止当前 daemon；活动 Agent 可自动拉起并重连
keepsess daemon run     # 前台运行，用于诊断；日志默认 ~/.keepsess/daemon.log
```

实例首次连接成功会提示 AI 调用 `sess_configure({"instance_name":"当前任务名称"})`，不用传 `session`。
实例名允许 1..64 个可见 Unicode 字符，显示在活动/归档列表；不改变稳定 `instance_id`，不同实例可重名。
全部 30 个工具统一通过 tools/list 可发现，每个工具携带完整参数说明，无需切换发现分组。
`sess_send_escaped` 只解码一层转义，不追加行尾；需要 CR/LF 时在 data 中明确指定。`sess_sendline_escaped` 解码后追加一次配置的行尾。
`sess_help` 提供可选的流程参考；配置查询可用 `sess_configure({"session":"s1"})` 或 `sess_get_config`。

历史写入 `<history.dir>/<instance_id>/`，目录/文件在支持的平台使用 0700/0600；会话关闭、任务淘汰不删除历史。
日志保存命令/cwd、原始终端输入和输出，**终端中输入的密码也会保存**；不写入连接认证参数、私钥或结构化 env 字段。
历史与 AI 审计相互独立。历史不自动删除，需手动清理不再使用的实例目录；不要发布历史、内部令牌或本地配置。
异步写盘约每秒同步一次，异常退出可能丢失尚未同步的尾部记录；归档目录索引从完整日志重建。
实时输出使用二进制 WS 帧，连续小输出最多合并 20ms / 64KiB，并协商压缩；低频状态与回执保留 JSON。
历史使用压缩二进制快照，按所选任务过滤输出，批量写入 xterm 后一次展示；大归档会内部续取有界快照，不逐条动画回放。
字体文件带内容指纹并使用长缓存，程序与状态接口保持更新。记录队列、浏览器队列和内存滚动历史均有界，慢消费者断连后从历史补齐，不重发输入。

页面资源、API 和 WS 均使用浏览器当前地址的相对路径，访问入口应以 `/` 结尾。
IP（IPv4/IPv6）或 localhost 入口支持不同的外部端口，不必配置 `web.public_url`；HTTPS 在反代终止、后端走 HTTP 也支持。
反代需转发 WebSocket Upgrade，并原样保留浏览器的 **Host（包括端口）**；Nginx 可用 `proxy_set_header Host $http_host;`，避免 `$host` 丢失非默认端口。
WS Origin 与浏览器可见的主机/端口匹配，HTTPS 页面自动使用 WSS；不会盲信 `Forwarded` / `X-Forwarded-*` 改写访问地址。
DNS 域名仍需通过 `web.public_url` 显式声明；代理改写 Host 时也可用该参数声明外部 Origin。此外该参数用于让 MCP 展示外部入口；未配置时 MCP 返回实际本机监听 URL。
本机入口免登录；对外监听通过 `web.listen` 显式设置，对外认证由反代负责。
服务校验 Host/Origin，Web 只读开关用于防误输入，不代替身份认证。
容量保护按启动时现有文件加本实例新增日志计算，多进程共用目录时不作为严格的全目录磁盘配额。
Web 启动失败不阻止 MCP 启动，`web.error` 会报告原因。

可选浏览器验收（需本地 Node.js、Playwright 和 Chromium，不影响正常 Go 构建）：

```sh
KEEPSESS_BROWSER_TEST=1 go test ./internal/web -run 'TestBrowser' -v
# 可用 KEEPSESS_NODE 指定 node 路径，PLAYWRIGHT_MODULE 指定已安装的 playwright 模块路径。
```

## 一键安装到客户端

MCP 标准规定 JSON-RPC、stdio、tools/list/tools/call 和 JSON Schema 工具描述；全部 30 个工具由客户端连接后按需发现，不需要逐个安装或切换发现分组。但 MCP 配置文件和 skill 发现目录由客户端决定，因此安装器按客户端适配。

```sh
# 默认：当前项目的 Claude Code .mcp.json
keepsess install

# 可指定目标 MCP 配置文件，自动识别常见路径或格式
keepsess install .cursor/mcp.json
keepsess install .vscode/mcp.json
keepsess install .codex/config.toml

# 显式选择客户端；省略文件时使用下表默认路径
keepsess install --client cursor
keepsess install --client vscode
keepsess install --client codex
keepsess install /path/to/custom.json --client claude

# 只预览；不创建目录或文件
keepsess install --client codex --dry-run

# 自定义二进制管理目录和 skill 目录
keepsess install /path/to/config.json --client json --dir /private/keepsess --skill-dir /path/to/skills/keepsess
```

`[FILE]` 指目标客户端配置，不是待安装的可执行文件。安装器会：

1. 复制当前运行的 keepsess 二进制到项目 `.keepsess/bin/<内容哈希>/`，配置使用该文件的绝对路径和 `serve` 参数；之后可移动或删除原下载文件。新版本放到新目录，不覆盖仍在运行的 Windows exe。
2. 合并名为 keepsess 的服务项，保留其他服务、字段及注释；已识别的 keepsess serve 项只更新程序路径，保留自定义 args（包括审计）、env、启停和授权设置。Codex 新建项设置 tool_timeout_sec=120，以容纳服务端 115s 硬上限；已有设置不更改。
3. 安装二进制内嵌的完整 SKILL.md；改动已有配置或 skill 前生成同目录、0600 权限的 `.keepsess-backup-*` 备份。相同版本重复安装不改内容，也不反复备份。
4. 先校验、后落盘，配置最后写入；失败则回滚已经修改的文件。现有同名服务不是 keepsess serve 时拒绝覆盖。配置锁避免同时安装覆盖，规划后检测到文件被其他程序改动会拒绝写入。

| `--client` | 默认配置文件 | 配置结构 | 默认项目 skill |
|---|---|---|---|
| claude | `.mcp.json` | `mcpServers.keepsess` | `.claude/skills/keepsess/SKILL.md` |
| cursor | `.cursor/mcp.json` | `mcpServers.keepsess` | `.cursor/skills/keepsess/SKILL.md` |
| vscode | `.vscode/mcp.json` | `servers.keepsess`，type=stdio | `.github/skills/keepsess/SKILL.md` |
| codex | `.codex/config.toml` | `[mcp_servers.keepsess]` | `.agents/skills/keepsess/SKILL.md` |
| json | `.mcp.json` | 通用 `mcpServers.keepsess` | `.agents/skills/keepsess/SKILL.md`，客户端需支持此发现目录 |

默认作用域是项目，不扫描或修改所有客户端的全局配置。指定标准 `.cursor/.codex/.vscode` 目录内的文件时，会以其上一级作为项目根；其他自定义位置以配置所在目录为根。要安装到全局配置或自定义 profile，可指定完整路径，并用 `--dir/--skill-dir` 明确管理和 skill 位置；不会修改 PATH。新建或更新配置文件使用 0600，二进制使用 0755，skill 使用 0644。

JSON/JSONC 合并保留注释、尾逗号与其他字段；TOML 对已识别服务只替换 command 字符串，其余字节保持不变。不能无损扩展的特殊 TOML 布局（如已有 inline mcp_servers 表但尚无 keepsess）会在写入前报错，需先转为普通表。安装目标为目录、符号链接、非法配置或重复 JSON key 时拒绝写入。异常退出遗留 `.keepsess-install.lock` 时，应确认没有安装进程后再处理该锁文件。

安装不会启动后台服务、替你连接设备、修改客户端权限或自动批准工具。重新加载客户端 MCP 配置，并完成客户端自己的项目信任/授权；旧连接属于旧进程，不会迁移。文件落盘和 stdio 握手已验证，不等于验证了每种客户端 GUI 的信任流程。生成的配置包含机器本地绝对路径，不适合直接作为跨机器配置提交；备份可能包含其他服务的秘密，不要发布。

格式参考：[MCP 工具标准](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)、[Claude Code MCP](https://code.claude.com/docs/en/mcp)、[Cursor MCP](https://cursor.com/docs/mcp)、[VS Code MCP](https://code.visualstudio.com/docs/agents/reference/mcp-configuration)、[Codex MCP](https://developers.openai.com/codex/mcp)。Skill 目录参考：[Claude](https://code.claude.com/docs/en/skills)、[Cursor](https://cursor.com/help/customization/skills)、[VS Code](https://code.visualstudio.com/docs/agent-customization/agent-skills)、[Codex](https://learn.chatgpt.com/docs/build-skills)。

## 工具

发送工具默认发送后等待并收集未读输出；`collect_output=false` 时仅发送。`sess_read_history` 是有输出预算的保留历史读取，并非一次返回全部输出。工具名已改为 `sess_send_text`、`sess_sendline_text`、`sess_read_history`；旧工具名不再注册，更新后需刷新客户端 tools/list。

| 旧名 | 新名 |
|---|---|
| sess_send_raw | sess_send_text |
| sess_sendline_raw | sess_sendline_text |
| sess_read_all | sess_read_history |

| 工具 | 参数与用途 |
|---|---|
| sess_help | 无参数列目录；topic 查询工具/主题，topic+parameter 查询单个参数，query 搜索短摘要；limit 默认 3、最大 5 |
| sess_connect | 必填 protocol、host、port；SSH 还必填 username，password/keyfile 至少一个（可同时提供）；可选 SSH 主机校验、ssh_jumps、mode 和顶层配置 |
| sess_rename | session、name；更新会话别名，稳定 sN ID 保持有效，不重建连接 |
| sess_task_rename | handle、name；更新任务别名，稳定 taskN ID 保持有效，不消费输出 |
| sess_reconnect | session；显式重建连接，复用原参数与当前配置、保留句柄、丢弃旧输出，不重放输入 |
| sess_configure | session、顶层配置字段；原子更新出现的字段，其余不变 |
| sess_get_config | session；有效配置、单位、说明和平台能力，不包含认证秘密 |
| sess_list | 无参数；列出连接和 exec 状态 |
| sess_exec | session?、command、name?、login?、cwd?、env?、interactive?、pty?、collect_output?、exec_total_timeout_ms?、term_on_timeout?、其他顶层配置?；SSH exec，独立 exec_total_timeout_ms 等待协议完成，忽略 read 超时与 end_pattern |
| sess_send_text | session 或 handle（二选一）、data、collect_output?、其他顶层配置?；发送文字并默认收集输出，不解析转义 |
| sess_send_escaped | 同上；显式解码一层转义再发送 |
| sess_sendline_text | 同 text，追加一次配置的 line_end |
| sess_sendline_escaped | 同 escaped，追加一次配置的 line_end |
| sess_read_available | session 或 handle（二选一）、其他顶层配置?；立即读取未读数据，不等待 |
| sess_read_until | session 或 handle（二选一）、其他顶层配置?；按匹配、超时等条件结束读取 |
| sess_read_tail | session 或 handle（二选一）、lines?、其他顶层配置?；最后 N 行，默认 20，不移动增量游标 |
| sess_read_history | session 或 handle（二选一）、offset?、其他顶层配置?；立即分页读取保留历史，不移动增量游标 |
| sess_task_list | session?；列出保留任务，不消费输出 |
| sess_task_status | handle；任务状态、退出信息和 stdin_open |
| sess_task_wait | handle、exec_total_timeout_ms?、term_on_timeout?、其他顶层配置?；等待任务完成并收集增量输出 |
| sess_task_signal | handle、signal；请求 SSH 信号，不保证退出 |
| sess_task_release | handle；释放已完成且无活动操作的任务记录 |
| sess_task_close_stdin | handle；向 interactive 任务发送 SSH stdin EOF，不消费输出、不关闭连接 |
| sess_close | session；关闭并丢弃该连接和缓冲，不返回终端正文 |
| sess_close_all | 无参数；关闭并丢弃全部连接和缓冲，不返回终端正文 |

工具发现直接提供完整工具说明及每个适用参数的语义、默认值、取值范围、互斥关系、执行与转义边界，包括 ssh_jumps 内嵌字段；模型获得工具定义后即可构造调用，无需先查 help。发现与 `sess_help` 使用同一套内置定义，只隐藏该操作不适用的配置参数。`sess_help` 继续提供可选的流程参考和排障查询：

```text
sess_help()                                            # 简短主题目录
sess_help(query="RPC 超时")                            # 默认最多 3 条搜索摘要
sess_help(topic="timeouts")                             # 一个操作主题
sess_help(topic="sess_exec")                            # 一个工具的详细 schema
sess_help(topic="sess_exec", parameter="env")           # 仅一个参数
sess_help(topic="configuration", parameter="line_end") # 配置默认值/协议差异
```

内置主题包含 connections、tasks、timeouts、ssh_jumps、naming、exec_environment、interactive、read_only、output、configuration，也接受全部工具名作为 topic。query 与 topic/parameter 互斥，parameter 必须配合 topic。搜索使用固定代码的关键词/标题/正文匹配及中英文关键词映射，不调用模型、向量库、网络或本地文件，也不搜索会话输出/凭据；query 最长 512 字节，最多处理 16 个搜索词，limit=1..5，每条摘要最多约 220 字符。无匹配明确返回 matched=false；按结果的 topic/parameter 获取细节，避免重复加载全文。

全部工具统一使用完整描述，不再提供 tool_profile 切换；运行时协议和配置默认值保持不变；原来已接受但对该操作无效的配置覆盖保持运行时兼容，发现列表和 help 不再推荐它们。数字配置仍支持整数及算术字符串。完整 skill 是可选流程参考，不要求常驻或每次查 help。

所有工具的配置参数都直接写在顶层，例如 `sess_configure({"session":"s1","read_only":true})`，或 `sess_read_until({"session":"s1","read_total_timeout_ms":10000})`。工具 schema 不再暴露 `config` 对象；返回结果中的 `config` 仍用于展示当前会话配置。

会话和任务命名：connect/exec 可传顶层 `name`，默认仍返回稳定 `sN` / `taskN` ID。`session` 或 `handle` 也接受当前名字作为别名；结果中的 session/handle 始终是稳定 ID，name 单独返回。使用 `sess_rename({"session":"s1","name":"prod"})` 和 `sess_task_rename({"handle":"task1","name":"build"})` 更新别名；旧别名立即失效，稳定 ID 仍有效。名字限 1..64 个 ASCII 字母、数字、点、下划线或短横线，首位须字母/数字，`sN/taskN` 保留。会话名与任务名分别唯一；会话连接期间也预留名字，任务名在发送 exec 请求前原子预留，重复名字不发送。名字只在当前保留记录内有效，释放后可复用；长流程优先保留稳定 ID。

每个服务进程生成 8 字符随机 `instance_id`，在 initialize 的 `_meta`、connect/reconnect、列表及未知句柄错误中展示，不要求调用方每次传入标识。未知句柄可能已关闭、过期或来自服务实例变化，先 list 检查，不自动重放；该诊断标识不是访问控制或强制实例校验，不提供重启恢复。本轮不增加 sess_expect 工具。

每次连接返回短句柄，例如 `{"session":"s1","connected":true}`，不读取、不消费欢迎信息。句柄进程内递增不复用；连接操作明确指定 session；exec 任务操作使用独立 handle（task1、task2……），同样进程内递增不复用。Telnet 登录属于终端流，应观察提示再发送相应信息，不猜测或重放登录流程。

`sess_exec` 可省略 `session`，仅在恰好保留一个会话时推断；结果仍返回实际 `session`。没有会话时须先 connect，多个会话时必须明确指定，不按在线状态、模式或最近连接筛选。唯一会话已断开时须显式 reconnect，不自动重连；显式指定的会话失效时不回退。任务工具仍须提供 `handle`，`sess_task_list()` 仍列出全部任务。

SSH 默认 mode=exec，适合普通服务器。**sess_exec 仅适配 SSH mode=exec，默认非交互；可用 interactive=true 显式保留任务 stdin。SSH mode=stream 和全部 Telnet 会话直接报错，不发送输入，也不自动回退到交互 shell。**持久终端 shell、console 登录流程和任何 Telnet 会话使用 mode=stream 的 send/read。SSH mode=exec 可显式启动 GDB 等交互任务，再按 task handle 收发。SSH 只是传输协议，不表示 shell 非交互；流静默也不能成为改用 exec 探测或重放的理由。交换机、路由器和 console 必须显式 mode=stream。Telnet 只能使用 stream。SSH 默认不校验主机密钥，也不自动读取或写入 known_hosts。显式提供 host_key（SHA256 指纹）或 known_hosts 文件路径才启用校验，校验失败直接报错。兼容保留 insecure=true 显式跳过；优先级为 insecure=true、host_key、known_hosts。insecure=false 本身不启用校验。

```json
{"protocol":"ssh","host":"server.example","port":22,"username":"operator","keyfile":"/private/id_ed25519","mode":"exec"}
```

SSH 或 Telnet 目标可通过顶层 `ssh_jumps` 数组指定一个或多个跳板，顺序是本机 → ssh_jumps[0] → ssh_jumps[1] → 目标 host/port，最多 8 跳。每跳填写 host、username 和 password/keyfile，port 默认 22；key_passphrase、host_key、known_hosts、insecure 的含义与目标相同，逐跳独立认证和校验。跳板必须允许 SSH TCP 转发（direct-tcpip），不通过远端 shell 执行 ssh 命令。`protocol` 表示最终目标协议：SSH 目标支持 exec/stream，Telnet 目标仅支持 stream 并继续使用内部 Telnet 实现。旧参数 `jumps` 不再接受。

```json
{"protocol":"ssh","host":"target.internal","port":22,"username":"target-user","keyfile":"/path/to/target_key","ssh_jumps":[{"host":"jump1.example","username":"jump-user","keyfile":"/path/to/jump1_key"},{"host":"jump2.internal","username":"jump-user","keyfile":"/path/to/jump2_key"}],"connect_timeout_ms":30000}
```

Telnet 目标示例（登录仍须观察提示后通过 send/read 显式处理，不自动发送目标凭据；最后一个跳板到 Telnet 目标为明文）：

```json
{"protocol":"telnet","host":"switch.internal","port":23,"ssh_jumps":[{"host":"bastion.example","username":"jump-user","keyfile":"/path/to/jump_key"}],"connect_timeout_ms":30000}
```

整条链路共用一个 connect_timeout_ms 预算（仍受 RPC 上限约束），失败或取消会关闭已建立的链路，不自动重试。sess_reconnect 私下复用整条跳板链与当前会话配置，保持句柄、不重放命令；close 释放链路与认证参数。SSH 目标的 SSH keepalive 探测目标；Telnet 目标的 SSH keepalive 探测最后一个 SSH 跳板（不检测 Telnet 登录/应用就绪），均穿过前面的跳板；TCP keepalive/排队反压测量本机到第一跳的 TCP 连接。SSH 目标的 connect/reconnect peer 摘要仍描述最终目标；Telnet 目标不返回 peer，不能把跳板身份当作设备身份；跳板认证参数不会进入结果或配置，审计中的密码/私钥口令逐跳脱敏。

```json
{"read_idle_timeout_ms":500,"read_total_timeout_ms":30000,"protocol":"telnet","host":"switch.example","port":23}
```

跳板失败包含 `hop_index`（从 1 开始、最终目标为跳数加 1）、`host`、`port`、`from_host`、`stage` 和 `target`。第一跳的 from_host 是 local，后续是前一跳配置的主机名，不代表实际出口 IP。SSH direct-tcpip 已用于 SSH/Telnet stream 路由；已有 `ssh -L` 转发也可直接通过其监听地址连接，本轮不新增创建/管理 -L/-R/-D 监听工具。

### 显式重连

```json
{"session":"s1"}
```

上例用于 sess_reconnect：先关闭旧传输连接，再使用原地址、SSH 认证方式、主机校验参数、mode 和当前会话配置建立连接，成功后仍使用 s1。connect_timeout_ms 的当前值用于本次重连。私钥与 known_hosts 文件按原路径重新读取；密码等必要参数仅保留在进程内，不出现在 get_config/list 或重连工具结果中。

重连丢弃旧缓冲和游标，从新连接的偏移 0 开始；新连接欢迎信息保留供显式 read，不消费、不返回终端正文。不发送回车、Telnet 用户名/密码、初始化命令或历史输入；SSH 协议认证正常执行。不保证 shell 目录/环境、设备视图或终端登录状态恢复，重连后应先读取观察。

有读写、任务操作、配置更新、其他重连或活跃 exec 时返回 busy，旧连接不受影响。重连会清空全部旧任务 handle 与历史；断线后仍未知的远端程序不自动重放。连接失败返回 connected=false 和明确错误，保留原参数与配置，可再次显式重连；绝不自动重试。取消或连接超时也保留句柄。close/close_all 会取消进行中的重连并彻底释放参数，已关闭或回收的句柄不能重连，须重新 connect。read_only 会保留，仍禁止应用输入；串口服务端能否恢复原设备状态取决于其实现。

### 连接健康与设备身份

会话默认连续两小时无工具操作才回收（不是连接建立后两小时强制关闭）；活动读写与活跃任务不回收。SSH 默认同时启用 TCP keepalive 和每 30s 的 SSH `keepalive@openssh.com` 协议探测，每次等回复最多 10s，仅一个探测在途。服务端否定回复也算有响应。keepalive 不输入字符、不延长本地会话空闲期限，不能阻止设备重启或终端自身的登录超时。配置更改可唤醒探测调度；已在途探测按原截止时间完成。

断连返回 disconnect_reason 与可用的原始 error：reset_by_peer、transport_timeout、keepalive_timeout、ssh_disconnect、remote_eof、transport_error。EOF 只说明连接关闭，不猜测设备空闲超时或重启；探测超时也不能证明设备一定已死机。保留句柄时优先 next_action=sess_reconnect，绝不重放输入。已回收/关闭时返回 session_idle_expired/explicit_close 与 sess_connect；最多保留 128 条、不含凭据的原因记录两小时，可能提前淘汰。未识别句柄报告 unknown_handle，说明可能关闭/回收/服务重启，不伪造设备原因。

SSH connect/reconnect 仅在该次结果附简短 peer：server（最多 96 字符软件标识）、host_key（SHA256 指纹），与同句柄上次观察值不同则附 host_key_changed=true。不在普通 read/send/list 中重复。失败重连不会把上次身份冒充为本次观测值。默认不校验策略不变；这是对端线索，不能证明镜像版本、物理设备身份或是否重启；不会自动执行 uname 等命令。

## 配置

所有工具的输入参数均位于顶层，不需要嵌套 `config`；connect、configure 与单次操作使用同一套配置校验。未知字段、null、非法正则、超范围值整体拒绝，不部分生效。调用开始时快照配置；read_only 在实际写入前重新检查，其余配置修改不会影响当前调用。单次操作的顶层配置不改变会话默认配置。

| 配置项 | 默认值 | 语义 |
|---|---|---|
| read_only | false | 会话级输入保护，禁止 send、exec 及手动/自动信号；只能通过 connect/configure 设置，不能单次覆盖 |
| connect_timeout_ms | 10000 | TCP 连接、SSH 认证和初始 shell 建立，总范围 1..110000ms；已建连后修改供下次 sess_reconnect 使用；新 connect 仍需显式传入配置字段 |
| write_timeout_ms | 10000 | 输入写入或 exec channel 建立，1..110000ms |
| session_idle_timeout_ms | 750000 | 无操作会话回收，0 禁用；最长一年；活动读写或运行中 exec 不回收 |
| read_total_timeout_ms | 30000 | 从读取开始的总时限，1..110000ms；不影响 sess_exec |
| read_idle_timeout_ms | **500** | 从读取开始计算，收到端点字节后重置；0 禁用，最高 110000ms；不影响 sess_exec |
| exec_total_timeout_ms | 120000 | sess_exec/task_wait 等待协议完成的独立总时限，1..300000ms；不受 read 超时与 end_pattern 影响；异步启动不建立后台运行计时器 |
| term_on_timeout | true | 完成等待超时尝试一次 TERM；可单次覆盖；普通 read 超时、取消等待和输出上限不触发 |
| ssh_keepalive_ms | 30000 | SSH 协议探测间隔，0 禁用；不发送终端输入、不刷新本地空闲计时；直连 Telnet 不使用，经 SSH 跳板的 Telnet 探测最后一个跳板 |
| ssh_keepalive_timeout_ms | 10000 | 单次 SSH 探测回复时限，1..110000ms；无回复关闭传输，未完成任务结果未知 |
| tcp_keepalive_ms | 15000 | 系统 TCP keepalive 周期参数，0 禁用，最高一天；实际探测与失效时序取决于 OS |
| line_end | SSH `\r`，Telnet `\r\n` | 可见转义文本，配置时按 UTF-8 与字节转义解码一次，最多 1024 字节；可为空 |
| encoding | utf-8 | utf-8、gbk、shift-jis；支持 utf8、sjis、shift_jis 等别名 |
| input_encode_error_action | error | 无法编码时整条输入拒绝，不进行部分发送 |
| output_decode_error_action | to_escape_bytes | 非法输出字节显示为 `\xNN`；to_hex 显示 `<NN>`；error 报错并保留非法字节 |
| max_input_bytes | 4096 | 实际编码后输入长度，包含行结束符；1..1048576 |
| max_output_bytes | 65536 | 默认 64KiB 分页，返回 UTF-8 文本字节预算，包括非法字节的展示文本；1..524288（最高 512KiB） |
| backpressure_bytes | 0 | 非零时按系统 TCP 队列阈值拒绝新写入，最高 1GiB；需平台支持 |
| end_pattern | 空 | Go RE2，匹配解码后的未读输出；空表示禁用，最多 1024 字节；不影响 sess_exec |

数字可传整数或只包含整数、四则运算、括号的字符串，例如：

```json
{"session_idle_timeout_ms":"2*60*60*1000","max_output_bytes":"64*1024","read_idle_timeout_ms":500,"session":"s1"}
```

不执行 eval、函数调用或变量；除法必须得到整数，表达式长度与数值有界。read_only、connect_timeout_ms、session_idle_timeout_ms、tcp_keepalive_ms、ssh_keepalive_ms、ssh_keepalive_timeout_ms 是会话/连接配置，只能在 connect/configure 中设置；其余可按操作覆盖。

### 多人串口旁观

连接时可在顶层设置 `"read_only":true`，也可显式切换：

```json
{"read_only":true,"session":"s1"}
```

上例用于 sess_configure。开启后四个 send 工具与 sess_exec 返回 `input_state=not_sent`、`error` 以 `read_only:` 开头，不发送应用输入、不消费输出；手动信号被拒绝，自动 TERM 报 skipped_read_only；读取、查看配置、列出会话、关闭连接仍可用。SSH/Telnet 协议协商与系统 keepalive 不受影响。

read_only 不能在任意操作中覆盖，无论 true 或 false 都拒绝。修改 read_only 时若应用写入正在进行，整份 configure 返回 busy 且不修改任何配置；待写入结束后显式重试。开启成功后，先前操作即使持有旧配置，也必须通过写入前的最新只读检查。不会停止已启动的远端命令，其后续输出仍可读取。

这是防误操作开关，AI 仍可通过 sess_configure 关闭，不是权限隔离。每个 keepsess 连接有独立的读取游标；串口服务器能否广播输出、是否抢占连接取决于服务端，read_only 不提供多人广播或输入仲裁。

## 交互 exec 与 stdin

普通 sess_exec 默认 interactive=false、pty=false，stdin 保持 EOF 语义；cat/python 等普通命令不会因隐式保持输入而挂起。显式交互示例（已建立 SSH mode=exec 的 s1）：

```json
{"session":"s1","command":"gdb ./app","interactive":true,"pty":true,"collect_output":false}
```

拿到 task handle 后，依次调用 sess_read_until 观察提示，使用四个 send 工具之一输入；例如 sess_sendline_text：

```json
{"line_end":"\\n","read_idle_timeout_ms":500,"handle":"task1","data":"bt"}
```

interactive=true 在命令启动前保留 stdin，不自动修改 collect_output 或超时策略。通常使用 collect_output=false 启动，再按 handle 收发；同步收集期间也允许其他调用 collect_output=false 向同一任务写入。每任务输入互斥、输出读取互斥，读写可并行；不同任务输入独立。启动时未开启 stdin 的任务不能事后切换。

pty=true 必须搭配 interactive=true，启动前申请 vt100 PTY；默认不申请。PTY 会改变行规程、回显、换行以及 stdout/stderr 表现，服务器可能把双流合并到终端。结束符仍按 line_end 显式配置，工具不猜测或规范化输入。无 PTY 时 Ctrl-C/Ctrl-D 是普通输入字节，信号请用 task_signal。

sess_task_close_stdin({"handle":"task1"}) 发送 SSH stdin EOF，保留输出和退出状态，重复关闭不再发送请求。发送 \x04 与 EOF 不等价；EOF 不保证程序退出，也不关闭 transport。关闭后继续 read/task_wait 收集输出。四种 send 与 EOF 都受当前会话 read_only、写入超时及 RPC 上限保护；卡住或取消正在进行的写入会关闭共享连接、明确报告未知结果，不重放输入。结果中的 interactive、pty、stdin_open 可供检查。

## 发送、转义和编码

四个 send 工具使用 session 操作 stream，或使用 handle 操作显式 interactive=true 的 exec 任务；二者必须且只能指定一个。text 表示文字输入，按 encoding 编码，不解析转义；它不限定 ASCII，可以发送中文、日文等 Unicode 文字。escaped 在 JSON 解码之后显式解码一层，适合模型经过 XML/JSON agent 承载可见转义文本。

- 普通文字、`\uNNNN`、`\UNNNNNNNN` 使用配置的文字编码。
- `\0 \r \n \t \b \f \v \a`、`\xNN`、Go 三位八进制转义产生实际字节，不二次编码。
- `\\` 产生一个反斜杠，支持转义引号；普通引号不需要额外转义。
- HTML 实体不转换；非法/不完整转义在任何写入前报错。

| escaped 的 JSON data 参数 | 实际字节 |
|---|---|
| `"\\r"` | 0D，CR |
| `"\\x03"` | 03，Ctrl-C |
| `"\\0"` | 00，NUL |
| `"\\\\r"` | 5C 72，字面反斜杠+r |
| `"\\u4e2d"`，encoding=gbk | D6 D0，GBK 的“中” |

sendline 无条件追加一次 line_end，不检查已有结尾、不裁剪、不归一化。空 data 合法，因此空 sendline 就是发送一次结束符；分页空格、Ctrl-C 等应使用不追加结尾的 send。

```json
{"end_pattern":"(?m)^<[^>\r\n]+>\\s*$","session":"s1","data":"display version"}
```

上例用于 sess_sendline_text；提示符须按观察结果配置，不能把正文里的括号猜成命令完成。没有自动分页、自动登录、自动 commit/save 或自动确认。

默认 collect_output=true：写入成功后按 read_until 收集未读输出，减少工具往返；false 只返回发送回执。**返回的流不归属于本次输入**：可能包括旧的未读输出、回放或自发日志。

### 发送回执不等于设备接受或认证成功

保留 `input_state=sent`，其严格含义为“**本地写入已完成**”。不改为 `accepted`，因为终端流没有设备应用层确认，accepted 更容易被误解为“设备已接受用户名/密码”。这不是设备认证结果字段。

| 字段或状态 | 能证明 | 不能证明 |
|---|---|---|
| sent（stream） | 本地 writer 完整接受本次应用输入 | 设备收到/处理输入、密码正确、登录成功或 shell 就绪 |
| sent（exec 启动） | SSH exec channel 启动请求成功 | 命令完成、退出码为零或某个终端内的登录结果 |
| sent（task 输入 / EOF） | stdin 本地写入完成 / EOF 请求已写出；EOF 不计入应用字节数 | 程序已消费输入、处理命令或退出 |
| not_sent | 本次尝试未写入新的应用字节 | 之前输入是否已经执行 |
| unknown | 写入/启动失败、超时或取消，可能已经部分发送或执行 | 零字节送达 |
| input_bytes | 编码/转义后的计划输入长度，包含 line_end | 物理 TCP 包大小或执行次数 |
| written_bytes | 本地 writer 确认接受的应用字节数，不含 SSH/Telnet 协议开销 | 远端应用实际消费字节数；unknown 时只能作为已确认下界 |

`sent` 甚至不应笼统解释为“字节已写入 TCP socket”：SSH 还有 channel/流控等传输层。TCP 队列清空、TCP ACK、keepalive 或 connected=true 都不能证明终端程序接受密码。unknown 时 written_bytes=0 或字段省略也不能证明没有送达。接口没有 input_id，不自动重试或重放。

发送用户名/密码后，`output` 为空或省略，且 `start == end`，只表示**本次读取没有消费原始输出字节**。它不说明“密码错误”“登录成功”“一定没送到”或“shell 已准备好”。连续两次空读取也无法区分这些情况；关闭回显的 shell 和密码输入本来就可能静默。检查 reason/error/more：输出可能在此前已读取，也可能存在尚未组成完整字符的未读字节。

不要因空输出重发用户名/密码、换 ID 重试、猜测改 CR/LF 或额外按回车。回车是新输入，可能提交空密码或触发上一命令回放。正常发送后收集静默 500ms 返回不代表执行失败；可改用一次有界、只读等待，而不是多轮空轮询：

```json
{"read_total_timeout_ms":10000,"read_idle_timeout_ms":0,"end_pattern":"(?im)^(<switch>|Password:|Login incorrect)[[:blank:]]*\\r?$","session":"s1"}
```

上例用于 sess_read_until；仅示意等待设备提示符、重新询问密码或失败文本，正则必须按实际观察到的设备输出配置。end_pattern 匹配只是观察到文本，旧未读内容或 console 回放也可能命中，不能把它当作本次密码认证的协议确认。若等待后仍无输出，应报告“本地写入已完成，远端是否收到、是否通过认证和 shell 是否就绪仍未知”，再检查端点/console 服务、已知结束符要求或请求现场证据。不要无限轮询或重连后自动重放凭据。

### 模型参数值与 JSON 展示转义

模型不一定直接生成 MCP JSON。DeepSeek V4.1 的公开参考实现生成 DSML，字符串参数 string="true" 的正文原样保留，服务解析层再用 json.dumps 组装 API arguments，agent 再构造 MCP JSON-RPC；对象/数组等 string="false" 的参数正文仍使用 JSON。参见[官方编码说明](https://huggingface.co/deepseek-ai/DeepSeek-V4.1-Flash/blob/main/encoding/README.md)和[解析器实现](https://huggingface.co/deepseek-ai/DeepSeek-V4.1-Flash/blob/main/encoding/encoding.py)。具体 agent 是否另有转换，须查看其实现。

以 sess_send_escaped 发送 Ctrl-C 为例：逻辑 data 值是四个可见字符 `\x03`；DSML 字符串参数正文写 `\x03`，等价 JSON 参数表示为 `{"data":"\\x03"}`。正常 JSON 序列化/反序列化保留逻辑值；最后由 keepsess 显式解码为一个 03 字节。不要把 JSON 示例里的额外反斜杠直接照搬进原样字符串参数，不要多次预解码。DSML 的 XML 样式不代表 HTML 实体会被解码，`&#13;` 仍是五个字符；不能仅凭最终输入错误就推断模型或 agent 哪一层有问题。

## 读取与结果

read_available 立即返回当前未读数据；read_until 从未读游标开始，等待后续数据。stream 读写可并行，同会话一次只允许一个读取者和一个写入者，竞争直接报 busy，无无限队列。collect_output=true 的 stream 发送必须先获得读取权，否则不发送。exec 的四个 read 工具必须传 handle，每个任务独立缓冲和增量游标；不同任务可并行读取，同一任务读取竞争返回 busy。session 与 handle 不可同时传入，不根据连接猜测任务。

| reason | 含义 |
|---|---|
| exec_finish | SSH exec channel 完成，不保证成功；有可靠协议退出状态才给 exit_code |
| match | 观察到结束正则，仅消费到匹配末尾，后面的字节留待下次读取 |
| total_timeout | 达到读取总时限，或 exec/task_wait 完成等待时限；后者默认尝试 TERM，正常超时不关闭连接 |
| idle_timeout | 本次读取期间无新字节达到静默时限，不关闭连接 |
| aborted | 网络、协议或输出解码错误，附 error；connected 指明连接是否还在 |
| cancelled | 调用方取消；读取取消保留连接，发送期间取消则断连且 input_state=unknown |
| output_limit | 达到输出预算，后续数据保留；若下一个字符放不下，返回 minimum_output_bytes |
| closed | 对端正常结束，或本地主动丢弃连接 |

立即读取耗尽当前数据时可以不带 reason。参数错误、非法正则和 busy 是工具错误，不混入 reason。匹配优先于同批数据的其他结束条件；输出预算以内没有匹配且达到预算时，先返回 output_limit，剩余字节后续读取。

输出统一为有效 UTF-8，不默认剥离 ANSI 或改变换行。跨网络包的半个字符在增量读取中保留；EOF、已完成 exec 或显式历史读取按错误策略处理残缺字节。error 策略不会因解码错误关闭连接，可以换策略再读。必要的字节转义作为整体展示，不切碎。

每个 stream/exec 任务历史保留 1MiB，more 表示后面还有数据；start/end 为原始字节偏移，不是显示字符下标。增量读取推进对应 stream/任务的游标；read_history/read_tail 不推进。read_history 默认从仍保留的最早位置开始，用 offset=end 分页，不会无限等待未来输出。dropped_bytes 明确报告请求位置之前已被覆盖的字节。显式 close 会丢弃全部缓冲，后续读取失败；远端自然关闭后尚可读取保留数据，直到回收或 close。

## exec 登录环境与输出投影

`sess_exec` 默认 `login=true`，通过 `bash --login -c` 运行非交互登录 shell；stdin 仍默认 EOF，无自动 PTY。Bash 不存在时按远端退出结果报错，不回退或重试。登录初始化通常读取 `/etc/profile` 和第一个可用用户登录初始化文件，不保证读取 `.bashrc`，也不代表加载所有 profile。初始化可能产生输出和副作用，计入相同的任务完成等待。

`env` 是最多 128 项的字符串对象，键必须是 shell 标识符；`cwd` 和 env 值正确引用。顺序为登录初始化 → export 显式 env → cd 到 cwd → 执行 command，设置失败就不执行 command，env 审计脱敏。设置仅本任务生效，不影响下次 exec。`login=false` 且没有 cwd/env 时原样提交 command；显式 cwd/env 时仍用 `bash -c` 包装但不加载登录 profile。max_input_bytes 对最终编码后的 exec 请求（含包装）生效。

```json
{"session":"prod","command":"make test","name":"build","cwd":"/srv/project","env":{"BUILD_MODE":"debug"},"collect_output":false}
```

输出工具及 exec/task_wait 支持顶层 `strip_ansi`（默认 false）、`include_pattern`、`exclude_pattern`（默认空，Go RE2，最多 1024 字节）。先清理 ANSI CSI/OSC 等控制序列，再按行包含/排除；这些设置也能通过 connect/configure 设置默认值。原始历史不改写，start/end 仍是原始字节偏移，过滤掉的行推进增量游标。`end_pattern` 继续匹配解码后的原始文本，不受过滤影响，也不会被 exec 使用。

投影每页最多扫描 64KiB 原始数据，返回 `scan_limit` 时按原 handle 继续读取；行超过 16KiB 时按有界片段过滤并返回 `line_fragments=true`，片段边界可能略延伸以保持字符/字节转义完整。未完成行保留到换行、读取结束条件或任务完成；未完成 ANSI 序列保留到后续数据，极长未闭合序列可能返回 `view_incomplete`，可关闭投影查看原始历史。过滤启用时一个可见行/片段整体返回；页面过小时返回 output_limit/minimum_output_bytes，不部分消费该行。

结果报告 `filtered_bytes`（丢弃的清理后 UTF-8 文本字节）、`filtered_lines`（行/片段数）、`stripped_bytes`（移除的 ANSI 文本字节），与原始历史丢失 `dropped_bytes` 区分。空输出会说明过滤/ANSI/不完整文本等原因；现有输出和历史上限继续生效。读取原始历史时显式设置 `strip_ansi=false, include_pattern="", exclude_pattern=""`。

```json
{"handle":"build","strip_ansi":true,"include_pattern":"ERROR|WARN","exclude_pattern":"DEBUG"}
```

## SSH exec 与连接保护

**sess_exec 不使用 read_total_timeout_ms、read_idle_timeout_ms 或 end_pattern。** 无论这些值来自会话配置还是本次顶层参数，命令都不会因静默 500ms、读取时限短或输出碰巧匹配提示符而提前结束等待。它使用独立的 exec_total_timeout_ms（默认 120000ms，1..300000ms），从命令成功启动后的收集阶段开始计时；channel 建立仍受 write_timeout_ms 约束，整个 RPC 仍有 115s 硬上限。默认完成等待 120s、可设至 300s，但单次 RPC 的 115s 预算先到时仅结束收集、保留 handle，不发送 TERM；继续 task_wait/read。此配置是完成等待读取时限，不是后台命令运行寿命限制，异步启动不建立运行计时器。

`exec_total_timeout_ms` 和 `term_on_timeout` 是 `sess_exec` / `sess_task_wait` 的顶层参数，只影响本次调用。会话默认值仍通过 connect/configure 的顶层参数设置。旧的嵌套 config 调用仅为兼容保留，不再出现在工具 schema；同时提供时顶层值优先。

例如，下面的 sess_exec 最多等待协议完成 60 秒，不改变会话默认设置：

```json
{"session":"s1","command":"sleep 2; uname -a","exec_total_timeout_ms":60000,"term_on_timeout":false}
```

`collect_output=true`（默认）在同一连接内只允许一个同步 exec 调用，竞争立即返回 busy/not_sent，不排队。同步调用返回时释放名额，包括超时、取消和 output_limit；旧程序仍运行也不阻止下一次同步调用。`collect_output=false` 启动后返回 `handle: "task22"` 与发送回执，不等完成，不启动后台运行计时器；异步任务彼此可并行，也可与同步任务并行。资源上限与服务器 channel 限制仍可能拒绝新任务。

每次 exec 使用独立 SSH channel，合并该任务的 stdout/stderr，按协议退出状态判断完成。cwd、环境不跨命令保留。正常同步完成且输出全部返回时省略 handle；非零退出码也属于完成。提前返回、启动结果不确定或仍有输出时返回 handle，后续按 handle 管理。完成记录仍可在 task_list 找到。缺失 exit-status/exit-signal 返回 warning 与缺省 exit_code，不能假定成功；exit-signal 单独返回 exit_signal，不把合成数字当作远端退出码。

每次 task_wait 均获得新的本次等待预算。结果中的 `wait_elapsed_ms` 表示本次收集耗时，`started_at/task_elapsed_ms` 表示任务启动及累计运行时间（完成后冻结）。`reason=total_timeout` 额外提供 `timeout_source=rpc_budget|exec_wait|caller_deadline`；普通 read 的配置总时限为 read_total。RPC 预算或调用方截止只结束收集，保留 handle，不发送 TERM，也不会把截止时间带入下一次等待。

完成等待超时返回 reason=total_timeout。**term_on_timeout 默认 true**：sess_exec 与 sess_task_wait 在超时后尝试发送一次 TERM，然后返回 handle，不等程序退出、不自动升级 KILL、不重放。设 false 仅结束等待。TERM 请求的 `signal.request_state=sent` 只表示请求已写出，不证明远端处理或程序/子进程树退出。可能返回 not_sent、unknown、skipped_read_only、skipped_finished，并附具体错误。信号写入限时 1s，包含在整个 RPC 115s 预算内；实际完成等待还受剩余 RPC 预算约束。半开连接导致 channel 建立或控制写入卡住时，必须关闭共享 transport 才能解除底层阻塞，会明确报告所有受影响任务的未知结果。

普通 sess_read_until 仍使用 read 的 total/idle/end_pattern 配置，超时**绝不触发 TERM**。取消等待、输出上限和解码错误不发信号。sess_task_wait 不占同步 exec 名额，等待期间可并行查询状态和发信号；同一任务只允许一个输出读取者。

任务状态：starting（启动中）、running（尚未观察到结束）、finished（channel 完成，成功与否看退出信息）、unknown（启动/连接故障导致远端结果未知）。task_status 不消费输出，more 表示还有未读字节，signal 是最近一次信号请求信息。task_release 仅删除已结束且无活动操作的记录；运行中或未知任务返回 busy。close/close_all 丢弃全部关联 handle 和缓冲，但不保证远端程序停止。

示例调用顺序（handle 使用实际返回值）：

```text
sess_exec({"session":"s1","command":"long-job","collect_output":false})
sess_task_status({"handle":"task22"})
sess_read_available({"handle":"task22"})
sess_task_wait({"handle":"task22","exec_total_timeout_ms":10000,"term_on_timeout":false})
sess_task_signal({"handle":"task22","signal":"TERM"})
sess_task_wait({"handle":"task22"})
sess_task_release({"handle":"task22"})
```

每连接最多 8 个未完成任务、16 条保留记录；全局最多 64 个未完成任务、128 条记录。运行中/未知任务不自动淘汰以腾出容量；完成记录保留 30min，达到容量时可提前淘汰最旧且无活动操作的完成记录。读取被淘汰的 handle 明确报 unknown or expired，不切换到其他任务、不重放。会话被关闭、重连或回收时任务记录一起清理。

**exec 不再有 110 秒无输出保护。** 无活动调用且无活跃任务的会话默认 2h 回收，可设 session_idle_timeout_ms=0 禁用；安静的后台任务不因无输出被关闭。连接/握手/写入截止时间、单次调用上限、TCP keepalive 继续有效。

收集输出但返回空文本时附 `comment`，区分无新数据、等待超时、命令已结束、历史范围为空、不完整字符、输出预算不足及错误/关闭。它解释本次观察，不保证设备没有发送内容，不证明执行或认证成功。collect_output=false 不收集输出，也不会误报“设备没有返回”。结构化 reason/state/退出信息与发送回执仍是判断依据。

Linux 使用 SIOCOUTQ/TIOCOUTQ 查询 TCP 排队字节，包含未确认数据；SSH 测量的是整个加密 transport，不能推断某条命令的积压。Windows 当前明确不支持此查询，非零 backpressure_bytes 被拒绝，get_config 会报告能力。两平台普通发送仍使用 Write/SSH 窗口流控和超时。队列低于阈值只是允许尝试写入，不保证整条消息立刻写完或远端已处理。

最多 32 会话、32 tool workers、64 输出帧；输入 JSON 帧上限 12MiB+128KiB，供转义后的最大输入使用。stdio 调度、远端操作与输出写入分离；stdout 消费者阻塞 3s 后结束服务，避免队头阻塞和无限 writer。重启后须重新连接。关闭不等待活动操作，且不回吐正文。认证字段审计脱敏、文件权限 0600，但 command/data 仍可能包含秘密。Telnet 是明文。

## 构建与验证

正常构建只需 **Go 1.22.1**：前端 JS/CSS 和 xterm.js 已用 `go:embed` 打包，不需要 Node.js、npm、前端打包器、CGO 或 C 编译器。首次构建需下载 Go 模块，缓存齐全后可以离线构建。

```sh
CGO_ENABLED=0 go build -o keepsess ./cmd/keepsess
```

三平台脚本另需 `sh` 和 `sha256sum`；Git 仅用于构建标识，缺失时使用 unknown。
普通 Go 测试不需要前端工具；`-race` 需要 CGO 和 C 编译器；可选浏览器验收另需 Node.js、Playwright、Chromium。

```sh
GOTOOLCHAIN=go1.22.1 go vet ./...
GOTOOLCHAIN=go1.22.1 go test -race ./...
GOTOOLCHAIN=go1.22.1 SESSMCP_LONG_TEST=1 go test -race ./... -count=1 -timeout 4m
GOTOOLCHAIN=go1.22.1 sh scripts/build.sh
```

产物和 SHA256SUMS 位于 dist/，包括 Linux amd64/arm64 与 Windows amd64。build.sh 注入 Git 标识，工作区有改动则带 dirty。固定依赖 x/crypto v0.33.0、x/sys v0.30.0、x/text v0.22.0，Telnet 为内部纯 Go 实现，不引入外部 Telnet 库。

测试覆盖真实 SSH 退出与双流、缺失退出状态、Telnet IAC 分包、交换机模式/分页/慢输出、输入转义与线上字节、Unicode/GBK/Shift-JIS 分包与错误策略、超时/取消、读写并发、TCP 满缓冲/队列阈值、stdio 慢消费者及 race。长测试验证真实静默端点的读取在两分钟内结束且 transport 保留。Windows/ARM64 在本地只验证交叉编译；docs/ci.yml 是包含 Windows 短测试的 CI 模板，未自动启用。

[验证记录](../VALIDATION.md) · [现场反馈与迁移说明](../docs/field-report.md) · [AI skill](../skills/keepsess/SKILL.md)

### 文件传输与 SSH 隧道

新增五个工具，总计 30 个。均复用已有 SSH 连接及跳板链路，适用于 SSH exec/stream，不支持 Telnet。

- `sess_get_file(session, src, dst?, backend?, overwrite?)`：远端普通文件下载到 keepsess 所在机器。省略 dst 时创建系统临时目录，返回 JSON 中的绝对 dst。文件由调用者使用后删除。
- `sess_put_file(session, src, dst, backend?, overwrite?)`：上传本地普通文件。路径均是文件，不自动创建父目录，不支持目录递归或续传。
- backend 默认 auto：探测 SFTP，随后传统 SCP，最后 SSH exec 原始字节流。无覆盖上传在 auto 下优先使用 exec 而非 SCP。远端分别需要 SFTP subsystem、scp 命令或 POSIX shell/cat/wc；发布还需 mv 或 ln/rm。无 PTY、无 Bash login 包装，不污染终端历史。只在传输开始前选择后端，失败不自动重传。
- overwrite 默认 false。上传先写同目录临时文件，再发布；失败返回 temporary_path 供检查/清理，可能保留部分临时文件。SFTP 无覆盖发布需要 hardlink 扩展，覆盖需要 posix-rename 扩展，不支持时明确失败。下载先写本地临时文件，成功才发布；无覆盖发布需要本地硬链接支持。
- 单次传输最多 110 秒；超时/取消可能关闭共享 SSH transport，影响其他任务，须检查状态并显式 reconnect。bytes_transferred 是本地复制确认数，不是远端持久化证明；缺失 SSH exit-status 返回失败/未知，不假定成功。
- read_only 阻止上传及新建隧道，允许下载。既有隧道继续运行；read_only 不检查隧道中的应用协议。

`sess_tunnel_open` 参数为 session、type、bind_host（默认 127.0.0.1）、bind_port（默认 0 自动分配），固定转发还需 target_host/target_port。返回 handle 和 listen_address：

| type | 监听位置 | 目标连接位置 |
|---|---|---|
| local | keepsess 本地 | 最终 SSH 服务器 |
| remote | 最终 SSH 服务器 | keepsess 本地 |
| dynamic | keepsess 本地 SOCKS5 | 最终 SSH 服务器 |
| remote_dynamic | 最终 SSH 服务器 SOCKS5 | keepsess 本地 |

例如 `sess_tunnel_open({"session":"s1","type":"local","target_host":"127.0.0.1","target_port":5556})` 后，本地 Python 可连接返回的 listen_address 访问远端 QMP。动态模式仅支持 SOCKS5 无认证 CONNECT（TCP），不支持 UDP、TUN/TAP 或 Unix socket；远程绑定范围受 SSH 服务器配置影响。

`sess_tunnel_list(session)` 查看监听与连接状态；`sess_tunnel_close(session, handle)` 关闭监听、连接并释放记录。每会话最多 16 个记录，全局最多 64 个监听，每监听最多 64 个 socket。建连/握手/写阻塞有界，空闲读取上限 2h，活跃连接阻止会话空闲回收。断线/reconnect 关闭旧隧道，不自动恢复，关闭的记录可显式释放。SSH 转发建连阻塞超时时可能关闭共享 transport。
