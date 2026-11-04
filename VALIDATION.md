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
