# keepsess 开发约束

- SSH 复用 golang.org/x/crypto/ssh。用户最新要求：Telnet 使用内部实现，禁止重新引入要求更高 Go 补丁版本的 Telnet 库。
- Go 1.22（固定目标补丁版本 1.22.1），纯 Go、CGO_ENABLED=0；依赖不得要求高于 Go 1.22。
- 交叉编译 linux/amd64、linux/arm64、windows/amd64。
- 进程内持久 SSH/Telnet 连接；无需重启恢复。sess_exec 仅适用于 SSH mode=exec，默认非交互，interactive=true 显式保留 stdin、pty=true 必须搭配 interactive，stream 直接报错且不发送输入；标准 SSH 优先 exec，以 SSH exit-status/exit-signal 判断完成，合并 stdout/stderr，禁止结束标记猜测。sess_exec 使用独立 exec_total_timeout_ms（默认 120000ms，1..300000ms），忽略 read_total_timeout_ms/read_idle_timeout_ms/end_pattern；term_on_timeout 默认 true，完成等待超时尝试一次 TERM，不保证退出；false 仅结束等待。普通 read 超时、取消收集和输出上限不发送信号。后续 read 使用 handle 和独立 read 配置。
- API 采用 22 工具：配置、exec、四种 send/sendline、四种 read、连接管理，以及 task_list/status/wait/signal/release/close_stdin。读取 total=30000ms、idle=500ms，end_pattern 默认禁用，任一条件返回；配置可按调用覆盖且不污染全局。任何调用不得无限等待。
- 独立端点静默保护、连接/握手/写入超时、RPC 上限、stdio 并发调度、有界队列及有界历史，防止半开连接和队头阻塞。整个调用小于两分钟，read_total_timeout_ms 最大 110000；正常读超时不关闭连接。exec 不设无输出静默保护；无活动调用会话按 session_idle_timeout_ms 回收（默认 2h，0 禁用），不能因分析/审批无输出在 110s 自动关闭。
- 默认增量输出；all/tail 可读取保留的历史，并明确报告截断/丢失；缓慢端点必须返回反压信息，禁止无限输入堆积。
- 断开连接要在工具结果中明确提示重连；不自动重放可能产生副作用的命令。
- 无子命令只显示用途；serve 显式启动；help 供人类，mcp-skill 和 install-skill 供 AI。
- 可选 --log JSONL 审计 AI 输入；认证字段脱敏；审计文件权限 0600，stdout 专供 MCP。
- 必须测试：真实 SSH 协议退出与双流、Telnet IAC 分包、交换机模拟器（分页、模式、慢输出）、半开端点、stdio 并发/取消/慢消费者与反压、race。
- 用户已授权创建公开 GitHub 仓库与推送本项目。发布前不得包含凭据、审计日志或本地配置。

- 现场反馈：缺失 SSH exit-status 不等于 transport 故障；返回 unknown outcome/warning，不能假定成功或自动重放。
- SSH 网络设备推荐 mode=stream + sendline_text（line_end 默认 CR），保留原始 data 精确语义；禁止为了修复疑似 CRLF 问题静默修改 raw data。模型可用 send_escaped 显式解码一层可见转义，去重与上限按解码后的字节计算，非法转义不得部分发送。
- 空输出/等待超时不代表未执行；返回 input_state/input_bytes/written_bytes。0.3 不再接受 input_id，不归因输入输出、不自动重试；stream 的 sent/written_bytes 只确认本地 writer 写入，不证明端点收到、密码被接受或 shell 就绪；不改为含混的 accepted。start=end/连续空输出均不能判断认证结果，不可因此重发凭据或补发回车；unknown 的 written_bytes 只是已确认下界。close 明确丢弃缓冲并释放句柄。
- 单次调用双警告不能直接定性为输入重复发送；要区分线上字节、终端 Enter 解释、回显与增量输出。未实测真实设备时不得声称已复现特定厂商问题。

- read_only 默认 false，仅 connect/configure 可设置，操作级覆盖一律拒绝。开启后 send/exec 返回 not_sent 与 read_only 错误，不消费输出。修改与应用写入同步，写入中返回 busy 且配置整体不变；写入前重查当前只读状态。协议协商/keepalive 继续，已运行命令不停止；防误操作，不作为权限边界。

- SSH 主机密钥默认不校验，不自动读写 known_hosts；显式 host_key/known_hosts 才启用且失败报错。兼容优先级 insecure=true > host_key > known_hosts > 默认跳过；insecure=false 本身不启用校验。

- sess_reconnect 显式复用原连接参数与当前配置，保留句柄，关闭旧连接并清空旧缓冲，不重放应用输入。读写/配置/exec 活动时 busy；失败保留参数供显式重试；close/close_all 取消重连并防止句柄复活。认证参数只在进程内保留，不出现在结果、配置或审计中。

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
- SSH connect 支持 jumps 数组，近端在前，最多 8 跳，跳板端口默认 22；每跳独立认证/主机校验，使用 x/crypto/ssh direct-tcpip，不执行远端 ssh 命令。整链共享 connect_timeout_ms，取消/失败关闭链路；重连私下复用完整链路，不重放。跳板凭据不进入结果/配置，审计递归脱敏；Telnet 不接受 jumps。
