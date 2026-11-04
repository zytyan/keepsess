# sessmcp

面向 AI Agent 的 Go MCP 工具，支持进程内持久 SSH / Telnet 连接、交换机等交互终端、有界等待与增量输出。**Go 1.22.1+，纯 Go，无 CGO。**

```sh
go build -buildvcs=false -o sessmcp ./cmd/sessmcp
./sessmcp help
./sessmcp serve
./sessmcp serve --log ./audit.jsonl
./sessmcp mcp-skill
./sessmcp install-skill --dir ./sessmcp-skill
```

直接运行 `sessmcp` 只显示帮助，不启动服务。`install-skill` 写入 AI 可读的 `SKILL.md`，不会覆盖已有文件。所有连接在服务进程退出后释放。

MCP 客户端配置（stdio，每行一条 JSON-RPC 消息）：

```json
{
  "mcpServers": {
    "sessmcp": { "command": "/absolute/path/sessmcp", "args": ["serve"] }
  }
}
```

## 工具

| 工具 | 用途 |
|---|---|
| `sess_connect` | 创建连接，返回 session；SSH 默认为非交互式 |
| `sess_exec` | 普通 SSH 命令；使用协议退出状态，合并 stdout/stderr |
| `sess_interact` | 用 line 发送单行命令，或用 data 发送精确原始输入 |
| `sess_read` | 不发送输入，继续等待、读取输出 |
| `sess_list` | 列出连接、运行与断链状态 |
| `sess_close` | 立即关闭并释放连接，不等待该会话的操作锁 |

**普通 SSH 优先使用 `sess_exec`**：一个持久 SSH transport，每个命令新建 exec channel。通过 `Session.Wait()` 获取 SSH exit-status / exit-signal，不使用结束标记或猜测提示符。各次 exec 不保留 cwd / shell 环境；需要状态时使用同一命令或显式交互 shell。stdout/stderr 合并为到达顺序；两个独立 SSH 流之间不承诺远端的严格时间排序。

SSH 连接示例：

```json
{"protocol":"ssh","address":"server.example:22","user":"ops","key_file":"/home/me/.ssh/id_ed25519","known_hosts":"/home/me/.ssh/known_hosts"}
```

也可以提供 `password`、加密私钥的 `key_passphrase`，或用 `host_key:"SHA256:..."` 固定主机密钥。必须选择 known_hosts、host_key 或显式 insecure=true；默认不跳过验证。

随后调用 `sess_exec`：

```json
{"session":"s1","command":"uname -a","wait":"30s"}
```

若提前返回且 `running:true`，使用 `sess_read` 等待原命令；不要重新提交。非零退出码通过 `exit_code` 返回，与传输故障区分。若通道关闭但未返回 exit-status/exit-signal，返回 `reason:exit_status_missing` 和 warning，保留输出、令 exit_code 缺省并保留 transport；不会把未知结果当作成功，也不会仅为缺失状态而断开整个连接。SSH 规范将退出状态报告作为建议行为，未返回它不等于 transport 断链。后续网络设备命令应使用交互模式，不能自动补执行已经输出结果的命令。

## 交换机、路由器、Telnet

SSH 连接设置 `interactive:true`；Telnet 连接天然是交互式。Telnet 登录由 Agent 按 Username / Password 提示符分步完成，不自动猜测设备登录流程。

```json
{"protocol":"telnet","address":"192.0.2.10:23"}
```

查看 connect 返回的初始输出，必要时用 sess_read 等待提示符；随后调用 sess_interact：

```json
{"session":"s1","line":"show version","input_id":"show-version-1","waitfor":"(?m)switch[#>]\\s*$","timeout":"800ms","wait":"30s"}
```

推荐 `line`（不含 CR/LF），自动添加一个选定的 Enter 序列：SSH 默认 CR，Telnet 默认 CRLF；可用 `enter:"cr"`、`"lf"` 或 `"crlf"` 覆盖。部分前台 CLI 把 CR、LF 分别视为 Enter，CRLF 有可能触发空行回放，所以 SSH 设备先使用默认 CR。`data` 保留原样，不自动追加或规范化换行；适用于补全、控制字符和分页空格，与 line 互斥。内部 Telnet 编解码负责 IAC 转义。

华为风格提示符可能是 `<host>`、`[~host]`、`[*host]` 或嵌套视图，应匹配实际观察到的完整提示符。例如 `(?m)(<[^>\r\n]+>|\[[^\r\n]*\])\s*$`（这是正则本身，JSON 中反斜杠需再转义）。waitfor 是匹配规则，不能代替正确的提示符或设备命令名。

## 发送确认与防止误重发

空输出、reason=wait/timeout 只表示本次返回时尚未收齐输出，**不表示命令没有执行**。接下来使用 `sess_read`，不要再次提交命令。结果新增：

| 字段 | 含义 |
|---|---|
| input_state | sent：传输接受输入；not_sent：未发送；unknown：可能部分发送；duplicate：已识别 input_id，本次没有重发；none：本次未发送输入 |
| input_seq | 会话内发送尝试序号；重复 ID 返回其原序号，纯读取返回最近序号 |
| input_bytes | 此序号对应的计划输入字节数，不代表远端已执行次数或已确认发送字节数 |
| next_action | 如 sess_read 或 reconnect_and_inspect，指导下一步 |

`input_id` 是 exec/interact 的可选防重参数：每个逻辑输入使用新 ID，重试同一输入保持 ID。会话保留最近 128 个不同 ID；相同 ID 与相同字节输入只发送一次，相同 ID 与不同输入报错。发送结果不确定时也记录 ID，防止自动重放。ID 不是 JSON-RPC 请求 ID；它不跨断链重连、重启或缓存淘汰提供保证，也不保证设备只执行一次。未提供 ID 时不会按命令文本去重，因为同一命令可能被有意多次使用。

## 等待与超时

需求中的“与”和“任意条件达成返回”存在歧义，本实现按后者处理，即 **OR**：

| 条件 | 含义 |
|---|---|
| `wait` | 从调用开始计算的固定时间，默认 `5s`，即使还设置了 waitfor/timeout |
| `waitfor` | Go RE2 正则匹配合并输出，支持跨网络包 |
| `timeout` | 本次调用开始后，stdout/stderr 连续无刷新的时长；输入和 SSH 保活不重置它 |
| SSH 完成 | 非交互命令退出时立即返回，无须等上述时间 |

时间支持 `100ms`、`30s`、`1min`、`1m30s` 等正值。`waitfor` 匹配失败也有默认 wait 兜底；可以显式增加 wait。发送输入时只匹配本次输入之后的输出；纯读取时也匹配上次增量读取后已经到达的未读输出，避免漏掉调用间隙到达的提示符。

额外边界：

- **TCP keepalive**：SSH/Telnet 默认显式启用，周期参数 15s，使用 Go 1.22 跨平台 API（Linux/Windows）；具体探测与失效判定由系统实现。TCP 探测不产生终端输入，也不刷新业务输出静默计时。
- **活动操作输出静默 110s**：活动调用从调用开始或最近一次输出（取较晚者）计时；运行中 SSH exec 在调用之间继续按最近输出计时，轮询不重置。超限断开并错误返回，不受更长 wait 影响。
- **无调用的空闲会话**：默认保留 30min，`sess_connect.idle_timeout` 可设为 `"10min"` 等时长，`"0"` 禁用回收。交互会话不会因分析或等审批时无输出 110s 被关闭。空闲回收不依据 running 标记猜测 console 命令完成。连接仍可能被远端关闭。
- **单个 RPC 上限 115s**：即使端点持续输出，也必须返回。此时远端命令可能仍在运行，继续读取并检查状态。
- 连接、SSH 握手/建通道、输入写入上限 **10s**；输入阶段还受本次 wait 剩余时间约束。若发送阶段超时则关闭连接，明确提示输入可能部分执行，需先检查状态再重试。
- MCP 取消通知停止等待；发送期间取消会断开连接并报告输入状态未知，发送完成后的取消不保证终止远端命令。不会自动重放。可使用 sess_close 释放整个连接。

断开时结果包含 `connected:false`、`error` 和重连提示；也可用 sess_list 检查。不会自动重连与重放具有副作用的命令。已断开的 handle 保留诊断，调用 sess_close 可释放会话配额。

## 增量、历史与反压

默认返回增量输出，同会话使用一个共享游标。`all:true` 返回保留历史，`tail:100` 选择保留历史的最后 100 行；`offset` 是绝对字节偏移，可配合结果的 `end` 分页。这三种显式读取不推进默认游标。历史最多 1 MiB，单次输出默认 8 KiB，可用 max_output_bytes 调至最多 64 KiB；`truncated` / `retained_from` 报告已丢弃历史，`more:true` 表示还可以读取更多。all 的含义是全部**仍保留**的历史，超过响应上限需要分页。

单条输入默认最大 4096 字节，可通过 `sess_connect.max_input_bytes` 配置（包含 line 自动追加的 Enter，0 使用默认值，最高 1 MiB）。同会话只允许一个正在等待/发送的操作，竞争立即返回 busy，不排队。交互输入在底层 Write 完整返回后即可继续发送，不要求回显或提示符，也不因 running=true 报背压。Write 表示本地传输接受字节，不代表 TCP 发送队列已清空、对端已收到或设备执行完成；不主动查询 TCP 是否满，而由 Go Write 在需要时等待可写。TCP 缓冲满或 SSH 窗口耗尽时等待写入，超过写入期限（最多 10s，且不超过 wait）或取消则关闭连接，返回 input_state=unknown，禁止自动重放。此路径使用纯 Go 网络接口，兼容 Linux/Windows。输出积压通过 more/truncated 和 backpressure 提示，仍允许下一条输入。

Session 使用短句柄 `s1`、`s2`，进程内递增且关闭后不复用；重启后须重新连接并使用新返回的句柄。句柄不是认证凭据。

最多 32 会话、32 工具 worker、64 个输出帧；单个输入 JSON 帧上限 12 MiB + 128 KiB（容纳显式转义和 JSON 封装；解码后的输入仍最多 1 MiB）。stdio 请求读取、远端操作与输出写入独立调度。长命令不会阻塞其他连接、ping、取消或关闭；库操作即使未响应 context，独立 RPC 看门狗也会结束响应，其 worker 配额在真正结束前不会释放。

stdio 的单流本身无法越过不读取的消费者传输数据：如果 stdout 写入阻塞 **3s**，服务结束并退出，客户端需重启服务并重连。不会无限阻塞 stdio，也不会无限创建 writer goroutine。

例如，连接时把单条输入上限设为 16 KiB：

```json
{"protocol":"telnet","address":"switch.example:23","max_input_bytes":16384}
```

后续调用的 `session` 必须使用本次 `sess_connect` 实际返回的短句柄；示例中的 `s1` 只是示意。

### 低开销交互（0.2.0）

- `sess_close` 只返回关闭状态，不返回终端正文，也不把正常关闭当作错误；需要最后输出先调用 `sess_read`。
- `sess_exec`、`sess_interact`、`sess_read` 均支持 `max_output_bytes`：默认 8192，最大 65536，0 使用默认值。`more=true` 时继续增量读，或用 `offset=end` 获取下一段；原始历史和偏移保持不变。`truncated` 表示历史已丢失，与有后续输出的 `more` 不同。未默认清理 ANSI 或归一化原始字节。
- 交互会话返回 `running=true` 不要求先读再发，也不能证明命令仍在执行；正常交互不再重复 hint/next_action。同会话并发 `busy` 返回 `next_action=wait_current_call`；标准 SSH 命令未结束时则提示 `sess_read`。
- 输入因 busy 未发送时，`retry_same_input=true` 表示等忙碌结束后可沿用相同输入和 input_id 重试；未发送的 ID 不进入去重窗口。`sent/unknown` 不能因空输出换 ID 重发。input_id 始终可选。
- CLI `version` 和 MCP initialize 返回相同的 0.2.0+构建标识；`scripts/build.sh` 注入 Git 标识和 dirty 标记，便于确认部署版本。

控制键是一等参数，与 line/data/data_escaped/enter 互斥：

```json
{"session":"s1","key":"enter","waitfor":"Password:|(?m)^<[^>\r\n]+>\s*$","wait":"10s"}
```

`key` 支持 `enter`、`ctrl_c`、`ctrl_d`、`ctrl_z`、`esc`、`tab`。Enter 与空 `line:""` 一样，SSH 发送 CR，Telnet 发送 CRLF。需要特定 Enter 时使用 line+enter 或原始 data。

原始 data 只进行 JSON 解码，不进行 HTML 实体或第二层转义解析：

| JSON 参数 | 实际发送 |
|---|---|
| `{"data":"\r"}` | 一个 CR |
| `{"data":"\u0003"}` | 一个 Ctrl-C |
| `{"data":"\\r"}` | 反斜杠和字母 r |
| `{"data":"&#13;"}` | 五个普通字符 |

`\xNN` 不是合法 JSON 转义，但可作为 data_escaped 的可见文本（JSON 内写成 `\\xNN`）。`waitfor` 匹配终端输出，输入调用匹配本次输入后的输出，read 匹配未读输出；它不匹配工具的 reason/error 状态。断连会自行结束等待。

### 模型转义文本到执行字节

中间 agent 的 XML/JSON 序列化可能影响控制字符传递，因此提供独立的 `data_escaped`，由 sessmcp 在 JSON 解码后再显式解码一次。`data` 的原样语义不变，不猜测上游做过几层转换。

| 模型交给 agent 的 data_escaped 文本 | 等价 JSON 参数 | 最终字节 |
|---|---|---|
| `\r` | `{"data_escaped":"\\r"}` | `0D`（CR） |
| `\x03` | `{"data_escaped":"\\x03"}` | `03`（Ctrl-C） |
| `\u4e2d` | `{"data_escaped":"\\u4e2d"}` | `E4 B8 AD`（UTF-8 中） |
| `\\r` | `{"data_escaped":"\\\\r"}` | `5C 72`（字面反斜杠+r） |

支持 `\r \n \t \b \f \v \a`、`\\`、转义引号、`\xNN`、`\uNNNN`、`\UNNNNNNNN` 和 Go 三位八进制转义。普通引号不必再转义；HTML 实体不转换。非法/不完整转义在发送任何字节前报错，错误不回显输入内容。input_bytes、max_input_bytes 和 input_id 去重都按解码后的实际字节计算；相同 ID 下 data 与 data_escaped 生成相同字节时不会重发。

`data_escaped` 与 `data/line/key/enter` 互斥，不支持隐式多轮解码。它与 raw data 一样可能包含密码等敏感内容；启用审计时这些输入文本不会自动脱敏。

## 日志

`serve --log PATH` 启用 JSONL 输入审计，包含时间、工具名与参数；认证字段 password / key_passphrase 等递归脱敏。POSIX 文件新建权限 0600，已有文件必须是权限私有的普通文件；Windows 使用目录继承的 ACL，需选择私有目录。审计确认写入后才执行输入；队列满、写入失败或 1s 内未完成则拒绝执行此次输入。日志不写 stdout。

命令与交互 data 作为审计对象原文记录，可能含设备密码或业务秘密；审计路径应由用户妥善保管。Telnet 是明文协议。

## 依赖与 Go 1.22

SSH 连接层复用 `golang.org/x/crypto/ssh v0.33.0`。Telnet 使用 `internal/telnet` 内部实现，无外部 Telnet 依赖，固定使用 **Go 1.22.1** 验证。它处理跨包 IAC、字面 IAC 转义、WILL/WONT/DO/DONT 与重复协商抑制，支持 BINARY、ECHO、SGA、终端类型 vt100 和 80×24 窗口尺寸；拒绝不支持的选项。子协商最多 8192 字节，异常转义或超限会报错断开。应用数据与协商回复都受到独立写入超时保护，已解码输出不会被未完整到达的控制序列扣住。go.mod / go.sum 固定兼容版本，不使用要求更高 Go 版本的最新版 SDK；MCP stdio 层实现所需的 JSON-RPC 方法，支持协商 2024-11-05、2025-03-26、2025-06-18、2025-11-25。

## 编译与测试

```sh
# 指定真实 Go 1.22 工具链验证（第一次会下载工具链）
GOTOOLCHAIN=go1.22.1 go test ./...
GOTOOLCHAIN=go1.22.1 go vet ./...
GOTOOLCHAIN=go1.22.1 SESSMCP_LONG_TEST=1 go test -race ./... -count=1 -timeout 4m

# 一次编译全部目标，CGO_ENABLED=0
GOTOOLCHAIN=go1.22.1 sh scripts/build.sh
```

产物与 SHA256SUMS 位于 dist/：

- sessmcp-linux-amd64
- sessmcp-linux-arm64
- sessmcp-windows-amd64.exe

也可直接使用 `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/sessmcp` 等标准交叉编译命令。

测试启动真实本机 TCP/SSH 服务，使用交换机模拟器覆盖配置模式、接口模式、分页、ANSI、分包提示符、串口慢输出和 Telnet IAC。另有 SSH 双流合并/非零退出、历史分页、输入反压、取消与 stdio 队头阻塞测试。长测试建立不发送任何数据、无限保持 TCP 连接的端点，并从 stdio 发出超过两分钟的等待，断言 120s 前得到错误且 ping 不被阻塞。仓库提供 `docs/ci.yml` CI 模板：使用 Go 1.22.1 执行长测试、race 与三目标交叉编译，Windows 单独运行短测试。当前 GitHub 令牌无 workflow 权限，未启用 Actions；有相应权限时将模板复制到 `.github/workflows/ci.yml` 即可。

相关协议/API：[MCP stdio](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)、[MCP tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)、[Go SSH](https://pkg.go.dev/golang.org/x/crypto/ssh)、[Telnet RFC 854](https://www.rfc-editor.org/rfc/rfc854)、[终端类型 RFC 1091](https://www.rfc-editor.org/rfc/rfc1091)、[窗口尺寸 RFC 1073](https://www.rfc-editor.org/rfc/rfc1073)。

现场反馈排查与已验证边界见 [docs/field-report.md](docs/field-report.md)。SSH 状态语义参考 [RFC 4254 §6.10](https://www.rfc-editor.org/rfc/rfc4254.html#section-6.10)。
