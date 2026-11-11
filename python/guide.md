# keepsess Python SDK

复用 keepsess Go 引擎的 Python 库：30 个 MCP 同名方法、显式关键字参数、dataclass 返回值、同步/异步客户端。运行时只依赖 Python 标准库。

需要 Python **3.10+** 和独立安装的 **keepsess 0.3.x** 可执行文件。Python 包不下载或捆绑二进制，不重新实现 SSH/Telnet。

## 安装或直接导入

从仓库根目录安装到当前 Python 环境：

```sh
python -m pip install ./python
# 开发时可编辑安装：
python -m pip install -e ./python
```

也可以不安装，设置标准 `PYTHONPATH`。以下 Unix 示例假设当前目录是仓库根目录：

```sh
export PYTHONPATH="$PWD/python${PYTHONPATH:+:$PYTHONPATH}"
export KEEPSESS_BINARY="$PWD/dist/keepsess-linux-amd64"
python -c 'from keepsess import Keepsess; print(Keepsess)'
```

Windows PowerShell（`$PWD` 为仓库根目录）：

```powershell
$env:PYTHONPATH = "$PWD\python;$env:PYTHONPATH"
$env:KEEPSESS_BINARY = "$PWD\dist\keepsess-windows-amd64.exe"
python -c "from keepsess import Keepsess; print(Keepsess)"
```

`KEEPSESS_BINARY` 是可执行文件路径，不是 shell 命令；也可以用 `Keepsess(binary="/path/to/keepsess")` 显式指定。都不设置时从 PATH 查找 `keepsess`。安装/导入 Python 包不需要 Go；从本仓库构建二进制仍按项目的 Go 1.22.1 构建说明进行。

## SSH 执行

```python
import os
from keepsess import Keepsess, ToolError

with Keepsess() as ks:
    ks.sess_configure(instance_name="Python 巡检")
    conn = ks.sess_connect(
        protocol="ssh", host=os.environ["SSH_HOST"], port=22,
        username=os.environ["SSH_USER"], password=os.environ["SSH_PASSWORD"],
        # 如需主机验证，显式指定 host_key 或 known_hosts；继承引擎默认策略。
    )
    try:
        result = ks.sess_exec(session=conn.session, command="uname -a")
    except ToolError as error:
        print(error.result.input_state, error.result.handle, error.result.error)
        raise
    print(result.output)
    print(result.exit_code, result.exit_signal, result.warning)
```

`exit_code=None` 表示没有已知退出码，不能当作成功。非零远端退出码本身不抛出 `ToolError`。同步完成且输出全部返回时 `handle` 可以为 `None`。输出为空时可查看 `comment`，不能据此判定命令未执行。

参数名、毫秒单位、转义规则、默认值与 MCP 一致；参数保持顶层。Python 参数值 `None` 表示省略，`False`、`0`、空字符串和空字典原样发送。配置只有 connect/configure 修改会话默认值。历史兼容的 `config` 参数仍可接收，建议使用顶层参数。

## 异步与长期任务

```python
import asyncio
from keepsess import AsyncKeepsess

async def main():
    async with AsyncKeepsess() as ks:
        # 独立客户端需要自行 sess_connect；以下是无需连接的例子。
        sessions, help_result = await asyncio.gather(
            ks.sess_list(), ks.sess_help(query="timeout"),
        )
        print(sessions.instance_id, help_result.matched)

asyncio.run(main())
```

长命令只提交一次，然后通过任务句柄继续等待：

```python
import time

# ks、conn 来自上面的 SSH 示例；启动异步任务不设置后台执行截止时间。
task = ks.sess_exec(
    session=conn.session, command="your-long-command",
    collect_output=False,
)
deadline = time.monotonic() + 600
while True:
    remaining_ms = int((deadline - time.monotonic()) * 1000)
    if remaining_ms <= 0:
        # 这里只结束脚本等待；保留 task.handle 供当前客户端后续检查。
        raise TimeoutError(f"等待预算耗尽，任务可能继续运行：{task.handle}")
    page = ks.sess_task_wait(
        handle=task.handle,
        exec_total_timeout_ms=min(110_000, remaining_ms),
        term_on_timeout=False,
    )
    print(page.output, end="")
    if page.error or page.warning:
        raise RuntimeError(page.to_dict())
    if page.state == "finished" and not page.more:
        print("exit:", page.exit_code, page.exit_signal)
        break
    if not page.connected or page.reason in {"closed", "cancelled", "aborted"}:
        raise RuntimeError(page.to_dict())
```

上述循环有显式 10 分钟总预算，不缓存全部输出、不重新提交输入，并关闭每次等待超时的自动 TERM。结果的 `dropped_bytes`、`filtered_bytes`、`minimum_output_bytes` 等信息应按业务需要检查。若在 `with` 内抛出异常导致退出上下文，会同时关闭这个客户端的连接与任务句柄；远端程序仍不保证退出。

## 网络设备和精确输入

**AI 编写 Python 脚本时优先使用 `sess_send_text` / `sess_sendline_text`，不要默认使用 `*_escaped`。** Python 普通字符串已经解释转义，`data="\r"` 和 `data="\x03"` 分别含实际 CR 和 Ctrl-C；SDK 的 JSON 序列化不会增加一次应用层解码。路径、命令中的字面反斜杠应使用 Python 原始字符串或正确引用，避免对普通运行时文本使用 `unicode_escape`。

```python
conn = ks.sess_connect(
    protocol="telnet", host="192.0.2.10", port=23, mode="stream",
)
# 登录按实测提示符显式 send/read，不自动发送凭据。
page = ks.sess_read_until(
    session=conn.session, end_pattern="Username:",
    read_idle_timeout_ms=0, read_total_timeout_ms=60_000,
)

# 原始文本不解码转义；此处末尾实际 CR 只发送一次。
receipt = ks.sess_send_text(
    session=conn.session, data="show version\r", collect_output=False,
)
# Ctrl-C 同样直接用 Python 的实际控制字符，无需 escaped 方法。
# ks.sess_send_text(session=conn.session, data="\x03", collect_output=False)
```

只有明确持有可见转义文本、需要工具解码一层时才使用 `*_escaped`，例如 `sess_send_escaped(data=r"\x03", ...)`；`sess_send_text(data=r"\x03", ...)` 则发送字面的反斜杠和 `x03`。非 ASCII 精确字节是另一个合理使用场景：Python `"\xff"` 是 Unicode U+00FF，按 UTF-8 发送会成为 C3 BF；需要原始 FF 字节时可以显式使用 escaped 的 `r"\xff"`。此优先规则只针对 send 的 `data`；`line_end` 仍是由工具解码的可见转义配置，例如 `line_end=r"\r"`，正则参数也保留自己的语法。

SSH 网络设备推荐 `mode="stream"`，`sess_sendline_text` 默认追加 CR；Telnet 默认追加 CRLF，可显式配置 `line_end`。`sendline` 总是追加一次，数据本身包含行尾时也不去重。`sent/written_bytes` 只确认本地写入，不证明设备收到、密码被接受或 shell 就绪。空输出不触发自动重试。

## 结果、错误和生命周期

- 返回值是不同结果类型的 dataclass，例如 `ConnectResult`、`ExecResult`、`ReadResult`。嵌套配置、peer、信号、列表条目支持点语法，例如 `ks.sess_get_config(session=conn.session).config.read_only`。
- `to_dict()` 只输出服务端实际返回的字段，包含未来新增的 `extra` 字段。已声明但缺失的字段通常为 `None`，`output` 缺失时便利值为 `""`；`to_dict()` 仍区分缺失和空字符串。未知字段也可以点语法访问；不改写 schema、env 等任意字典键。
- `ToolError.result` 保留类型化结果，`ToolError.envelope` 保留 MCP 原始结果。`ProtocolError` 表示协议/版本问题；`TransportError` 表示进程或管道故障。均不自动重启或重放。
- `CallTimeout` 是 Python 本地响应截止时间，默认 `rpc_timeout=125` 秒；启动握手默认 `startup_timeout=15` 秒，均可在构造函数修改，只接受有限正数。工具自身的 `*_timeout_ms` 保持 MCP 语义：单 RPC 约 115 秒；默认 exec 等待 120 秒，最高 300 秒；RPC 预算先到返回句柄且不发送 TERM。
- 客户端本地超时或异步取消会发送 MCP 取消通知，继续排空迟到响应。取消不保证终止命令；卡住的写入取消仍可能关闭共享 transport。配置的 exec 完成等待超时依然默认尝试 TERM，只有显式 `term_on_timeout=False` 才关闭此行为。
- 单客户端最多 32 个本地在途请求、64 个待写帧；达到上限立即 `ClientBusy`，该调用未发送。引擎自己的 busy/背压、会话/任务资源上限同样有效。进程不读取输入时，管道写入 5 秒超时会终止子进程，执行结果可能未知。
- 使用 `with Keepsess()` / `async with AsyncKeepsess()`；不用上下文管理器时显式 `start()` / `close()`。首次方法调用也会启动进程。关闭有界等待并清理进程，幂等；不能重新打开同一客户端。同步客户端可跨线程调用，异步客户端限于同一事件循环。
- 每个客户端独立启动并持有一个进程和连接，不接管 AI 当前会话。创建多个客户端会产生多个实例；`sN/taskN` 仅在所属实例中有效。

## 人类 Web 观察

原样使用 `keepsess serve` 和 `~/.keepsess.toml`：Web 默认开启，实例加入每用户共享 daemon。人类可以实时查看和回看归档，Web 读取不消耗 Python 的输出游标。`ks.meta.web` 和 `ks.sess_list().web` 提供入口信息；Web 启动失败不会阻止 MCP 功能。使用 `sess_configure(instance_name=...)` 给脚本命名。

页面默认只读，人工解锁输入可能与脚本竞争。关闭 Python 客户端会关闭自己的连接，daemon 继续运行并保留已有归档。历史继承原工具策略，可能保存终端输入中的密码；连接认证字段不进入结果。`stderr_tail` 是显式诊断入口，最多保留 64KiB，不自动打印。

## 开发与验证

```sh
PYTHONPATH=python python3 -m unittest discover -s python/tests -v
# Go 工具定义变化后生成/检查 30 个显式方法：
python3 python/tools/generate.py
python3 python/tools/generate.py --check

# 可选真实 SSH/Telnet 集成测试，使用临时配置目录：
CGO_ENABLED=0 go build -o /tmp/keepsess-sdk ./cmd/keepsess
CGO_ENABLED=0 go build -o /tmp/keepsess-sdk-ssh ./python/tests/ssh_fixture.go
PYTHONPATH=python KEEPSESS_TEST_BINARY=/tmp/keepsess-sdk \
  KEEPSESS_TEST_SSH_FIXTURE=/tmp/keepsess-sdk-ssh \
  python3 -m unittest discover -s python/tests -v
```

`GO=/path/to/go` 可为生成脚本指定 Go 路径。生成的 API 已纳入源码，普通安装和导入无需运行生成器。SDK 提供全部 30 个同名方法；MCP tools/list 同样统一展示全部 30 个工具，不存在隐藏工具或发现分组。
