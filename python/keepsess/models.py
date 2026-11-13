"""Dataclass views of engine results. to_dict() preserves actual wire fields."""
from __future__ import annotations

from dataclasses import dataclass, field, fields
from typing import Any, TypeVar

T = TypeVar("T", bound="Record")


@dataclass
class Record:
    extra: dict[str, Any] = field(default_factory=dict, repr=False)
    _present: set[str] = field(default_factory=set, repr=False, compare=False)

    @classmethod
    def from_dict(cls: type[T], data: dict[str, Any]) -> T:
        names = {f.name for f in fields(cls)} - {"extra", "_present"}
        return cls(**{k: v for k, v in data.items() if k in names},
                   extra={k: v for k, v in data.items() if k not in names},
                   _present=set(data))

    def __getattr__(self, name: str) -> Any:
        extra = self.__dict__.get("extra", {})
        if name in extra:
            return extra[name]
        raise AttributeError(name)

    def to_dict(self) -> dict[str, Any]:
        def plain(value: Any) -> Any:
            if isinstance(value, Record):
                return value.to_dict()
            if isinstance(value, list):
                return [plain(v) for v in value]
            if isinstance(value, dict):
                return {k: plain(v) for k, v in value.items()}
            return value
        result = dict(self.extra)
        for f in fields(self):
            if f.name in self._present and f.name not in {"extra", "_present"}:
                result[f.name] = plain(getattr(self, f.name))
        return result


@dataclass
class PeerIdentity(Record):
    server: str | None = None
    host_key: str | None = None
    host_key_changed: bool | None = None


@dataclass
class SignalResult(Record):
    name: str | None = None
    request_state: str | None = None
    error: str | None = None


@dataclass
class WebInfo(Record):
    instance_id: str | None = None
    enabled: bool | None = None
    url: str | None = None
    listen: str | None = None
    base_path: str | None = None
    history: Record | None = None
    error: str | None = None


@dataclass
class Result(Record):
    session: str | None = None
    handle: str | None = None
    name: str | None = None
    instance_id: str | None = None
    instance_name: str | None = None
    started_at: str | None = None
    task_elapsed_ms: int | None = None
    wait_elapsed_ms: int | None = None
    timeout_source: str | None = None
    filtered_bytes: int | None = None
    filtered_lines: int | None = None
    stripped_bytes: int | None = None
    line_fragments: bool | None = None
    interactive: bool | None = None
    pty: bool | None = None
    stdin_open: bool | None = None
    peer: PeerIdentity | None = None
    disconnect_reason: str | None = None
    state: str | None = None
    comment: str | None = None
    exit_signal: str | None = None
    signal: SignalResult | None = None
    output: str = ""
    reason: str | None = None
    connected: bool | None = None
    running: bool | None = None
    input_state: str | None = None
    input_bytes: int | None = None
    written_bytes: int | None = None
    exit_code: int | None = None
    warning: str | None = None
    error: str | None = None
    next_action: str | None = None
    guidance: str | None = None
    next_arguments: dict[str, Any] | None = None
    available_profiles: list[str] | None = None
    start: int | None = None
    end: int | None = None
    more: bool | None = None
    dropped_bytes: int | None = None
    minimum_output_bytes: int | None = None
    queued_bytes: int | None = None
    hop_index: int | None = None
    host: str | None = None
    port: int | None = None
    from_host: str | None = None
    stage: str | None = None
    target: str | None = None


@dataclass
class ConnectResult(Result):
    pass


@dataclass
class ExecResult(Result):
    pass


@dataclass
class ReadResult(Result):
    pass


@dataclass
class SendResult(Result):
    pass


@dataclass
class SessionList(Record):
    api_contract: str | None = None
    available_profiles: list[str] = field(default_factory=list)
    instance_id: str | None = None
    instance_name: str | None = None
    sessions: list[Result] = field(default_factory=list)
    web: WebInfo | None = None


@dataclass
class TaskList(Record):
    instance_id: str | None = None
    tasks: list[Result] = field(default_factory=list)


@dataclass
class Config(Record):
    strip_ansi: bool | None = None
    include_pattern: str | None = None
    exclude_pattern: str | None = None
    term_on_timeout: bool | None = None
    read_only: bool | None = None
    connect_timeout_ms: int | None = None
    write_timeout_ms: int | None = None
    session_idle_timeout_ms: int | None = None
    read_total_timeout_ms: int | None = None
    read_idle_timeout_ms: int | None = None
    exec_total_timeout_ms: int | None = None
    ssh_keepalive_ms: int | None = None
    ssh_keepalive_timeout_ms: int | None = None
    tcp_keepalive_ms: int | None = None
    line_end: str | None = None
    encoding: str | None = None
    input_encode_error_action: str | None = None
    output_decode_error_action: str | None = None
    max_input_bytes: int | None = None
    max_output_bytes: int | None = None
    backpressure_bytes: int | None = None
    end_pattern: str | None = None


@dataclass
class ConfigField(Record):
    value: Any = None
    description: str | None = None
    unit: str | None = None


@dataclass
class ConfigResult(Result):
    config: Config | None = None
    fields: dict[str, ConfigField] = field(default_factory=dict)
    capabilities: Record | None = None
    tool_profile: str | None = None
    tool_count: int | None = None


@dataclass
class FileResult(Result):
    src: str | None = None
    dst: str | None = None
    backend: str | None = None
    complete: bool | None = None
    bytes_transferred: int | None = None
    temporary_path: str | None = None


@dataclass
class TunnelResult(Result):
    type: str | None = None
    listen_address: str | None = None
    target_host: str | None = None
    target_port: int | None = None
    closed: bool | None = None
    active_sockets: int | None = None


@dataclass
class HelpResult(Record):
    topics: list[Record] = field(default_factory=list)
    results: list[Record] = field(default_factory=list)
    matched: bool | None = None
    topic: str | None = None
    parameter: str | None = None
    title: str | None = None
    description: str | None = None
    text: str | None = None
    next: str | None = None
    schema: dict[str, Any] | None = None
    inputSchema: dict[str, Any] | None = None
    error: str | None = None


def result_type(tool: str) -> type[Record]:
    if tool in {"connect", "reconnect"}:
        return ConnectResult
    if tool in {"exec", "task_wait", "task_status"}:
        return ExecResult
    if tool.startswith("send"):
        return SendResult
    if tool.startswith("read_"):
        return ReadResult
    return {"list": SessionList, "task_list": TaskList,
            "configure": ConfigResult, "get_config": ConfigResult,
            "get_file": FileResult, "put_file": FileResult,
            "help": HelpResult, "tunnel_open": TunnelResult,
            "tunnel_close": TunnelResult}.get(tool, Result)


def decode_record(cls: type[T], data: dict[str, Any]) -> T:
    value = cls.from_dict(data)
    for key, nested in {"peer": PeerIdentity, "signal": SignalResult,
                        "config": Config, "web": WebInfo,
                        "capabilities": Record, "history": Record}.items():
        if key in data and isinstance(data[key], dict) and hasattr(value, key):
            setattr(value, key, decode_record(nested, data[key]))
    for key, nested in {"sessions": Result, "tasks": Result,
                        "topics": Record, "results": Record}.items():
        if key in data and isinstance(data[key], list) and hasattr(value, key):
            setattr(value, key, [decode_record(nested, item) for item in data[key]])
    if isinstance(value, ConfigResult) and isinstance(data.get("fields"), dict):
        value.fields = {k: ConfigField.from_dict(v) for k, v in data["fields"].items()}
    return value


def decode_result(tool: str, data: Any) -> Any:
    if tool == "tunnel_list" and isinstance(data, list):
        return [decode_record(TunnelResult, item) for item in data]
    if not isinstance(data, dict):
        raise ValueError("Expected a structured result object")
    return decode_record(result_type(tool), data)
