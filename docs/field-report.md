# 0.3 接口迁移与现场反馈

当前可调用的 22 个工具和完整参数以 [README](../README.md) 与 [skill](../skills/keepsess/SKILL.md) 为准。下文保留旧版本现场分析，旧工具名、input_id、wait 等不是 0.3 的调用规范。

0.3 将 SSH exec 与双向 stream 分开，connect 使用 host/port/username/keyfile/mode/config；配置原子更新，单次覆盖只影响该调用。read_idle_timeout_ms 默认 **500ms**，总读取默认 30000ms；正常读取超时不关闭 transport。普通 stream 空闲回收独立为 30min，可禁用。

输入提供 raw/escaped 和是否追加 line_end 的四种组合。escaped 显式处理模型经过 agent 承载的可见转义，编码文字与直接字节区分；不把错误字节简单归因于模型不了解 JSON。输入和输出不建立关联，不再接受 input_id，也不自动重发。使用 sent/not_sent/unknown 和已确认写入字节数判断是否可能已执行。

匹配仅消费到匹配末尾，未读后缀保留；历史读取独立于增量游标。close/close_all 明确丢弃连接与缓冲，不倾倒 console 回放；远端正常 EOF 则可在回收前读取已保留的数据。未实现自动登录/分页/提交，也未声称定位真实厂商设备的命令重复根因。

Linux 支持真实 OS TCP 队列阈值；Windows 显式报告不支持该查询，不伪装成另一个指标。普通有界读写和系统 keepalive 仍支持两平台。

## 静默登录的发送回执边界

用户反馈：sendline_text 发送用户名/密码后 input_state=sent、written_bytes 非零，但输出为空且 start=end，后续两次读取也为空。没有真实端点日志/抓包，不能判断是设备未处理、密码正确但 shell 静默、认证等待其他步骤、回放/游标影响，或数据未送达。

本轮澄清：持久终端 shell、console 登录、任何 Telnet 都使用 stream send/read；SSH mode=exec 的显式 interactive 任务可按 task handle 收发。stream sent 只是本地 writer 完整写入；written_bytes 是应用字节本地计数，SSH/Telnet framing 不在计数内。TCP ACK 最多是传输层证据，不是终端认证回执。保留 sent，避免 accepted 被误解为“设备接受密码”。SSH exec 的 sent 表示 exec channel 启动请求成功，与最终退出状态分开。

空输出/start=end 仅表示本次没有消费原始输出字节，不提供认证结论。应以实际观察到的设备提示/失败文本进行一次有界只读等待；仍为空时明确报告远端送达/认证/shell 就绪未知，不自动重发密码、改行结尾、补回车或重连重放。提示符匹配也可能来自旧未读内容或 console 回放，不能建立本次输入的因果确认。本轮是文档和工具描述澄清，没有增加应用层 ACK，也没有复现特定设备的密码问题。

---

## 历史分析（0.1 / 0.2）

# 网络设备现场反馈复盘

反馈包含：exec 输出后断链、增量空输出诱发重试、提示符不匹配、一次 diagnose 调用出现两次进入视图警告。本次没有真实设备访问和原始审计日志，以下区分代码确认与模拟器验证。

| 问题 | 排查结论 | 更新 |
|---|---|---|
| SSH 通道未返回 exit-status 后整条连接被关闭 | 已从代码确认，旧版把 ExitMissingError 当作 transport 故障 | 保留连接与输出，reason=exit_status_missing、warning、exit_code 缺省；不判断命令成功 |
| 输出延迟被理解为未发送 | 增量输出本身符合约定，但缺少直接发送确认，易误用 | 新增 input_state、input_seq、input_bytes、next_action；空输出也提示继续 sess_read |
| 重复调用相同逻辑输入 | 旧版仅在完全无输出时限制新输入，无法识别客户端重试 | 可选 input_id 在最近 128 ID 窗口内强制去重；改变输入复用 ID 报错，未知发送也保留记录 |
| diagnose 双警告 | 未确认真实设备根因；发送路径没有重发循环 | 加 SSH 线上字节测试，加入 CR/LF 分别触发 Enter 且空行回放上一命令的模拟终端；line 默认 SSH CR 避免多余 LF |
| [host] 正则不匹配 [~host] | 模式与实际提示符不同 | skill/README 增加 ~、*、嵌套视图示例，要求匹配观察到的提示符 |
| diagnose-view 不识别 | 无法从这些资料判定所有型号/版本的命令名 | 不自动修正或补执行；建议观察设备帮助 |

测试中一次原始 data="diagnose\r\n" 只在 SSH 通道发出这 10 个字节；上述模拟器仍能产生两次警告，因为 CR 和 LF 各触发一次 Enter。line="diagnose" 默认发送 diagnose + CR 共 9 字节，同一模拟器只产生一次警告。它证明“两个警告”不能单独证明客户端发了两次命令，**没有证明真实华为设备一定如此处理 CRLF**。

现场复测建议：使用单独测试会话和 `serve --log /private/audit.jsonl`；记录工具参数、input_seq/input_state/input_bytes、start/end 与原始输出。先观察当前提示符，再以 line 和唯一 input_id 发出一次用户授权的命令；只用 sess_read 获取延迟输出。如需比较 CRLF，应先确认该操作在测试设备上允许重复，而不是直接对配置命令重复试验。输入审计记录包含原始参数，可能含敏感内容。

input_id 是有界、进程内去重，不能提供跨连接 exactly-once，也不能控制设备对控制字符或空行的解释。没有真实设备和日志时，双警告根因仍需现场核实。

## 0.2.0 后续反馈处理

旧现场记录中的 32 位 session ID 和 “Previous input has produced no output yet” 属于旧版行为；新部署先通过 version/MCP initialize 核对构建。静默设备不再受回显背压限制，正常交互不重复返回强制先读提示。无活动调用时默认保留 30min，可配置 idle_timeout；活动等待和后台 SSH exec 仍有 110s 静默保护。关闭不再带出未读 console 回放内容，输入/读取工具增加单次输出预算。

观察到 data="&#13;" 发出五个字符，只能证明最终参数中的实体未转成 CR，不能据此判定是模型不懂转义。模型到 agent 的 XML/JSON 等中间承载可能影响可见转义文本。新增 data_escaped 显式解码一层，并提供 key 控制键；原始 data 语义保留。测试覆盖 JSON-RPC 参数到实际 TCP 字节，但没有验证特定外部 agent 的内部序列化格式。协议边界与完整例子见 README 和 skill。

本轮没有自动探测设备、自动重放登录、自动提交配置或绕过 harness 拒绝。未从原始共享 console 流中自动判断哪些字节属于历史回放，也未声称复现真实厂商设备问题。
