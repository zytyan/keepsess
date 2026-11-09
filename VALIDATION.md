# IP 反代与相对路径入口

- 修复 HTTP Host 白名单绑定后端监听 IP/端口导致内网 IP 反代和端口转发被误拒绝的问题；允许合法 IPv4/IPv6/localhost 入口的实际访问端口，仍拒绝未声明的 DNS 名称。
- Origin 按浏览器可见的主机和端口规范化比较，支持 IPv6 等价写法、大小写、默认端口与 HTTPS/WSS 在反代终止后走 HTTP；不依赖后端 socket 的协议或信任任意 Forwarded 头。
- 页面资源/API/WS 继续使用相对路径；保留浏览器 Host（含端口）时，通过 IP 的反代无需 --web-public-url。保留前缀用 --web-base-path，剥离前缀内部维持 /；MCP 未配置公开地址时仍诚实返回本机监听入口。
- 测试包含 Chromium 通过保留 Host 的 IP HTTPS 反代访问完整页面/API/WS（不配置 public URL），并覆盖 IPv4/IPv6/localhost 与不同外部端口、默认端口和 IPv6 规范化、跨 IP/端口拒绝、DNS/非法 Host 与转发头伪造拒绝，以及无公开 URL 的 HTTP/HTTPS 反代保留/剥离前缀、相对资源/API 和 WS/WSS。

---

# 会话总览与多命令同屏

- 二级侧栏增加可切换的会话总览，按 taskN 顺序纵向显示命令、各自输出与状态；默认单任务视图保持。总览始终只读，打开单任务后仍需显式解锁才能操作。
- 最多 20 个预览终端，单卡片最多展开 12 行并独立滚动；5000 行前端滚动历史预算按预览分配，磁盘历史不变。更早/较新/最新分页可读取已经从 core 淘汰的任务；PTY 提供单任务入口。
- 输出、ANSI 与增量字符解码按任务隔离；总览尾部跟随新任务/输出，上翻时保留阅读锚点，前方卡片增高不推走当前阅读内容。切换页面取消旧渲染，不消费模型游标或重放输入。
- Chromium + 真实 SSH 协议模拟器覆盖短命令同屏、跨任务交错 UTF-8 分包、长输出上限、终端查询不写入、前方内容增高时保持位置、总览/单任务切换、20 条上限与历史翻页、停留/跟随新任务和 PTY 入口；原终端和自动跟随浏览器验收同时回归。

---

# 二级任务侧栏与自动跟随

- exec 任务从顶部标签移到会话旁的二级侧栏，展示 ID、别名、状态和命令摘要；按 taskN 创建顺序排列。stream 隐藏任务侧栏，仍使用会话列表。
- 输出前记录是否位于尾部，写入完成时仅在用户未改变滚动位置的情况下滚到底部。采用 xterm 实际单元格尺寸适配终端高度，避免最后一行被裁切。
- 只有查看最后一个 task 且停在尾部时，新任务才自动选中；查看旧任务、上翻历史、存在待发送输入或待回执操作时不抢占。自动切换仍恢复只读，批量新建任务时继续跟随最新任务。
- 可选 Chromium 用真实 SSH 输出验证二级侧栏位置、尾部跟随、上翻后保持、写入回调期间滚动不被抢回、旧任务尾部不跳转、最新任务尾部自动切换、连续任务和网页执行命令的条件跟随；普通构建仍仅需 Go 1.22.1，构建依赖说明已补充到 README。

---

# 内置 Web 终端与持久历史

- `serve` 默认启动本机免登录 Web，浏览器/服务端默认只读，解锁与每个 WS 连接绑定，仍遵守会话 `read_only`。xterm.js 5.5.0 离线嵌入，前端滚动历史默认 5000 行，可调并保存在浏览器；纯 Go 构建无需 Node。
- core 独立事件出口记录生命周期、原始输入/输出与写入回执，不消费 MCP 输出游标。私有分段日志跨进程保留，关闭/淘汰不删除历史；归档索引可从完整记录重建，旧实例只读、未完成任务标为未知。结构化认证/env 不保存；x/crypto 拒绝 exec 时包含包装命令的错误只在历史投影中隐藏，避免 env 随错误落盘。按用户选择，终端输入（包括密码）和输出原样保存。
- 覆盖日志分页/跨段无缺口、索引恢复与损坏尾部、文件权限、容量/队列故障不阻塞实时输出、慢订阅者上限；覆盖关闭已断开会话、任务 transport 故障未知结果、输入编码覆盖和凭据排除。
- Web/WS 覆盖端口自动重试、默认只读/重新连接只读、会话只读保护、exec 双流及 MCP 独立游标、关闭记录、增量补齐、Host/Origin 和无效根路径；真实 TLS 反代覆盖保留与剥离前缀、HTTPS/WSS。浏览器携带实例标识，服务进程替换后拒绝旧页面写入，不重放输入。
- Chromium 验收覆盖滚动历史默认值与修改持久化、exec 退出码、已关闭会话、stream 键盘操作、精确输入字节无重复、Unicode 输入、快速切换终端时取消旧回放且不污染新游标、旧实例回放和分包 GBK；终端查询自动回复不作为远端输入。SSH 模拟器无 PTY 行规程，明确区分 Enter 的原始 CR 与测试中补充的 LF，不据此推断真实设备行为。
- 已运行 Go 1.22.1 全量测试、全量 race、含 110s 等待的长时间 race、go vet，并完成 CGO_ENABLED=0 的 linux/amd64、linux/arm64、windows/amd64 编译。可选浏览器测试通过 `KEEPSESS_BROWSER_TEST=1 go test ./internal/web -run 'TestBrowser(Acceptance|Following|Overview)' -v` 运行，需要单独安装的 Node/Playwright/Chromium。

---

# 精简发现与渐进帮助

- 新增 sess_help，共 25 工具。发现列表独立投影完整运行时定义，保留关键类型/默认值/互斥/安全提示，只隐藏不适用配置并压缩重复说明；现有运行时参数接受/校验、协议行为和默认值不变。数字配置的 integer|string 联合保留算术字符串能力，初始化说明仅保留常用全局约束。
- sess_help 无参数返回主题/工具目录，topic 或 topic+parameter 返回一份文档/字段；参数详情从相同定义派生，配置默认值来自 core.DefaultConfig。静态全文索引按名称/标题/关键词/正文确定性排序，支持常见中英文映射；query <=512字节、最多16词，默认3条、limit1..5，摘要约220字符。无匹配明确返回；不访问会话、凭据、文件或网络，不改变状态。
- 官方 DeepSeek-V4.1-Flash tokenizer 与 encoding.py（revision 314113f8a7dadb388215396ba5fbee2c610a04d6）本地计数：工具+初始化说明由 20,486 降到 9,337 tokens（-54.42%，新增 help 已包含）。此范围不含可选完整 skill、其他系统提示/历史，不等同线上 API usage。独立 JSON 帮助正文样例：目录392、RPC超时搜索234、env单字段61、timeouts主题308 tokens；客户端包装另算。未调用收费推理 API，也未做实际模型成功率 A/B。
- 覆盖所有发现字段的帮助/默认值一致性、静态定义不变性、查询排序/中英文/长度/结果上限/无匹配/错误组合、无 Manager 的只读帮助，以及“搜索→单字段→SSH connect→异步 exec→task_wait 双流退出”的 stdio 流程。全量测试、race、go vet 通过；Go1.22.1、CGO_ENABLED=0 三目标构建通过。README、CLI、两份 skill 与开发约束已同步。

---

# 命名、实例诊断、exec 环境与输出投影

- 新增 sess_rename/sess_task_rename，共 24 工具；connect/exec 的 name 是可变别名，稳定 sN/taskN ID 仍有效。会话名覆盖待连接预留，任务名在 exec 请求前原子预留，重复不发送；引用、配置、列表、关闭和重连支持别名。任务名进程内全局唯一，别名随保留记录释放，长期操作推荐稳定 ID。
- 每进程 8 字符随机 instance_id，在 initialize._meta、connect/reconnect、列表和未知句柄错误显示；不增加 expected_instance_id，也不要求 sess_expect。未知句柄提示记录关闭/过期或实例变化、先 list 不重放。
- task_wait 每次独立预算，新增 wait_elapsed_ms、started_at、task_elapsed_ms（完成后冻结）及 timeout_source。绑定层标记实际 RPC 截止，即使关闭 TERM 保留也能区分 rpc_budget/caller_deadline；只允许配置的 exec_wait 超时尝试 TERM。模拟预算耗尽后第二次 wait 收到新增输出并正常完成，确认只启动一次、不发送 TERM；未复现“沿用上次 deadline”的持久状态问题，不据此宣称已复现现场故障。
- exec 默认 Bash 非交互登录 shell（login=true），仍默认 stdin EOF/无 PTY；profile 后应用已引用的 env/cwd，设置失败不执行 command，env 审计脱敏；login=false 且无 cwd/env 原样提交，否则 Bash 非登录包装，不重试或自动回退。真实 SSH 协议加本机 Bash 测试验证 login_shell、引号/特殊字符、cwd 失败和退出码；普通模拟器仅解包命令标签，不能冒充远端真实 Bash 验证。
- 跳板错误新增 hop_index/host/port/from_host/stage/target 与近端到远端顺序说明；来源是配置逻辑主机而非出口 IP。read 同传 session/handle 时明确提示 exec 删除 session，stream 删除 handle；不静默忽略。现有 SSH/Telnet stream direct-tcpip 支持保留，没有新增 -L/-R/-D 监听管理。
- strip_ansi/include_pattern/exclude_pattern 只投影返回值，保持原始历史和偏移，end_pattern 仍匹配解码原文。投影原始扫描每页 64KiB，长行约 16KiB 片段，过滤行整体返回；控制序列/字符分包、超时保留、history 独立投影、过滤空输出、扫描预算推进、过小页面及原始历史恢复均有测试。超长不完整 ANSI 或匹配截断控制序列返回 view_incomplete，提示显式 raw 检查；不虚构完整终端渲染。
- 新增命名并发/待连接去重、MCP 新参数与实例诊断、结构化路由错误、env 审计脱敏测试；原交互测试改为等待实际回显，避免依赖 10ms 到达假设。Go 1.22.1 全量测试、race、go vet 及 CGO_ENABLED=0 三目标构建通过；两份 skill、README、CLI、MCP 和开发约束同步。未实测用户 QEMU/外部设备现场。

---

# ssh_jumps 与 SSH 跳板到 Telnet 目标

- 跳板参数统一改名 ssh_jumps，schema/运行时均不接受旧 jumps（包括两者同时出现）。protocol 表示最终目标：SSH 支持 exec/stream，Telnet 仅 stream，复用内部 Telnet 实现；不自动发送目标登录凭据。
- 提取共用 SSH 路由建立逻辑，SSH 握手与最后一级到 Telnet 的 direct-tcpip 共用连接预算及取消保护。Telnet 的 SSH 保活探测最后一个 SSH 跳板，不推断 Telnet 应用就绪，不返回跳板 peer 冒充目标身份。
- 为不支持原生 deadline 的 SSH 转发通道提供 Telnet 写 deadline 适配，协商回复和应用写入卡住时关闭共享根 transport，返回超时与未知输入结果；普通读取超时不关闭链路。
- 新增真实协议模拟器与 MCP 测试：单/双跳 Telnet、IAC 分包/字面 IAC/准确输入字节、只读仍允许协商、模式与分页、读超时保留链路、exec 拒绝、重连、SSH 保活否定回复/超时、最后一级转发超时/取消/close_all、写 deadline，以及新旧参数/schema 校验。原 SSH 跳板回归仍通过。未实测外部真实设备。
- Go 1.22.1 全量测试、race、go vet，以及 CGO_ENABLED=0 的 linux/amd64、linux/arm64、windows/amd64 构建通过；构建缓存/临时文件迁入项目忽略目录以避开 /tmp 容量限制。README、CLI、MCP 和两份 skill 已同步。

---

# 全部参数扁平化与 SSH 跳板

- 22 个工具的输入 schema 均不再暴露 config 对象；原配置字段在适用工具顶层展开，连接级/read_only 参数只在 connect/configure 暴露。旧嵌套调用仅兼容接收，顶层优先；结果中的 config 仍表示当前会话配置。会话默认值与操作覆盖不互相污染。
- MCP stdio 覆盖顶层 connect/configure、四种 send、四种 read、exec/task_wait 参数、表达式/false/非法值、旧调用优先级、只读拒绝和配置持久性；schema 遍历检查无 config 参数。
- SSH jumps 为近端在前的最多 8 跳 direct-tcpip 链，每跳独立认证/主机校验，默认端口 22；整链共用连接超时，根 TCP 关闭可中断转发与内层握手。显式 reconnect 复用私有参数，close 释放整链。
- 真实 SSH 协议模拟器覆盖单跳/双跳的 exec 双流与 stream、重连、调用方参数隔离、密码/加密私钥、跳板与目标主机密钥失败、第一/第二跳认证失败、转发拒绝、卡住的转发/握手及取消、close_all 取消双跳无句柄复活、断开保留未知结果且重连不重放、失败链路释放。审计验证数组内跳板密码/口令递归脱敏。未实测外部真实跳板主机。
- Go 1.22.1 全量测试、race、go vet 通过；CGO_ENABLED=0 的 linux/amd64、linux/arm64、windows/amd64 构建通过。两份 skill 内容一致，README/CLI/MCP/现场说明及开发约束已同步。

---

# exec 唯一会话推断

- sess_exec 的 session 改为可选，仅在恰好保留一个会话时推断；断开和 stream 会话均参与计数，不按模式、在线状态或最近连接筛选。显式无效 session 不回退，显式空值/null 拒绝，唯一断开会话不自动重连。
- 真实 SSH 与 MCP stdio 测试覆盖零/单/多会话、双流输出、实际 session 返回、显式指定与失效、断开会话、stream 模式拒绝、拒绝时不启动任务；任务 handle 必填和 task_list() 全部任务语义保持。
- Go 1.22.1 全量测试、race、go vet 通过；CGO_ENABLED=0 的 linux/amd64、linux/arm64、windows/amd64 构建通过。README、CLI 帮助、MCP schema/初始化说明及两份 skill 同步。

---

# 显式交互 exec

- sess_exec 新增 interactive/pty，默认均 false；interactive 保留 stdin，pty 必须搭配 interactive，启动前配置，不能事后启用。不改变 collect_output、超时和普通 stdin EOF 默认语义。
- 四个 send 工具支持 session（stream）或 handle（interactive exec）二选一，复用编码/转义/结束符/输入上限/OS 反压/收集语义。每任务单写者和独立读者；同步收集期间可并行 collect_output=false 输入。
- 新增第 22 个工具 sess_task_close_stdin(handle)，发送 SSH EOF、幂等、不消费输出，不以 Ctrl-D 代替；输出/退出状态仍可读取。status/list/结果报告 interactive、pty、stdin_open。
- 输入/EOF 都检查当前会话 read_only，写入期间修改保护返回 busy；写入超时或取消关闭共享 transport、报告 unknown，不重放。普通任务输入错误保持 transport 可用并将 stdin 标为不可用。
- 真实 SSH 测试覆盖默认 EOF、四种发送与字节、PTY 请求、显式 EOF 与 Ctrl-D 区别、只读/非法参数/无 stdin/已结束拒绝、同步读写并行、写入/EOF 超时与取消、PTY 拒绝不启动；新增 MCP 端到端与 schema 目标选择验证。未实测真实 GDB 或操作系统 PTY 行规程，模拟器确认 SSH 请求/字节行为。
- 完整 go test -race ./...、go vet ./...、Linux amd64/arm64 与 Windows amd64 交叉构建、skill 校验及内嵌一致性检查通过；可选 110s 静默测试未重跑。README、CLI、MCP、skill、现场说明和开发约束已同步。

---

# 连接健康、身份摘要与新预算

- 会话空闲保留默认 7200000ms（2h），活跃任务/读写排除；不是固定连接年龄。SSH 协议探测默认 30000ms、单回复截止 10000ms，至多一个在途；否定回复仍为健康，不输入终端字符、不刷新本地空闲，Telnet 保持 TCP keepalive。
- 新增 disconnect_reason 与原始错误保留，区分 reset_by_peer、transport_timeout、keepalive_timeout、ssh_disconnect、remote_eof、transport_error；保留句柄优先 reconnect。释放原因记录最多 128 条/2h，不保留凭据；回收/关闭与 unknown_handle 明确区分，不能把未知归因为设备 reset。
- SSH connect/reconnect 返回一次 peer.server（96 字符上限）、host_key（SHA256），重连变化标记 host_key_changed；不执行命令、不更改默认校验策略，不在普通读写/list 重复。不把旧身份冒充失败重连后的新观测。
- exec/task_wait 完成等待默认 120000ms、最高 300000ms；RPC 仍最多 115s，RPC/调用截止先到只返回任务 handle、不发 TERM。仅配置的完成等待超时适用 term_on_timeout；异步无后台运行计时器。
- 输出默认分页 65536（64KiB）、最高 524288（512KiB），历史仍 1MiB；验证核心分页及实际 MCP 最大页传输（测试客户端有界扫描上限相应扩展）。
- 新增真实 SSH 探测否定回复/无回复/禁用与重连、任务未知结果、密钥切换、重连身份变化、断连分类、原因记录容量/过期、MCP 配置/身份/关闭原因、RPC 截止不 TERM 测试。长空闲和 RPC 上限采用模拟时间/缩短测试预算，未实等两小时或 115s。
- 完整 go test -race ./...、go vet ./...、Linux amd64/arm64 与 Windows amd64 交叉构建、skill 校验与内嵌一致性检查通过；可选 110s 静默测试未重跑，未实测用户嵌入式设备。
- README、CLI、MCP 描述和开发约束同步；修复独立 skill 落后于内嵌版的问题，保留现有 21 工具、task 与信号/空输出说明。第 4 项 exec 交互输入未修改。

---

# 显式重连

- 新增第 16 个工具 sess_reconnect(session)，复用私有原连接参数与当前配置（包括 read_only、connect_timeout_ms），保留句柄，不自动重试或重放应用输入。
- 替换整个连接实例，丢弃旧缓冲/游标，新欢迎信息保持未读；失败、超时或取消保留参数供显式重试。旧协程不写入新连接历史。
- 读写、配置、其他重连及运行中 exec 返回 busy；close/close_all 取消进行中的重连并防止句柄复活。关闭/回收时清除保留的参数；get_config/list/reconnect 结果不包含认证字段。
- 新增真实 SSH exec/stream、Telnet 不重放与历史隔离、只读与配置保留、失败重试、当前连接超时、取消、busy、close/close_all 取消，以及 MCP 端到端测试。
- 完整 go test -race ./...、go vet ./...、Linux amd64/arm64 与 Windows amd64 交叉构建、skill 校验和副本一致性检查通过。可选 110s 静默测试未重跑；未验证真实共享串口服务器的状态恢复。
- README、CLI、MCP 描述、现场说明入口、开发约束与两份 skill 已同步。

---

# SSH 主机密钥默认行为

- 默认不校验主机密钥，不自动读取/写入 known_hosts；显式 host_key 或 known_hosts 才启用校验，失败不回退。
- 保留原优先级：insecure=true > host_key > known_hosts > 默认跳过。insecure=false 本身不启用校验。
- 新增真实 SSH 测试覆盖默认连接执行、默认文件不自动使用/修改、显式无效/缺失文件、正确/错误指纹及兼容优先级。
- 完整 go test -race ./...、go vet ./...、Linux amd64/arm64 与 Windows amd64 交叉构建、skill 校验及内嵌副本一致性检查通过。可选 110s 静默测试未重跑。
- README、CLI、MCP 参数描述、开发约束和两份 skill 已同步。

---

# 会话只读配置

- 新增 read_only 布尔配置，默认 false；connect/configure 可设置，get_config 返回值和说明，所有操作级覆盖（包括 true）都拒绝。
- 开启后四种 send 与 SSH exec 返回 not_sent/read_only，既不发送应用输入，也不消费输出；读取、查询、关闭及必要协议协商/keepalive 保持可用。
- 切换与应用写入互斥：写入中 configure 返回 busy，整份 patch 不生效；实际写入前检查实时只读状态，防止旧快照绕过。已运行命令不停止。
- 新增核心和 MCP 测试，覆盖默认值、非法类型、覆盖拒绝、四种发送、SSH exec、读写恢复、旧配置检查、并发切换原子性及 MCP 错误标记与 Telnet 旁观读取。
- 修正已有 SSH 测试模拟器：hang 等待 channel 关闭，不能把非交互 exec 的 stdin EOF 误认为命令结束。
- 完整 go test -race ./...、go vet ./...、Linux amd64/arm64 与 Windows amd64 交叉构建、skill 校验及副本一致性检查通过；只读测试和 SSH 取消测试重复 5 次通过。未设置 SESSMCP_LONG_TEST，未重跑可选 110s 静默等待测试。同步 README、CLI、MCP 描述、开发约束及内嵌/独立 skill。未验证真实串口服务器的多人广播行为。

---

# 发送回执与静默登录说明

- 同步两份 skill、README、CLI 帮助及 MCP tools/list/initialize 描述：交互式 Bash/shell、console 登录和任意 Telnet 均不使用 sess_exec。
- 保留 input_state=sent，不改为 accepted；明确 stream 本地写入与远端送达/认证/shell 就绪的区别，exec 启动回执与完成状态分开说明。
- 补充 output 为空/start=end、密码不回显、连续空读取的证据限制，以及不补发回车/不重发凭据的一次有界只读等待示例。
- 澄清 DSML 原样字符串参数与 JSON 展示层转义，逻辑参数值与执行字节的区别；没有调用真实模型 API 或特定设备验证认证行为。
- 验证通过：CLI/MCP race 测试、go vet ./...、Linux amd64/arm64 与 Windows amd64 交叉构建、skill 校验及两份 skill 一致性；登录示例正则经 Go RE2 编译和代表性提示文本匹配检查。

---

# 工具命名与 exec 模式限制

- 工具与内部操作统一为 sess_send_text、sess_sendline_text、sess_read_history；15 工具数量不变，旧名称不再注册或接受调用。
- sess_exec 仅允许非交互 SSH mode=exec；SSH/Telnet stream 在获取读写锁或发送输入前直接报错，无 shell 回退。测试验证读写方向均占用时仍返回模式错误，输入缓冲与会话状态不变。
- JSON-RPC 测试覆盖新工具发现、旧别名拒绝、两种协议 stream 的 exec 拒绝，以及拒绝后文字发送仍正常。
- 默认发送并收集输出的说明已前置，Unicode、GBK、Shift-JIS 和字节转义测试保持通过；不增加 ASCII 限制。
- Go 1.22.1 全量短 race、go vet、skill 校验和三目标 CGO_ENABLED=0 构建通过；Windows/ARM64 仍仅验证编译。

---

# exec 等待与读取配置隔离

- sess_exec 使用独立 exec_total_timeout_ms，默认 30000ms，范围 1..110000ms；read_total_timeout_ms、read_idle_timeout_ms、end_pattern 对 exec 等待无效。
- 真实 SSH quiet 命令静默 650ms 后退出，默认 500ms read idle 不会截断 exec；会话级和单次级 read total/idle=1ms、正则匹配 first 的配置也不会提前返回。
- 独立 exec 截止返回 total_timeout/running=true，保留连接与发送回执；单次覆盖不污染持久配置。随后显式 read_until 仍使用自身 read 配置并可读取原命令的后续输出。
- 保留取消、输出预算、缺失 exit-status、非零退出和双流测试；JSON-RPC 端到端验证配置传递与 collect_output=false 跳过等待。
- Go 1.22.1 全量短 race、go vet、skill 校验及 CGO_ENABLED=0 三目标构建通过。Windows/ARM64 仅验证编译，未运行实测。

---

# 0.3.2 keepsess 命名统一

程序入口 cmd/keepsess、Go module github.com/zytyan/keepsess、MCP serverInfo.name、注册项 keepsess、skills/keepsess、安装目录 .keepsess、三平台产物和仓库引用统一改名。按用户要求不实现旧安装器配置迁移；15 个 sess_* 工具名与短句柄保持原语义。

Go 1.22.1 全量 race、go vet、skill 校验及 CGO_ENABLED=0 的 linux/amd64、linux/arm64、windows/amd64 构建通过。四客户端真实安装端到端通过：注册项、程序路径和 skill 全部为 keepsess，删除原下载后仍可 initialize（serverInfo.name=keepsess）并发现原有 15 个 sess_* 工具。Windows/ARM64 未运行实测。

---

# 0.3.1 安装器验证

- 覆盖 Claude Code / Cursor 的 mcpServers JSON、VS Code servers JSONC、Codex mcp_servers TOML。
- 保留其他服务、自定义日志参数、env、启停设置、注释与 TOML 多行文本；对新 Codex 项设置 120s 客户端工具时限。
- 二进制、skill、配置一次安装；相同内容重复执行不产生更新/备份，新二进制写入不同内容哈希目录；原配置备份内容完整，Linux 权限 0600。
- dry-run 零写入；冲突/非法配置拒绝；配置写入失败时回滚先前 skill 更新；file 参数在 flags 前后均可使用。
- 真实二进制端到端验证四种客户端布局：安装后删除原始下载，仍可从配置路径启动并完成 initialize 与 tools/list（15 个工具）。完整 race、go vet 与 Go 1.22.1 CGO_ENABLED=0 三目标构建通过。客户端 GUI 授权与 Windows/ARM64 运行尚未实测。

---

# 0.3 验证记录

环境：Linux amd64，实际 Go **1.22.1**，发布构建 CGO_ENABLED=0。SSH 使用 x/crypto v0.33.0，字符编码 x/text v0.22.0（go 1.18），Linux TCP 队列查询 x/sys v0.30.0；Telnet 使用内部纯 Go 实现。

- 15 工具 tools/list、严格参数校验、新连接参数、配置读取与原子修改、每次调用覆盖不污染默认值。
- 验证默认读取总时限 30000ms、静默 **500ms**；实际静默读取用例不少于 480ms；整数数学表达式、除零/非整数/函数调用拒绝。
- 真实 SSH exit-status 0/7、stdout/stderr 合并、慢命令后台读取、缺失退出状态保留 transport 并报告未知结果、主机密钥验证；stream 与 exec 入口限制。
- SSH/Telnet 交换机模拟器：模式切换、分包提示符、分页、慢输出和正常关闭；Telnet IAC 分包/子协商/字面 IAC 与写入期限测试保留。
- raw/escaped/sendline 的精确线上字节，NUL、CR、Ctrl-C、可见反斜杠、HTML 实体、编码前拒绝非法转义和无法表示的文字。
- UTF-8、GBK、Shift-JIS 跨包字符；非法字节 escape/hex/error 策略；输出预算不拆字符，原始偏移、增量游标、历史分页/尾部与覆盖字节数。
- 读取并发拒绝、读写并行、配置快照、取消保留已收集输出、显式关闭唤醒读取并丢弃缓冲、unknown 写入状态不自动重试。
- 真实满 TCP 缓冲写入有界超时；Linux 实际查询 TCP 排队字节并验证阈值拒绝零发送；系统 keepalive 配置更新。
- stdio 跨会话/控制请求调度、取消、EOF 排空、stdout 慢消费者 3s 截止和全量 race。

Go 1.22.1 全量 race 与 SESSMCP_LONG_TEST=1 长测试通过：core 包 112.674s、MCP 包 114.125s，真实读取调用本身小于 120s，返回 total_timeout 且保留 transport。close_all 取消在途握手的后续回归也通过全量短 race。go vet、skill 校验与 CGO_ENABLED=0 的三个目标构建均通过。Windows/ARM64 仅交叉编译，未在目标系统运行；Windows 非零 backpressure_bytes 明确拒绝，CI 模板包含 Windows 短测试。

---

## 历史验证（0.1 / 0.2，非当前 API）

# 本次验证

环境：Linux amd64；实际使用 Go **1.22.1**（go.mod 最低 1.22.1），SSH 依赖固定版本，Telnet 为内部实现，无外部 Telnet 依赖。

- `GOTOOLCHAIN=go1.22.1 go test ./...` 通过。
- `GOTOOLCHAIN=go1.22.1 go vet ./...` 通过。
- `GOTOOLCHAIN=go1.22.1 SESSMCP_LONG_TEST=1 go test -race ./... -count=1 -timeout 4m -v` 通过。
- 实际无输出半开 TCP 端点经 JSON-RPC stdio 调用，**110.00350824 秒**返回 endpoint_idle 错误；等待过程中另一个 ping 及时返回。
- stdout 消费者不读取：**3.00 秒**触发独立输出超时，无 stdio 死锁。
- 后续新增阻塞输入超时、静默端点写完后继续输入测试，并再次通过全套短测试与 race。
- 真实 SSH 协议服务验证 exit-status 0/7、双流、增量、后台命令不重复提交、主机密钥校验、交互 shell。
- 新增独立 Telnet 线上字节测试：分包 IAC、字面 IAC、重复协商/拒绝、禁用后重启选项、终端类型与窗口大小、子协商转义/超限/损坏、半截控制序列前的输出、应用写入和协商回复超时。
- SSH / Telnet 交换机模拟器验证模式切换、分页、ANSI、分包提示符、慢速串口式输出。
- `CGO_ENABLED=0` 交叉编译 Linux amd64、Linux arm64、Windows amd64。ARM64 / Windows 产物在本环境仅验证编译与文件格式，未在目标系统运行；CI 模板包含 Windows 短测试。

构建命令与 CI 详见 README.md、scripts/build.sh、docs/ci.yml（模板，未启用 Actions）。日志中的时间是本次实测，CI 重跑允许正常调度偏差，但必须小于 120 秒。

本次现场反馈更新另外验证：SSH 缺失退出状态后保留 transport 并可继续执行；一个 SSH 输入只出现一次线上字节；CR/LF 分别触发 Enter 的模拟终端产生双警告而单 CR 仅一次；延迟输出通过 sess_read 取回；input_id 重试去重、改变输入拒绝、部分发送状态未知时仍不重放；新参数经 MCP JSON 解码与 schema 暴露。仍未连接真实华为设备，具体 diagnose 双警告根因未证实。


2026-11-04 背压与会话更新：

- 删除依赖回显的输入限制及 running=true 的交互背压提示；不主动查询 TCP 队列，依据完整 Write 返回放行下一次调用。
- 真实本地 TCP 套接字缩小发送/接收缓冲，验证发送堵塞后有界超时、断连、unknown 状态及 input_id 去重；net.Pipe 验证阻塞、busy 和恢复后再次写入。
- 验证 8192 字节自定义输入限制、超限不发送、非法配置拒绝、短句柄 s1/s2 关闭后不复用。
- SSH/Telnet 显式设置 TCP keepalive 周期参数 15s；未通过抓包验证探测时序，Windows 未运行实测。
- Go 1.22.1 全量短测试和 race 通过；一次 race 首轮出现既有输出静默计时用例调度抖动，复跑通过，后续 keepalive 更新后的全量 race 也通过。
- CGO_ENABLED=0 的 linux/amd64、linux/arm64、windows/amd64 构建通过。

0.2.0 交互与转义更新（2026-11-04）：

- 空闲会话与活动操作分开计时：默认空闲 30min，可设 idle_timeout=0；验证无活动交互连接不受 110s 限制、配置到期回收、禁用回收，以及活动调用和后台 SSH exec 继续受静默保护。空闲回收使用会话操作锁防止与新调用竞争。
- 关闭保留大量未读输出的会话，不返回终端正文、不推进读游标、不报告正常关闭为错误；MCP 路径验证 isError=false。
- 单次输出默认 8KiB，max_output_bytes 上限 64KiB；验证多次增量读取无丢失/重复、显式 offset 不推进默认游标、非法上限拒绝。
- JSON-RPC 到实际 TCP 字节端到端验证 enter、Ctrl-C/D/Z、Esc、Tab、空 line、原始 CR、可见反斜杠-r、HTML 实体，以及 data_escaped 的 CR、十六进制控制字符和只解码一次行为。
- 验证显式解码的 Unicode、任意字节、引号、八进制及非法转义；按解码后字节限制大小，并跨 data/data_escaped 使用相同 input_id 去重。错误不回显输入内容。
- 验证 busy 未发送输入可以沿用相同 ID 重试；常规静默交互不再重复 hint 或强制 next_action=sess_read。
- Go 1.22.1 全量 race（含 SESSMCP_LONG_TEST=1）通过：真实无输出端点仍在 120s 内经 stdio 返回错误，ping 不被阻塞。最终空闲回收并发保护更新后全量短 race 通过；go vet 与 skill 校验通过。
- 三目标 CGO_ENABLED=0 构建通过。CLI/MCP 使用同一 0.2.0+构建标识；Windows/ARM64 仍仅验证交叉编译，未运行实测。

文件传输和 SSH 转发新增验证：
- 真实 SSH 下 exec / 传统 SCP / auto 二进制往返，含 NUL、非法 UTF-8、CR/LF 与引号路径。
- SFTP 上传、自动下载到临时目录、无覆盖发布；取消传输有界返回。
- local / remote / dynamic / remote_dynamic 实际监听及字节往返、SOCKS5 协商、只读拒绝新建、重连关闭旧监听。
- Go 1.22.1 全量 race 回归、go vet；CGO_ENABLED=0 编译 linux/amd64、linux/arm64、windows/amd64。
- 测试使用本机 SSH 协议模拟服务；未实测厂商嵌入式设备。
