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
	"github.com/zytyan/keepsess/internal/daemon"
	"github.com/zytyan/keepsess/internal/history"
	"github.com/zytyan/keepsess/internal/mcp"
	"github.com/zytyan/keepsess/internal/settings"
	"github.com/zytyan/keepsess/internal/web"
)

//go:embed skill.md
var Skill string

const Help = `keepsess — 面向 AI Agent 的持久 SSH / Telnet MCP 工具 (Go 1.22.1+)

用法:
  keepsess help                         查看用途和子命令
  keepsess serve [--log PATH]           启动 MCP stdio，加入共享 Web daemon
  keepsess mcp-skill                    输出 AI 操作指南
  keepsess install [FILE] [--client X]  安装二进制、MCP 配置和 skill
  keepsess install-skill [--dir PATH]   安装 SKILL.md (默认 ./keepsess-skill)
  keepsess version                     查看版本
  keepsess daemon status|stop|run|upgrade      查询 / 停止 / 前台运行 / 从当前二进制升级共享 daemon

全部 30 个工具统一可发现，提供完整参数说明，无需切换发现分组。
完整接口：get_file/put_file、tunnel_open/list/close、help、connect/reconnect/rename/task_rename/configure/get_config/list、exec、四种 send/sendline、
read_available/read_until/read_tail/read_history、task_list/status/wait/signal/release/close_stdin、close/close_all（均带 sess_ 前缀）。
sess_exec 用于 SSH mode=exec，默认 login=true 使用 Bash 登录 shell、stdin EOF；interactive=true 显式保留 stdin，pty=true 需 interactive。
sess_exec 仅在恰好保留一个会话时可省略 session；多会话须明确指定，断开的会话也计数。
mode=stream 调用 exec 直接报错，使用 sendline_text/escaped。
持久终端 shell、console 登录和全部 Telnet 使用 mode=stream；交互 exec 按 task handle send/read。
sess_task_close_stdin(handle) 发送 EOF，保留输出与退出状态；Ctrl-D 字节不等于关闭 stdin。
sess_help() 列内置目录；topic/parameter 定点查看，query 搜索短摘要（默认 3 条），不访问端点/文件/网络。
工具发现保留类型、默认值和关键约束，详细说明按需 help；无需每次调用前查询帮助。
读取默认总时限 30000ms、静默时限 500ms；所有工具参数均在顶层，无需 config 嵌套。
connect/configure 设置会话默认值；操作顶层配置仅覆盖本次调用。
connect/exec 可传 name；rename/task_rename 修改别名，稳定 sN/taskN ID 保持有效；重复名字不发送。
instance_id 仅用于实例诊断，不要求每次传入；未知句柄先 list 检查，禁止重放。
exec 可传 cwd/env，在登录初始化后应用；login=false 且无 cwd/env 时原样提交 command。
收集支持 strip_ansi/include_pattern/exclude_pattern，仅投影结果、保留原始历史；scan_limit 时继续分页。
等待返回 wait_elapsed_ms、task_elapsed_ms、timeout_source；RPC 预算耗尽不影响下一次等待。
connect 可用 ssh_jumps 数组指定最多 8 级跳板（近端在前），每跳独立认证/主机校验，整链共用连接超时；目标支持 SSH 或 Telnet（仅 stream）。
exec/task_wait 顶层参数 exec_total_timeout_ms（默认 120000ms，最高 300000ms）；term_on_timeout 默认 true，超时尝试 TERM，不保证退出。
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
用户配置统一读取 ~/.keepsess.toml，首次启动生成默认配置，Agent 只需 keepsess serve。
首个进程自动启动当前用户的共享 daemon（默认 127.0.0.1:8765，占用时自动换端口）。
所有活动进程共用 Web 入口，initialize._meta.web / sess_list.web 可查 URL；进程退出仍保留历史。
daemon 空闲不退出；SSH/Telnet 仍在原 Agent 进程内，daemon 重启仅恢复观察，不重放输入。
[web] enabled/listen/base_path/public_url、[history] dir/max_bytes、audit_log 配置替代启动参数。
IP/localhost 反代入口保留 Host（含端口），页面/API/WS 使用相对路径；DNS 名设置 web.public_url。
升级二进制后用 keepsess daemon upgrade 替换共享 daemon；兼容 Agent 会重新注册。
Web 本机免登录，每次刷新/重连恢复只读，解锁仍受会话 read_only 保护；滚动历史默认 5000 行，可调整并记住。
历史包含命令、原始输入和输出（可能包含密码）；连接认证参数/env 字段不写入。关闭会话仍保留 Web 历史。
--log 写入 0600 JSONL 审计；认证字段脱敏，command/data 仍可能含敏感信息。
Telnet 为明文；SSH 默认不验证主机密钥，显式 known_hosts/host_key 可启用。连接只在本次服务进程内持久化。
`

var launchDaemon = daemon.Launch

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
	case "daemon":
		if len(args) != 2 {
			return errors.New("usage: keepsess daemon run|status|stop|upgrade")
		}
		var cfg settings.Config
		var err error
		if home := os.Getenv("KEEPSESS_SETTINGS_HOME"); home != "" {
			cfg, err = settings.LoadHome(home)
		} else {
			cfg, err = settings.Load()
		}
		if err != nil {
			return err
		}
		switch args[1] {
		case "check":
			if !cfg.Web.Enabled {
				return errors.New("web.enabled=false; daemon disabled")
			}
			return nil
		case "upgrade":
			r, e := daemon.Upgrade(ctx, cfg)
			if e != nil {
				return e
			}
			r.Token = ""
			r.Endpoint = ""
			return json.NewEncoder(out).Encode(r)
		case "run":
			return daemon.Run(ctx, cfg)
		case "status":
			r, e := daemon.ReadRuntime(cfg)
			if e != nil {
				return e
			}
			info, e := daemon.Status(ctx, r)
			if e != nil {
				return e
			}
			return json.NewEncoder(out).Encode(info)
		case "stop":
			if e := daemon.Stop(ctx, cfg); e != nil {
				return e
			}
			_, e := fmt.Fprintln(out, "daemon stop requested; active Agents may start it again and reconnect without replaying input")
			return e
		default:
			return errors.New("usage: keepsess daemon run|status|stop|upgrade")
		}
	case "serve":
		cfg, err := settings.Load()
		if err != nil {
			return err
		}
		// Preserve the explicitly supported audit flag. Agent configurations need
		// only ["serve"]; all persistent user preferences live in ~/.keepsess.toml.
		f := flag.NewFlagSet("serve", flag.ContinueOnError)
		f.SetOutput(errout)
		auditPath := f.String("log", cfg.AuditLog, "Legacy audit override; prefer audit_log in ~/.keepsess.toml")
		if e := f.Parse(args[1:]); e != nil {
			return fmt.Errorf("%w; configure ~/.keepsess.toml instead of Web/history arguments", e)
		}
		if f.NArg() != 0 {
			return errors.New("unexpected arguments; configure ~/.keepsess.toml")
		}
		s := &mcp.Server{Manager: core.NewManager()}
		defer s.Manager.CloseAll()
		if cfg.Web.Enabled {
			info := web.Info{InstanceID: s.Manager.InstanceID()}
			runtime, e := daemon.Ensure(ctx, cfg, launchDaemon)
			if e != nil {
				info.Error = e.Error()
			} else {
				if runtime.ConfigHash != cfg.Fingerprint() {
					fmt.Fprintln(errout, "keepsess: daemon uses previous Web/history settings; run keepsess daemon stop to reload ~/.keepsess.toml")
				}
				cfg.History.Dir = runtime.HistoryDir
				cfg.History.MaxBytes = runtime.HistoryMaxBytes
				store, e := history.Open(cfg.History.Dir, s.Manager.InstanceID(), cfg.History.MaxBytes)
				if e != nil {
					info.Error = e.Error()
				} else {
					defer func() {
						store.Close()
						if state := store.Status(); state.Error != "" {
							fmt.Fprintln(errout, "keepsess history:", state.Error)
						}
					}()
					s.Manager.SetEventSink(store)
					client := daemon.Connect(ctx, cfg, s.Manager, store, runtime, launchDaemon)
					defer client.Close()
					s.WebInfo = func() any { return client.Info() }
					fmt.Fprintln(errout, "keepsess Web:", runtime.Info.URL, "(shared daemon; input archived; default read-only)")
				}
			}
			if s.WebInfo == nil {
				s.WebInfo = func() any { return info }
				fmt.Fprintln(errout, "keepsess Web unavailable:", info.Error)
			}
		}
		if *auditPath != "" {
			a, e := OpenAudit(*auditPath)
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
			case "password", "key_passphrase", "token", "private_key", "env":
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
