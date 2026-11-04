# keepsess

面向 LLM 的持久 SSH exec / SSH、Telnet 双向流 MCP 工具。纯 Go，固定 Go 1.22.1 验证，发布构建 `CGO_ENABLED=0`，支持 linux/amd64、linux/arm64、windows/amd64。

项目、Go module、MCP 注册名和 skill 名称统一为 **keepsess**，仓库地址为 [zytyan/keepsess](https://github.com/zytyan/keepsess)。工具名继续使用 `sess_*`，句柄继续使用 `s1`、`s2`；安装器直接写入 keepsess 配置，不迁移旧名称。

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

版本 0.3.0 重构为下列 15 个工具。CLI version 与 MCP initialize 暴露相同的版本和 Git 构建标识。**这是接口迁移**：旧 sess_interact、sess_read、address/user/key_file、wait/waitfor/timeout、input_id 等参数不再使用；重新获取 tools/list 并安装新版 skill。

## 一键安装到客户端

MCP 标准规定 JSON-RPC、stdio、tools/list/tools/call 和 JSON Schema 工具描述；15 个工具由客户端连接后自动发现，不需要逐个安装。但 MCP 配置文件和 skill 发现目录由客户端决定，因此安装器按客户端适配。

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

| 工具 | 参数与用途 |
|---|---|
| sess_connect | protocol、host、port；可选 username/password/keyfile、SSH 主机校验、mode、config |
| sess_configure | session、config；原子更新出现的字段，其余不变 |
| sess_get_config | session；有效配置、单位、说明和平台能力，不包含认证秘密 |
| sess_list | 无参数；列出连接和 exec 状态 |
| sess_exec | session、command、collect_output?、config?；SSH exec，独立 exec_total_timeout_ms 等待协议完成，忽略 read 超时与 end_pattern |
| sess_send_raw | session、data、collect_output?、config?；文字编码后发送，不解析转义 |
| sess_send_escaped | 同上；显式解码一层转义再发送 |
| sess_sendline_raw | 同 raw，追加一次配置的 line_end |
| sess_sendline_escaped | 同 escaped，追加一次配置的 line_end |
| sess_read_available | session、config?；立即读取未读数据，不等待 |
| sess_read_until | session、config?；按匹配、超时等条件结束读取 |
| sess_read_tail | session、lines?、config?；最后 N 行，默认 20，不移动增量游标 |
| sess_read_all | session、offset?、config?；立即分页读取保留历史，不移动增量游标 |
| sess_close | session；关闭并丢弃该连接和缓冲，不返回终端正文 |
| sess_close_all | 无参数；关闭并丢弃全部连接和缓冲，不返回终端正文 |

每次连接返回短句柄，例如 `{"session":"s1","connected":true}`，不读取、不消费欢迎信息。句柄进程内递增不复用；所有后续操作明确指定 session。Telnet 登录属于终端流，应观察提示再发送相应信息，不猜测或重放登录流程。

SSH 默认 mode=exec，适合普通服务器。交换机、路由器和 console 必须显式 mode=stream。Telnet 只能使用 stream。SSH 默认校验主机密钥：使用 known_hosts 或 host_key（SHA256 指纹），只有显式 insecure=true 才跳过。

```json
{"protocol":"ssh","host":"server.example","port":22,"username":"operator","keyfile":"/private/id_ed25519","known_hosts":"/private/known_hosts","mode":"exec"}
```

```json
{"protocol":"telnet","host":"switch.example","port":23,"config":{"read_idle_timeout_ms":500,"read_total_timeout_ms":30000}}
```

## 配置

连接时 config、sess_configure.config 与操作的 config 使用同一套校验。未知字段、null、非法正则、超范围值整体拒绝，不部分生效。调用开始时快照配置；之后修改不会影响当前调用。操作中的 config 不改变会话默认配置。

| 配置项 | 默认值 | 语义 |
|---|---|---|
| connect_timeout_ms | 10000 | TCP 连接、SSH 认证和初始 shell 建立，总范围 1..110000ms；已建连后修改仅保存值，重新 connect 时显式复用该 config 才生效 |
| write_timeout_ms | 10000 | 输入写入或 exec channel 建立，1..110000ms |
| session_idle_timeout_ms | 1800000 | 无操作会话回收，0 禁用；最长一年；活动读写或运行中 exec 不回收 |
| read_total_timeout_ms | 30000 | 从读取开始的总时限，1..110000ms；不影响 sess_exec |
| read_idle_timeout_ms | **500** | 从读取开始计算，收到端点字节后重置；0 禁用，最高 110000ms；不影响 sess_exec |
| exec_total_timeout_ms | 30000 | sess_exec 命令启动后等待协议完成的独立总时限，1..110000ms；不受 read 超时与 end_pattern 影响 |
| tcp_keepalive_ms | 15000 | 系统 TCP keepalive 周期参数，0 禁用，最高一天；实际探测与失效时序取决于 OS |
| line_end | SSH `\r`，Telnet `\r\n` | 可见转义文本，配置时按 UTF-8 与字节转义解码一次，最多 1024 字节；可为空 |
| encoding | utf-8 | utf-8、gbk、shift-jis；支持 utf8、sjis、shift_jis 等别名 |
| input_encode_error_action | error | 无法编码时整条输入拒绝，不进行部分发送 |
| output_decode_error_action | to_escape_bytes | 非法输出字节显示为 `\xNN`；to_hex 显示 `<NN>`；error 报错并保留非法字节 |
| max_input_bytes | 4096 | 实际编码后输入长度，包含行结束符；1..1048576 |
| max_output_bytes | 8192 | 返回 UTF-8 文本字节预算，包括非法字节的展示文本；1..65536 |
| backpressure_bytes | 0 | 非零时按系统 TCP 队列阈值拒绝新写入，最高 1GiB；需平台支持 |
| end_pattern | 空 | Go RE2，匹配解码后的未读输出；空表示禁用，最多 1024 字节；不影响 sess_exec |

数字可传整数或只包含整数、四则运算、括号的字符串，例如：

```json
{"session":"s1","config":{"session_idle_timeout_ms":"30*60*1000","max_output_bytes":"8*1024","read_idle_timeout_ms":500}}
```

不执行 eval、函数调用或变量；除法必须得到整数，表达式长度与数值有界。connect_timeout_ms、session_idle_timeout_ms、tcp_keepalive_ms 是连接配置，只能在 connect/configure 中设置；其余可按操作覆盖。

## 发送、转义和编码

四个 send 工具只适用于 stream。raw 的含义是“不解析转义”，文字仍按 encoding 编码。escaped 在 JSON 解码之后显式解码一层，适合模型经过 XML/JSON agent 承载可见转义文本。

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
{"session":"s1","data":"display version","config":{"end_pattern":"(?m)^<[^>\r\n]+>\\s*$"}}
```

上例用于 sess_sendline_raw；提示符须按观察结果配置，不能把正文里的括号猜成命令完成。没有自动分页、自动登录、自动 commit/save 或自动确认。

默认 collect_output=true：写入成功后按 read_until 收集未读输出，减少工具往返；false 只返回发送回执。**返回的流不归属于本次输入**：可能包括旧的未读输出、回放或自发日志。

input_state 为 sent、not_sent 或 unknown；input_bytes 是计划发送字节数，written_bytes 是已确认写入字节数，unknown 时仅是下界。sent 不代表远端已执行，unknown 也不代表零字节送达。没有 input_id，不自动重试或重放；空输出不是重发依据。零值计数字段可以省略。

## 读取与结果

read_available 立即返回当前未读数据；read_until 从未读游标开始，等待后续数据。读写可并行，同会话一次只允许一个读取者和一个写入者，竞争直接报 busy，无无限队列。collect_output=true 的发送必须先获得读取权，否则不发送。

| reason | 含义 |
|---|---|
| exec_finish | SSH exec channel 完成，不保证成功；有可靠协议退出状态才给 exit_code |
| match | 观察到结束正则，仅消费到匹配末尾，后面的字节留待下次读取 |
| total_timeout | 达到读取总时限，或 sess_exec 的独立等待总时限；不关闭连接 |
| idle_timeout | 本次读取期间无新字节达到静默时限，不关闭连接 |
| aborted | 网络、协议或输出解码错误，附 error；connected 指明连接是否还在 |
| cancelled | 调用方取消；读取取消保留连接，发送期间取消则断连且 input_state=unknown |
| output_limit | 达到输出预算，后续数据保留；若下一个字符放不下，返回 minimum_output_bytes |
| closed | 对端正常结束，或本地主动丢弃连接 |

立即读取耗尽当前数据时可以不带 reason。参数错误、非法正则和 busy 是工具错误，不混入 reason。匹配优先于同批数据的其他结束条件；输出预算以内没有匹配且达到预算时，先返回 output_limit，剩余字节后续读取。

输出统一为有效 UTF-8，不默认剥离 ANSI 或改变换行。跨网络包的半个字符在增量读取中保留；EOF、已完成 exec 或显式历史读取按错误策略处理残缺字节。error 策略不会因解码错误关闭连接，可以换策略再读。必要的字节转义作为整体展示，不切碎。

历史保留 1MiB，more 表示后面还有数据；start/end 为原始字节偏移，不是显示字符下标。增量读取推进一个会话游标；read_all/read_tail 不推进。read_all 默认从仍保留的最早位置开始，用 offset=end 分页，不会无限等待未来输出。dropped_bytes 明确报告请求位置之前已被覆盖的字节。显式 close 会丢弃全部缓冲，后续读取失败；远端自然关闭后尚可读取保留数据，直到回收或 close。

## SSH exec 与连接保护

**sess_exec 不使用 read_total_timeout_ms、read_idle_timeout_ms 或 end_pattern。** 无论这些值来自会话配置还是本次 config，命令都不会因静默 500ms、读取时限短或输出碰巧匹配提示符而提前结束等待。它使用独立的 exec_total_timeout_ms（默认 30000ms，1..110000ms），从命令成功启动后的收集阶段开始计时；channel 建立仍受 write_timeout_ms 约束，整个 RPC 仍有 115s 硬上限。

例如，下面的 sess_exec 最多等待协议完成 60 秒，不改变会话默认设置：

```json
{"session":"s1","command":"sleep 2; uname -a","config":{"exec_total_timeout_ms":60000}}
```

独立总时限到达仍返回 reason=total_timeout；它只结束本次等待，不停止远端命令或断开 transport。running=true 时用 sess_read_until/sess_read_available 继续读取，不重发命令。**随后显式调用的 read 工具照常使用 read 配置**；例如 read_until 仍可按 500ms 静默返回。collect_output=false 跳过执行等待读取，只返回发送回执。

输出预算、取消、关闭、网络/解码错误及后台 SSH exec 的 110s 静默保护继续生效，不能把 output_limit/total_timeout/cancelled 当作命令完成。只有 exec_finish 表示协议 channel 已结束，成功与否还须看 exit_code；缺失退出状态仍是未知结果。

sess_exec 每次新建 SSH channel，在持久 transport 上执行命令，合并 stdout/stderr。exec 的 cwd、环境不跨命令保留。运行中返回 running=true 时，用 read_until/read_available 继续读，不重新提交。缺失 exit-status/exit-signal 返回 exec_finish、warning 和缺省 exit_code，不能假定成功，也不因此关闭 transport。

每个 RPC 硬上限 115s；各连接、写入、读取配置均有界。stream 无活动调用时不因 110s 无输出关闭；默认 30min 无操作回收连接和缓冲。后台 SSH exec 独立受到 110s 无输出保护，轮询不重置。TCP keepalive 不刷新业务输出计时，也不能保证远端终端程序健康。

Linux 使用 SIOCOUTQ/TIOCOUTQ 查询 TCP 排队字节，包含未确认数据；SSH 测量的是整个加密 transport，不能推断某条命令的积压。Windows 当前明确不支持此查询，非零 backpressure_bytes 被拒绝，get_config 会报告能力。两平台普通发送仍使用 Write/SSH 窗口流控和超时。队列低于阈值只是允许尝试写入，不保证整条消息立刻写完或远端已处理。

最多 32 会话、32 tool workers、64 输出帧；输入 JSON 帧上限 12MiB+128KiB，供转义后的最大输入使用。stdio 调度、远端操作与输出写入分离；stdout 消费者阻塞 3s 后结束服务，避免队头阻塞和无限 writer。重启后须重新连接。关闭不等待活动操作，且不回吐正文。认证字段审计脱敏、文件权限 0600，但 command/data 仍可能包含秘密。Telnet 是明文。

## 构建与验证

```sh
GOTOOLCHAIN=go1.22.1 go vet ./...
GOTOOLCHAIN=go1.22.1 go test -race ./...
GOTOOLCHAIN=go1.22.1 SESSMCP_LONG_TEST=1 go test -race ./... -count=1 -timeout 4m
GOTOOLCHAIN=go1.22.1 sh scripts/build.sh
```

产物和 SHA256SUMS 位于 dist/，包括 Linux amd64/arm64 与 Windows amd64。build.sh 注入 Git 标识，工作区有改动则带 dirty。固定依赖 x/crypto v0.33.0、x/sys v0.30.0、x/text v0.22.0，Telnet 为内部纯 Go 实现，不引入外部 Telnet 库。

测试覆盖真实 SSH 退出与双流、缺失退出状态、Telnet IAC 分包、交换机模式/分页/慢输出、输入转义与线上字节、Unicode/GBK/Shift-JIS 分包与错误策略、超时/取消、读写并发、TCP 满缓冲/队列阈值、stdio 慢消费者及 race。长测试验证真实静默端点的读取在两分钟内结束且 transport 保留。Windows/ARM64 在本地只验证交叉编译；docs/ci.yml 是包含 Windows 短测试的 CI 模板，未自动启用。

[验证记录](VALIDATION.md) · [现场反馈与迁移说明](docs/field-report.md) · [AI skill](skills/keepsess/SKILL.md)
