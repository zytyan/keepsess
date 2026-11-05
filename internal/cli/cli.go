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

15 个工具：connect/configure/get_config/list、exec、四种 send/sendline、
read_available/read_until/read_tail/read_history、close/close_all（均带 sess_ 前缀）。
sess_exec 仅用于非交互 SSH mode=exec；stream 直接报错，使用 sendline_text/escaped。
交互式 Bash/shell、console 登录和全部 Telnet 使用 mode=stream 的 send/read，禁止 sess_exec。
读取默认总时限 30000ms、静默时限 500ms；config 可持久设置或按调用覆盖。
exec 使用独立 exec_total_timeout_ms（默认 30000ms），不受 read 超时/end_pattern 影响。
send_text 编码文字；send_escaped 显式解码一层转义；sendline 追加配置结尾。
发送默认顺便收集未读流，collect_output=false 仅发送；不归因输入输出，不自动重放。
read_only 默认 false；true 禁止 send/exec，只能通过 connect/configure 设置，不能按调用覆盖。
sent/written_bytes 仅确认本地写入，不证明设备收到、密码正确或 shell 就绪。
空输出/start=end 时认证状态未知；有界只读等待，不重发密码或补发回车。
默认增量输出 8KiB，上限 64KiB；history 1MiB；close 丢弃连接和缓冲，无正文。
读写可并行，同方向竞争 busy，不无限排队；RPC 上限 115s，写入超时断连。
空闲会话默认 30min 回收，可禁用；session 使用 s1、s2 等短句柄。
TCP keepalive 默认 15000ms；Linux 支持 TCP 队列阈值，Windows 明确报告不支持。
MCP 配置: {"command":"/path/to/keepsess","args":["serve"]}
--log 写入 0600 JSONL 审计；认证字段脱敏，command/data 仍可能含敏感信息。
Telnet 为明文；SSH 默认验证主机密钥。连接只在本次服务进程内持久化。
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
