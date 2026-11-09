"""Failures preserve uncertainty: a timeout never proves input was not sent."""
from __future__ import annotations
from typing import Any


class KeepsessError(Exception):
    """Base SDK error."""


class TransportError(KeepsessError):
    """Process/stdio failed. No automatic restart or replay is attempted."""


class ProtocolError(KeepsessError):
    """Invalid MCP response, unsupported engine, or JSON-RPC error."""

    def __init__(self, message: str, *, code: int | None = None):
        super().__init__(message)
        self.code = code


class CallTimeout(KeepsessError):
    """Local response deadline; the remote operation may still have executed."""

    def __init__(self, request_id: int, tool: str):
        super().__init__(f"Response deadline for {tool}; execution outcome may be unknown. "
                         "Inspect sessions/tasks; do not automatically replay input.")
        self.request_id = request_id
        self.tool = tool


class ClientBusy(KeepsessError):
    """Local in-flight limit reached; this call was not sent."""


class ToolError(KeepsessError):
    """MCP tool error, with its complete typed result and original envelope."""

    def __init__(self, tool: str, result: Any, envelope: dict[str, Any]):
        super().__init__(f"{tool}: {getattr(result, 'error', None) or 'tool failed'}")
        self.tool = tool
        self.result = result
        self.envelope = envelope
