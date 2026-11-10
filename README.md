# keepsess

面向 AI 和程序自动化的持久 SSH / Telnet 工具。AI 通过 MCP 操作，Python 脚本复用同一 Go 引擎，人类通过 Web 实时观察和回看历史。

- SSH 支持独立命令、交互任务、跳板、文件传输与 TCP/SOCKS5 隧道；Telnet 使用内部实现。
- 连接和任务由常驻进程持有，支持并发、有界读取、取消、只读保护和明确的发送回执。
- 纯 Go，固定 Go 1.22.1，`CGO_ENABLED=0`；支持 Linux amd64/arm64、Windows amd64。

## 安装与启动

从 [Releases](https://github.com/zytyan/keepsess/releases) 获取对应平台二进制，或从源码构建：

```sh
CGO_ENABLED=0 go build -o keepsess ./cmd/keepsess
# 三平台构建：sh scripts/build.sh
```

在目标项目目录安装到 MCP 客户端：

```sh
keepsess install --client codex   # 也支持 claude、cursor、vscode
# 先查看改动：追加 --dry-run
```

安装器复制二进制、合并 MCP 配置并安装 skill；重新加载客户端后使用。也可手动配置：

```json
{"command":"/path/to/keepsess","args":["serve"]}
```

无子命令只显示帮助；`serve` 才启动 MCP，stdout 专供协议。`keepsess help` 查看人类帮助，`keepsess mcp-skill` 输出 AI 操作指南。

## Python 自动化

需要 Python 3.10+ 和 keepsess 0.3.x 二进制。在仓库根目录安装，或通过环境变量直接导入：

```sh
python -m pip install ./python
# 免安装时：
export PYTHONPATH="$PWD/python${PYTHONPATH:+:$PYTHONPATH}"
# 引擎未加入 PATH 时：
export KEEPSESS_BINARY="$PWD/dist/keepsess-linux-amd64"
```

```python
import os
from keepsess import Keepsess

with Keepsess() as ks:
    ks.sess_configure(instance_name="Python 巡检")
    conn = ks.sess_connect(
        protocol="ssh", host=os.environ["SSH_HOST"], port=22,
        username=os.environ["SSH_USER"], password=os.environ["SSH_PASSWORD"],
    )
    result = ks.sess_exec(session=conn.session, command="uname -a")
    print(result.output, result.exit_code)
```

提供全部 30 个同名方法、dataclass 点语法结果及同步/异步客户端，零 Python 运行时依赖。每个客户端独立建连，退出上下文时关闭连接。详见 [Python SDK](python/README.md)。

## 核心使用规则

- 普通服务器用 SSH `mode="exec"`，以 SSH 退出状态判断完成；网络设备、持久 shell 和 Telnet 用 `mode="stream"` 配合 send/read。
- 所有配置参数放在顶层；connect/configure 修改会话默认值，操作参数只影响本次。全部 30 个工具统一可发现，提供完整参数说明，无需切换发现分组。
- `sess_help()` 列目录，`sess_help(topic="sess_exec")` 查看工具，`query` 搜索内置帮助。
- Python 优先 `sess_send_text` / `sess_sendline_text`：普通字符串已解释 `"\r"`、`"\x03"`。仅在需要显式解码可见转义文本时使用 `*_escaped`；`line_end` 配置仍使用可见转义，例如 `r"\r"`。
- 默认增量输出，每页 64KiB；长任务返回 handle 后继续 wait/read，不重复提交。单 RPC 上限 115 秒；exec 默认等待 120 秒，配置等待超时默认尝试 TERM，RPC 预算到期不发 TERM。
- 空输出、超时和本地写入成功都不证明远端执行或认证成功。缺失退出状态表示结果未知；断连后显式重连，不自动重放命令或凭据。

## Web 与配置

人类 Web 页面支持桌面端近期稳定版 Chromium 内核浏览器（Chrome、Edge）。实例可通过“修改名称”设置人类名称，优先显示，AI 命名在下方以小字展示；清空人类名称后恢复显示 AI 名称。两者独立保存，活动与归档实例均可修改，刷新或 daemon 重启后保留。实例以名称和本地时间显示；背景与呼吸光点表示进程连接仍活动，亮色边框表示当前选择。启用减少动态效果时使用静态标识。新进程 instance_id 使用 Bitcoin Base58，旧归档 ID 保持兼容。

`serve` 默认加入每用户共享 Web daemon，入口通常为 `http://127.0.0.1:8765/`；实际地址可用 `keepsess daemon status` 查询。页面列出活动实例、会话和任务，默认只读；观察不消耗 MCP/Python 的读取游标。进程退出后保留归档，连接不会恢复。活动实例优先于归档；会话按已连接/连接中、已关闭/断开、首次连接失败排列，失败记录默认折叠，点击“连接失败（N）”查看。排序不会切换当前选择。

从未产生 session 记录的实例不显示，正常退出并完成历史写入后清理空目录；连接失败也算 session 记录。异常退出的空目录仅在确认写入者已结束、历史完整且可在有界检查内确认无 session 时清理；旧归档缺少所有者锁或历史不完整时保留。daemon 暂时断连不会触发删除。

设置统一读取 `~/.keepsess.toml`，首次启动自动生成。`[web] enabled=false` 禁用本进程的 Web/历史；网页“显示与高亮”提供默认关键词高亮、自定义规则和预览，保存到同一配置文件，无需重启。修改其他 Web/history 配置后执行 `keepsess daemon stop`，修改历史目录还需重启原进程。

SSH 默认跳过主机密钥验证；显式设置 `host_key` 或 `known_hosts` 启用。Telnet 为明文。历史可能包含终端输入的密码，不要提交凭据、审计日志、历史、本地配置或内部令牌。Web 对外提供服务时应配置认证代理。

[完整使用参考](docs/usage.md) · [AI skill](skills/keepsess/SKILL.md) · [验证记录](VALIDATION.md)
