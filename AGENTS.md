# keepsess 开发约束

本文件记录当前实现约束；API 完整参考见 docs/usage.md，运行时定义在 internal/mcp、默认值在 core.DefaultConfig。VALIDATION.md 与 docs/field-report.md 的历史节只记录当时行为，不覆盖当前规范。CLI 内嵌 internal/cli/skill.md 与分发 skills/keepsess/SKILL.md 必须同步；更改接口后同步检查 Python 生成方法与文档。

- SSH 复用 golang.org/x/crypto/ssh。用户最新要求：Telnet 使用内部实现，禁止重新引入要求更高 Go 补丁版本的 Telnet 库。
- Go 1.22（固定目标补丁版本 1.22.1），纯 Go、CGO_ENABLED=0；依赖不得要求高于 Go 1.22。
- 交叉编译 linux/amd64、linux/arm64、windows/amd64。
- 进程内持久 SSH/Telnet 连接；无需重启恢复。sess_exec 仅适用于 SSH mode=exec，默认非交互，interactive=true 显式保留 stdin、pty=true 必须搭配 interactive，stream 直接报错且不发送输入；标准 SSH 优先 exec，以 SSH exit-status/exit-signal 判断完成，合并 stdout/stderr，禁止结束标记猜测。sess_exec 使用独立 exec_total_timeout_ms（默认 120000ms，1..300000ms），忽略 read_total_timeout_ms/read_idle_timeout_ms/end_pattern；term_on_timeout 默认 true，完成等待超时尝试一次 TERM，不保证退出；false 仅结束等待。普通 read 超时、取消收集和输出上限不发送信号。后续 read 使用 handle 和独立 read 配置。
- API 采用 30 工具（含 sess_help、sess_rename/sess_task_rename）：配置、exec、四种 send/sendline、四种 read、连接管理、task_list/status/wait/signal/release/close_stdin、get_file/put_file 和 tunnel_open/list/close。读取 total=30000ms、idle=500ms，end_pattern 默认禁用，任一条件返回；配置可按调用覆盖且不污染全局。任何调用不得无限等待。
- 独立端点静默保护、连接/握手/写入超时、RPC 上限、stdio 并发调度、有界队列及有界历史，防止半开连接和队头阻塞。整个调用小于两分钟，read_total_timeout_ms 最大 110000；正常读超时不关闭连接。exec 不设无输出静默保护；无活动调用会话按 session_idle_timeout_ms 回收（默认 2h，0 禁用），不能因分析/审批无输出在 110s 自动关闭。
- 默认增量输出；all/tail 可读取保留的历史，并明确报告截断/丢失；缓慢端点必须返回反压信息，禁止无限输入堆积。
- 断开连接要在工具结果中明确提示重连；不自动重放可能产生副作用的命令。
- 无子命令只显示用途；serve 显式启动；help 供人类，mcp-skill 和 install-skill 供 AI。
- 可选 --log JSONL 审计 AI 输入；认证字段脱敏；审计文件权限 0600，stdout 专供 MCP。
- 必须测试：真实 SSH 协议退出与双流、Telnet IAC 分包、交换机模拟器（分页、模式、慢输出）、半开端点、stdio 并发/取消/慢消费者与反压、race。
- 用户已授权创建公开 GitHub 仓库与推送本项目。发布前不得包含凭据、审计日志或本地配置。

- 现场反馈：缺失 SSH exit-status 不等于 transport 故障；返回 unknown outcome/warning，不能假定成功或自动重放。
- SSH 网络设备推荐 mode=stream + sendline_text（line_end 默认 CR），保留原始 data 精确语义；禁止为了修复疑似 CRLF 问题静默修改 raw data。模型可用 send_escaped 显式解码一层可见转义，上限按解码并编码后的字节（含追加行尾）计算；不做输入去重，非法转义不得部分发送。
- 空输出/等待超时不代表未执行；返回 input_state/input_bytes/written_bytes。0.3 不再接受 input_id，不归因输入输出、不自动重试；stream 的 sent/written_bytes 只确认本地 writer 写入，不证明端点收到、密码被接受或 shell 就绪；不改为含混的 accepted。start=end/连续空输出均不能判断认证结果，不可因此重发凭据或补发回车；unknown 的 written_bytes 只是已确认下界。close 明确丢弃缓冲并释放句柄。
- 单次调用双警告不能直接定性为输入重复发送；要区分线上字节、终端 Enter 解释、回显与增量输出。未实测真实设备时不得声称已复现特定厂商问题。

- read_only 默认 false，仅 connect/configure 可设置，操作级覆盖一律拒绝。开启后 send/exec 返回 not_sent 与 read_only 错误，不消费输出。修改与应用写入同步，写入中返回 busy 且配置整体不变；写入前重查当前只读状态。协议协商/keepalive 继续，已运行命令不停止；防误操作，不作为权限边界。

- SSH 主机密钥默认不校验，不自动读写 known_hosts；显式 host_key/known_hosts 才启用且失败报错。兼容优先级 insecure=true > host_key > known_hosts > 默认跳过；insecure=false 本身不启用校验。

- sess_reconnect 显式复用原连接参数与当前配置，保留句柄，关闭旧连接并清空旧缓冲，不重放应用输入。读写/配置/exec 活动时 busy；失败保留参数供显式重试；close/close_all 取消重连并防止句柄复活。重连使用的完整认证参数仅在进程内保留，不出现在结果、配置或审计中；成功连接的目标及跳板密码另存私有连接资料，私钥与私钥口令不落盘。

- exec 同步调用只与同一 session 的同步调用互斥，busy/not_sent 不排队；异步与同步、异步与异步可并行。同步调用任何返回都释放名额，即使超时 TERM 被忽略、失败或旧程序仍运行。资源/服务器上限仍可拒绝新任务。
- 每次 exec 独立 taskN handle、channel、输出游标与退出状态；正常同步完成且输出已返回完毕时省略 handle，其他已启动/不确定结果返回 handle。完成历史可通过 task_list 查询，任务句柄不复用。四个 read 工具 session（stream）与 handle（exec）二选一。
- 每会话未完成任务 8、记录 16；全局未完成 64、记录 128；每任务历史 1MiB。完成记录保留 30min，可因容量提前淘汰最旧且无活动操作记录；过期明确报错。运行中/未知任务不因容量淘汰。task_release 只释放无活动操作的完成任务。
- 手动/自动信号受 read_only 保护。信号请求 sent 不证明收到或退出，信号写入上限 1s，计入 115s RPC 预算；卡住时关闭共享 transport 并明确报告受影响任务未知结果，不自动升级 KILL。exit_signal 独立报告，不把合成状态码当作远端 exit_code。
- 收集输出为空时增加 comment，区分无新数据、超时、已完成、不完整字符、预算不足、历史范围和故障；不宣称设备肯定没发数据、执行/认证成功。collect_output=false 不生成空输出说明。

- SSH 协议 keepalive 默认 30s，回复时限 10s，单探测在途；否定回复也算响应。探测不发送终端输入、不刷新本地空闲，配置仅 connect/configure 可改；超时关闭 transport，任务未知。断连返回分类与原始错误，保留句柄优先 reconnect；释放句柄最多保留 128 条/2h 无凭据原因记录。
- SSH connect/reconnect 仅一次返回紧凑 peer 软件标识和 SHA256 指纹，重连变化标记 host_key_changed；不执行身份命令、不改变主机校验策略，不把线索当物理身份或系统版本证明。
- 输出默认分页 64KiB，max_output_bytes 最大 512KiB；历史仍有界。命令完成等待默认 120s、最高 300s，RPC 仍小于 2min；RPC 预算先到只返回 handle、不发 TERM，配置的完成等待超时才适用 term_on_timeout。

- interactive/pty 仅 exec 启动参数，默认 false；普通 stdin EOF 行为保持，已启动任务不能补开。四种 send 的 session（stream）与 handle（interactive exec）二选一；task_close_stdin 发送 SSH EOF、幂等、不消费输出，不以 Ctrl-D 字节代替。任务输入受当前 read_only、编码/上限/反压/写超时保护，每任务单写者与独立读者可并行；写入中改只读 busy，卡住/取消关闭共享 transport 报未知。PTY 影响行规程与双流，不自动更改收集/超时配置。

- 所有 MCP 工具配置输入均在顶层，schema 不再暴露 config 对象；旧嵌套 config 仅兼容接收，顶层优先。connect/configure 修改会话默认值，操作参数仅本次覆盖；连接级/read_only 限制保持。结果中的 config 继续用于展示。
- connect 支持 ssh_jumps 数组（不再接受旧 jumps），近端在前，最多 8 跳，跳板端口默认 22；每跳独立认证/主机校验，使用 x/crypto/ssh direct-tcpip，不执行远端 ssh 命令。整链共享 connect_timeout_ms，取消/失败关闭链路；重连私下复用完整链路，不重放。跳板凭据不进入结果/配置，审计递归脱敏；最终目标可为 SSH 或 Telnet；Telnet 仅 stream，继续使用内部 Telnet 编解码，登录输入不自动发送；不将跳板身份作为 Telnet 设备 peer，SSH 保活探测最后跳板，转发通道写入须有界（含协商回复），超时关闭根 transport。

- 会话/任务支持启动 name 和 sess_rename/sess_task_rename 别名更新；稳定 sN/taskN ID 不变且可继续引用。名字 1..64 ASCII alphanumeric/._-，首位 alphanumeric，保留 sN/taskN；会话与全局任务各自唯一，连接中预留会话名，exec 发送前原子预留任务名，重复不得发送。释放记录后可复用别名，长期操作推荐稳定 ID。
- 进程启动生成短随机 Bitcoin Base58 instance_id（旧归档 ID 继续兼容），在 initialize._meta、connect/reconnect/list、未知句柄错误及人类 Web/历史中展示，不增加 expected_instance_id 或强制校验；未知句柄提示可能关闭/过期/实例变化，先 list，不重放。本轮无 sess_expect 工具。
- sess_exec 可省略 session，仅在恰好保留一个会话时推断（包括已断开或 stream 会话），不按在线/模式/最近使用筛选。零会话先 connect，多会话明确指定；失效显式目标不回退，断开不自动重连。任务操作仍须 handle，task_list 可列全部。
- exec 默认 login=true，Bash 非交互登录 shell，stdin/PTY 默认保持；缺 Bash 失败、不回退。profile 后应用 env/cwd，然后 command，设置失败不执行 command；shell 引用必须正确，env 审计脱敏。login=false 且无 cwd/env 原样提交，否则 bash -c；每任务独立，包装后的线上命令计入输入上限。
- 每次 task_wait 新预算；返回 wait_elapsed_ms、started_at、task_elapsed_ms（完成后冻结）及 timeout_source=rpc_budget/exec_wait/caller_deadline，普通 read 为 read_total；只有配置的 exec_wait 超时可 TERM，RPC/调用截止不发信号。跳板失败结构化报告 1-based hop_index/host/port/from_host/stage/target，来源仅配置逻辑主机，不推断出口 IP。
- 输出 strip_ansi/include_pattern/exclude_pattern 默认关闭，只作用投影，保留 raw history/偏移，过滤行推进游标；end_pattern 仍匹配解码原文，exec 仍忽略。投影每页原始扫描 64KiB、行 16KiB 有界片段，完整字符/渲染转义不分割；未完成 ANSI 保留，极长控制序列 view_incomplete 提示 raw 检查。过滤保留行/片段整体返回，过小页面报告 minimum_output_bytes；filtered_bytes/lines、stripped_bytes 与 dropped_bytes 区分，空输出说明过滤/不完整等原因，任何扫描/等待不可无界。

- MCP 发现直接保留完整工具与所有适用参数说明（含嵌套字段），覆盖语义/默认值/范围/互斥/执行与转义边界，获得工具定义即可调用，不依赖先查 help。只隐藏各工具不适用的配置，不修改运行时参数接受行为；发现和详细帮助从同一完整定义派生，默认值来自 core.DefaultConfig，初始化说明保留核心规则。
- sess_help 无参数返回简短目录，topic 为内置主题或工具名，topic+parameter 单字段，query 固定代码全文搜索（中英文别名、确定性排序）；query 与 topic/parameter 互斥，query <=512 字节/最多16词，默认3条、limit1..5，每摘要约220字符。只搜索内置文档，不访问网络/文件/会话/凭据、不改变运行状态，未命中明确返回。参数帮助不能丢失执行边界/逃逸层次信息；目录/搜索不倾倒完整手册。

- 人类 Web 入口由每用户共享 daemon 提供；首个 serve 自动启动、后续进程主动注册，SSH/Telnet 仍由原进程持有。daemon 空闲不退出，重启只恢复观察，绝不重放输入；进程退出转只读归档，UI 标出所有活动实例/PID。服务端设置从 ~/.keepsess.toml 读取，网页显示偏好使用浏览器 localStorage；内部令牌/运行状态私有保存，禁止发布。
- 实例首次 connect 成功提示 AI 用 sess_configure(instance_name=...) 命名，不新增命名工具。全部 30 个工具统一可发现，不分 standard/full，不提供 tool_profile 开关；旧配置 [tools].profile 兼容读取但忽略。

- sess_send_escaped 和 sess_sendline_escaped 均可发现；前者精确解码一层且不追加行尾，后者解码后追加一次配置的行尾。

- daemon v2 起固定 Protobuf wire 契约与既有能力语义，新增执行语义先协商能力；不得复用字段或破坏旧能力。用 Buf FILE 比较冻结基线与发布版本。daemon upgrade 使用当前二进制私有副本、生命周期锁与认证身份健康检查，不重放输入；v1 Agent 首次迁移需自行安排重启。

- 文件传输 sess_get_file/sess_put_file 复用 SSH exec/stream（含跳板），Telnet 不支持。本机路径指 keepsess 主机；下载 dst 可省略并返回绝对临时路径。backend=auto/sftp/scp/exec，auto 只探测后端，已开始传输不重试；无 PTY/login 包装。overwrite 默认 false，仅普通文件，无递归/续传，110s 预算；上传同目录暂存后发布，失败可留 temporary_path。SFTP 无覆盖需 hardlink 扩展、覆盖需 posix-rename。上传受 read_only 保护，下载允许；取消可关闭共享 transport，需报告任务未知结果。
- 隧道 sess_tunnel_open/list/close 仅 SSH；type=local/remote/dynamic/remote_dynamic，SOCKS5 仅 CONNECT。local/dynamic 在 keepsess 监听、从最终 SSH 服务器访问目标，remote/remote_dynamic 相反；local/remote 必填目标。绑定默认 127.0.0.1:0，远端受 GatewayPorts 限制。每会话16监听、全局64、每监听64连接；隧道 handle 不作 task handle。只读阻止新建但不停止已有流量；断连/重连关闭隧道且不恢复。
- 人类实例名与 AI instance_name 独立持久化，支持活动/归档；人类名优先，清空恢复 AI 名。空实例无 session 不显示，连接失败也算 session；仅在写入者结束且历史完整可确认时清理空目录，不因 daemon 暂时断连删除。实例活动优先，会话按在线/连接中、关闭/断开、首次失败排序，首次失败默认折叠，排序不抢选中。
- Web exec 默认会话连续视图，按事件顺序展示所有命令/输出/完成，无20任务分页与每任务行配额；共享整体滚动预算，尾部跟随、上翻保持，新任务不切换连续视图。移除单任务窗口及主页面输入；命令历史默认折叠并支持定位，来源栏默认收起为按 task 连续的色带，stdout/stderr 切换不断开，命令与同 task 连续输出连接，任务/完成/省略边界圆角断开；Canvas 色带覆盖当前视口，软折行不断开，明暗主题使用不同调色板；点击浮层展开来源及逐行接收时间，不挤窄终端或重排历史，缺失时间未知、不用回放时刻补造。统一查找面板列匹配行并可跳转，主 exec 支持任务内前后各0..50行上下文和仅显示匹配行；缓存按滚动预算保留逻辑行/16KiB片段，另限32MiB，淘汰提示范围缩减；修改条件不重新拉取磁盘历史。人类输入在新浏览器标签页，exec 明确启动独立登录 Bash/PTY，stream 操作原会话；关闭页面不终止或重建 shell、不重发输入，恢复重新锁定；输入页右上角“只读”按钮文字固定，aria-pressed=true 表示只读，false 表示可写，观察页保持只读且按钮禁用；显式结束尝试 TERM。默认滚动5000行，可配100..50000，仅浏览器保存；不改变 MCP 游标或磁盘历史。
- Web 支持桌面 Chromium，离线 WebGL 优先、失败/上下文丢失回退 DOM；所有终端页面以 Ctrl+F/图标/右键打开统一查找结果面板，点击匹配行跳原文，不发送输入或自动聚焦可输入终端；仅搜当前页已加载内容，不搜索未加载磁盘归档，覆盖/淘汰/屏幕切换使旧结果失效。夜间/日间/系统主题、10..24px 终端字号（默认14px，周边界面字号11..18px、默认13px，独立统一缩放）、高亮、过滤及布局偏好统一以带版本的 localStorage 保存，可导入导出 JSON，不含凭据或原始命令/输出；网页不回写服务端高亮。字体离线内嵌。临时本地回显不进入终端缓冲/复制/历史，当前终端任意非空远端字节或锁定/切换/断连/失败清空，不改变线上输入、不补发。
- 历史原样记录命令/cwd、终端输入（可能含密码）与输出，排除结构化认证/env；与可选 AI 审计独立。默认不自动删除，history.max_bytes=0 不限，非零是按进程启动基数计算的容量保护，不是多进程严格磁盘配额。记录故障不得阻塞远端；不要将本地历史、令牌、配置及安装备份发布。

- 连接管理：成功 connect/reconnect 按 instance/session 保存私有连接资料，成功重连更新时间，失败不覆盖；目录 0700、文件 0600，目标及全部跳板密码明文保存，排除私钥内容/口令。资料与普通事件/终端历史分离，关闭 Web 注册或 daemon 不可用仍尝试保存；有界异步写入，故障不阻塞远端且明确报告。浏览器独立列表支持活动/归档，默认只返回密码存在性，勾选后经同源 POST 获取，取消/切换/关闭清除页面明文，不进入 localStorage。Telnet 仅人工补录目标用户名/密码，明确未验证，不发送输入、不修改运行时/重连参数，成功重连保留补录。旧实例不反推密码；资料禁止发布。
