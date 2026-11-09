# keepsess Python SDK

复用 keepsess Go 引擎的 Python 库：30 个 MCP 同名方法、显式关键字参数、dataclass 点语法结果，以及同步和异步客户端。零 Python 运行时依赖。

需要 **Python 3.10+** 和独立安装的 **keepsess 0.3.x** 二进制；库不下载引擎。

## 安装或直接导入

从仓库根目录安装到当前 Python 环境：

```sh
python -m pip install ./python
# 开发时：python -m pip install -e ./python
```

也可免安装，通过标准环境变量直接导入：

```sh
export PYTHONPATH="$PWD/python${PYTHONPATH:+:$PYTHONPATH}"
export KEEPSESS_BINARY="$PWD/dist/keepsess-linux-amd64"
```

Windows PowerShell：

```powershell
$env:PYTHONPATH = "$PWD\python;$env:PYTHONPATH"
$env:KEEPSESS_BINARY = "$PWD\dist\keepsess-windows-amd64.exe"
```

引擎路径优先使用 `Keepsess(binary="/path/to/keepsess")`，其次是 `KEEPSESS_BINARY`，未设置时从 PATH 查找。安装和导入 Python 包无需 Go。

## 使用

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
    print(result.output)
    print(result.exit_code, result.warning)
```

异步程序使用 `async with AsyncKeepsess() as ks`，并 `await ks.sess_*()`。同步客户端支持跨线程调用；异步客户端应在同一事件循环中使用。

## 关键约定

- 每个客户端持有一个长期运行的 `keepsess serve`，自行建连，不接管 AI 现有会话。使用上下文管理器，或显式 `close()`；关闭连接不保证远端程序退出。
- 参数名、毫秒单位、执行语义和 MCP 一致，配置保持顶层。`None` 表示省略；`False`、`0`、空字符串原样发送。connect/configure 修改默认值，操作参数只影响本次。
- 返回值支持 `conn.session`、`result.peer.server`、`config.config.read_only` 等点语法；`to_dict()` 保留服务端实际字段，未知字段存入 `extra`。
- `ToolError.result` 保留完整类型化结果；远端非零退出码本身不抛出工具异常。`exit_code=None` 不代表成功，空输出不代表没有执行。
- Python 优先用 `sess_send_text` / `sess_sendline_text`。普通字符串 `"\r"`、`"\x03"` 已包含实际控制字符；原始字符串 `r"\x03"` 保留字面反斜杠。只有明确需要再解码一层时使用 `*_escaped`。`line_end` 仍是工具解释的可见转义，如 `r"\r"`。
- Python 本地响应等待默认 `rpc_timeout=125` 秒，启动握手默认 `startup_timeout=15` 秒；构造时可修改，只接受有限正数。服务端单 RPC 仍为 115 秒，exec 默认等待 120 秒；配置的完成等待超时默认尝试 TERM，RPC 预算到期不发 TERM。
- 长命令可用 `collect_output=False` 启动，再通过 handle 调用 `sess_task_wait` / read；需要自行设置总预算。`term_on_timeout=False` 只结束等待。客户端不自动重连、重启或重放输入。
- 输出默认每页 64KiB，应处理 `more`、截断和历史丢失。并发、队列、输入输出均有界；本地超时或取消不保证远端任务终止。

## 人类观察与详细用法

沿用 `~/.keepsess.toml`，Web 默认开启，Python 实例加入现有共享界面。`ks.meta.web` 或 `ks.sess_list().web` 提供入口；使用 `instance_name` 命名。观察不消耗脚本读取游标，退出后保留归档。人工输入可能与脚本竞争；历史可能保存终端输入中的密码。

[完整指南与长任务示例](guide.md) · [SSH 示例](examples/ssh_exec.py) · [项目说明](../README.md)

开发检查（仓库根目录）：

```sh
PYTHONPATH=python python3 -m unittest discover -s python/tests -v
python3 python/tools/generate.py --check
```
