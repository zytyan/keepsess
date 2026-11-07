package cli

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/zytyan/keepsess/internal/buildinfo"
	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/mcp"
)

//go:embed skill.md
var Skill string

const Help = `keepsess — 面向 AI Agent 的持久 SSH / Telnet MCP 工具 (Go 1.22.1+)

用法:
  keepsess help                         查看用途和子命令
  keepsess serve [--log PATH]           显式启动 MCP stdio 服务
  keepsess mcp-skill                    输出 AI 操作指南
  keepsess install [FILE] [--client X]  安装二进制、MCP 配置和 skill
  keepsess install-skill [--dir PATH]   安装 SKILL.md (默认 ./keepsess-skill)
  keepsess version                     查看版本

22 个工具：connect/reconnect/configure/get_config/list、exec、四种 send/sendline、
read_available/read_until/read_tail/read_history、task_list/status/wait/signal/release/close_stdin、close/close_all（均带 sess_ 前缀）。
sess_exec 用于 SSH mode=exec，默认 stdin EOF；interactive=true 显式保留 stdin，pty=true 需 interactive。
sess_exec 仅在恰好保留一个会话时可省略 session；多会话须明确指定，断开的会话也计数。
mode=stream 调用 exec 直接报错，使用 sendline_text/escaped。
持久终端 shell、console 登录和全部 Telnet 使用 mode=stream；交互 exec 按 task handle send/read。
sess_task_close_stdin(handle) 发送 EOF，保留输出与退出状态；Ctrl-D 字节不等于关闭 stdin。
读取默认总时限 30000ms、静默时限 500ms；config 可持久设置或按调用覆盖。
exec/task_wait 使用 exec_total_timeout_ms（默认 120000ms，最高 300000ms）；term_on_timeout 默认 true，超时尝试 TERM，不保证退出。
同步调用互斥，返回即释放名额；异步返回 taskN，可与同步/异步并行，无后台运行时限。
正常同步完成且输出返回完毕时省略 handle；exec 后续 read 必须用 handle，stream 用 session。
普通 read 超时、取消等待和输出上限不发 TERM；exec 不再因 110s 无输出关闭。
send_text 编码文字；send_escaped 显式解码一层转义；sendline 追加配置结尾。
发送默认顺便收集未读流，collect_output=false 仅发送；不归因输入输出，不自动重放。
reconnect 显式复用参数与当前配置，保留句柄、清空旧输出，不重放输入；close 后不能重连。
read_only 默认 false；true 禁止 send/exec/信号，只能通过 connect/configure 设置，不能按调用覆盖。
sent/written_bytes 仅确认本地写入，不证明设备收到、密码正确或 shell 就绪。
空输出附 comment 解释当前观察，不证明设备未发送或执行/认证成功；start=end 时认证状态未知；有界只读等待，不重发密码或补发回车。
默认增量输出 64KiB，上限 512KiB；history 1MiB；close 丢弃连接和缓冲，无正文。
读写可并行，同方向竞争 busy，不无限排队；RPC 上限 115s，写入超时断连。
空闲会话默认 2h 回收，可禁用；session 使用 s1、s2 等短句柄。
SSH 协议 keepalive 默认 30000ms、回复时限 10000ms；RPC 预算先到不发 TERM，保留任务 handle。
TCP keepalive 默认 15000ms；Linux 支持 TCP 队列阈值，Windows 明确报告不支持。
MCP 配置: {"command":"/path/to/keepsess","args":["serve"]}
--log 写入 0600 JSONL 审计；认证字段脱敏，command/data 仍可能含敏感信息。
Telnet 为明文；SSH 默认不验证主机密钥，显式 known_hosts/host_key 可启用。连接只在本次服务进程内持久化。
`

func Run(ctx context.Context, args []string, in io.Reader, out, errout io.Writer) error {
	if len(args) == 0 {
		_, e := io.WriteString(out, Help)
		return e
	}
	switch args[0] {
	case "help", "--help", "-h":
		_, e := io.WriteString(out, Help)
		return e
	case "version":
		_, e := fmt.Fprintln(out, "keepsess "+buildinfo.String())
		return e
	case "mcp-skill":
		_, e := io.WriteString(out, Skill)
		return e
	case "install":
		return installCommand(args[1:], out, errout)
	case "install-skill":
		f := flag.NewFlagSet("install-skill", flag.ContinueOnError)
		f.SetOutput(errout)
		dir := f.String("dir", "keepsess-skill", "Skill destination directory")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		if f.NArg() != 0 {
			return errors.New("unexpected arguments")
		}
		if e := os.MkdirAll(*dir, 0755); e != nil {
			return e
		}
		p := filepath.Join(*dir, "SKILL.md")
		w, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if e != nil {
			return e
		}
		_, e = io.WriteString(w, Skill)
		ce := w.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		_, e = fmt.Fprintln(out, p)
		return e
	case "serve":
		f := flag.NewFlagSet("serve", flag.ContinueOnError)
		f.SetOutput(errout)
		path := f.String("log", "", "Audit JSONL path (authentication redacted)")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		if f.NArg() != 0 {
			return errors.New("unexpected arguments")
		}
		s := &mcp.Server{Manager: core.NewManager()}
		if *path != "" {
			a, e := OpenAudit(*path)
			if e != nil {
				return e
			}
			defer a.Close()
			s.Audit = a
		}
		return s.Serve(ctx, in, out)
	default:
		return fmt.Errorf("unknown subcommand %q; run keepsess help", args[0])
	}
}

type auditEntry struct {
	b   []byte
	ack chan error
}
type Logger struct {
	file    *os.File
	queue   chan auditEntry
	mu      sync.Mutex
	failure error
	done    chan struct{}
	once    sync.Once
}

func OpenAudit(path string) (*Logger, error) {
	if st, e := os.Lstat(path); e == nil {
		if !st.Mode().IsRegular() || (runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0) {
			return nil, errors.New("existing audit file must be regular and private (0600)")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if e != nil {
		return nil, e
	}
	a := &Logger{file: f, queue: make(chan auditEntry, 32), done: make(chan struct{})}
	go func() {
		for {
			var r auditEntry
			select {
			case <-a.done:
				return
			case r = <-a.queue:
			}
			_, e := f.Write(r.b)
			a.mu.Lock()
			if e != nil {
				a.failure = e
			}
			a.mu.Unlock()
			r.ack <- e
		}
	}()
	return a, nil
}
func redact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			switch k {
			case "password", "key_passphrase", "token", "private_key":
				t[k] = "[REDACTED]"
			default:
				t[k] = redact(x)
			}
		}
	case []any:
		for i, x := range t {
			t[i] = redact(x)
		}
	}
	return v
}
func (a *Logger) Record(v any) error {
	a.mu.Lock()
	e := a.failure
	a.mu.Unlock()
	if e != nil {
		return e
	}
	b, e := json.Marshal(map[string]any{"time": time.Now().UTC().Format(time.RFC3339Nano), "input": redact(v)})
	if e != nil {
		return e
	}
	r := auditEntry{b: append(b, '\n'), ack: make(chan error, 1)}
	select {
	case <-a.done:
		return errors.New("audit closed")
	case a.queue <- r:
	default:
		return errors.New("audit queue full; input was not executed")
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case e := <-r.ack:
		return e
	case <-timer.C:
		return errors.New("audit write deadline; input was not executed")
	}
}
func (a *Logger) Close() { a.once.Do(func() { close(a.done); go func() { _ = a.file.Close() }() }) }
