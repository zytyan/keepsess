# keepsess 开发约束

- SSH 复用 golang.org/x/crypto/ssh。用户最新要求：Telnet 使用内部实现，禁止重新引入要求更高 Go 补丁版本的 Telnet 库。
- Go 1.22（固定目标补丁版本 1.22.1），纯 Go、CGO_ENABLED=0；依赖不得要求高于 Go 1.22。
- 交叉编译 linux/amd64、linux/arm64、windows/amd64。
- 进程内持久 SSH/Telnet 连接；无需重启恢复。sess_exec 仅适用于非交互 SSH mode=exec，stream 直接报错且不发送输入；标准 SSH 优先 exec，以 SSH exit-status/exit-signal 判断完成，合并 stdout/stderr，禁止结束标记猜测。sess_exec 使用独立 exec_total_timeout_ms（默认 30000ms，1..110000ms），忽略 read_total_timeout_ms/read_idle_timeout_ms/end_pattern；超时只结束等待、不杀命令，后续显式 read 工具仍按 read 配置执行。
- 0.3 API 采用 15 工具：配置、exec、四种 send/sendline、四种 read、连接管理。读取 total=30000ms、idle=500ms，end_pattern 默认禁用，任一条件返回；配置可按调用覆盖且不污染全局。任何调用不得无限等待。
- 独立端点静默保护、连接/握手/写入超时、RPC 上限、stdio 并发调度、有界队列及有界历史，防止半开连接和队头阻塞。整个调用小于两分钟，read_total_timeout_ms 最大 110000；正常读超时不关闭连接。运行中 SSH exec 独立 110s 无输出保护；无活动调用会话按 session_idle_timeout_ms 回收（默认 30min，0 禁用），不能因分析/审批无输出在 110s 自动关闭。
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
