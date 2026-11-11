# Daemon 升级和协议兼容

安装或构建新 keepsess 后执行：

```sh
/path/to/new/keepsess daemon upgrade
keepsess daemon status
```

upgrade 使用当前可执行文件，不联网下载发行版。二进制复制到用户私有运行目录的
`versions/<sha256>/`；预检通过后停止旧观察进程，使用相同实际监听地址和 Web 路径启动新进程。
其他配置读取 `~/.keepsess.toml`。健康检查核对启动 ID 和二进制摘要后才提交首选版本。
相同摘要且健康时不重启；没有 daemon 时启动当前版本。命令总预算 60 秒，并发升级返回 busy。

Web 会短暂断开，浏览器恢复只读。SSH/Telnet 和任务由 Agent 进程持有，继续运行；
未确认的输入结果未知，绝不自动重发。升级不执行身份探测命令，不迁移远端连接。
失败时尝试启动上一份受管理二进制，并分别报告升级及恢复结果。首次升级若没有上一份
受管理副本，无法自动恢复；使用原二进制重新运行 upgrade。日志位于私有运行目录。

首选版本保存在 `selected.json`；后续 Agent 自动启动会校验并使用它，不能因旧 Agent
可执行文件较旧而降回旧 daemon。升级事务 `upgrade.json` 及令牌仅保存在用户私有目录。
命令中断后，下一次 Ensure 检查健康候选并提交，或清理残留事务、沿用先前首选版本。
所有生命周期动作受 OS 文件锁协调，不用 PID 判断进程身份或杀死无关进程。

## 一次性迁移

此版本将旧 WebSocket JSON v1 协议切换为 Protobuf 二进制 v2。旧 v1 daemon 可通过原有
认证 stop 接口升级；旧 Agent 不能注册新 daemon。旧 Agent 持有的连接仍存在，但不能从
新 Web 操作。请自行安排结束旧会话和重启 Agent；升级命令不终止 Agent。首次迁移期间
旧 Agent 不认识升级锁，可能竞争拉起 v1 daemon，命令有界失败时停止旧 Agent 的自动启动后重试。

## 稳定协议约束

`internal/daemon/wire/daemon.proto` 是从 v2 起永久支持的 wire 契约，独立于内部 Go 类型。
现有操作 `exec.v1`、`send_text.v1`、`task_list.v1` 是能力标识；新操作必须协商能力后发送。
不支持的操作返回 not_sent，不替代执行；未知通知忽略。软件构建版本不参与相等校验。

新增字段必须兼容缺省行为；禁止更改既有字段编号、类型、名称和语义，禁止复用删除的字段。
新增请求语义必须有独立能力标识，不能仅因为对端能解码新增字段就执行。未知字段可忽略。
ConfigPatch map 的值为单个 JSON 值的 UTF-8 字节，保持现有配置名称、类型和语义；新增会影响
执行的配置也须经能力协商，旧配置仍由 core 验证，不静默降级。
浏览器协议继续由同一 daemon 的无缓存静态资源提供，不要求旧浏览器代码永久互通。

生成工具：protoc 25.3、protoc-gen-go v1.36.6、Buf v1.47.2。runtime 固定为
`google.golang.org/protobuf v1.36.6`，支持 Go 1.22.1。生成代码入库，普通构建不需要生成工具。

```sh
buf generate
sh scripts/check-protocol.sh
```

CI 以 FILE 规则对比冻结的 `baseline.binpb` 及 PR 基线，防止后续新增契约回退。
基线不能随功能变更重新生成；新增字段的运行语义仍需兼容测试，Buf 不能验证业务语义。
