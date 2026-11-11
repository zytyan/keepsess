package daemon

import (
	"encoding/json"
	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/daemon/wire"
	"github.com/zytyan/keepsess/internal/history"
	"time"
)

func encodeRequest(v core.Request) *wire.Request {
	w := &wire.Request{}
	w.Name = v.Name
	if v.Login != nil {
		x := bool(*v.Login)
		w.Login = &x
	}
	w.Cwd = v.Cwd
	w.Env = v.Env
	w.Interactive = v.Interactive
	w.Pty = v.PTY
	w.Session = v.Session
	w.Handle = v.Handle
	w.Signal = v.Signal
	w.Data = v.Data
	w.Command = v.Command
	if v.CollectOutput != nil {
		x := bool(*v.CollectOutput)
		w.CollectOutput = &x
	}
	w.Config = make(map[string][]byte)
	for k, x := range v.Config {
		w.Config[k] = []byte(x)
	}
	w.Lines = int64(v.Lines)
	if v.Offset != nil {
		x := int64(*v.Offset)
		w.Offset = &x
	}
	return w
}
func decodeRequest(w *wire.Request) core.Request {
	var v core.Request
	if w == nil {
		return v
	}
	v.Name = w.Name
	if w.Login != nil {
		x := bool(*w.Login)
		v.Login = &x
	}
	v.Cwd = w.Cwd
	v.Env = w.Env
	v.Interactive = w.Interactive
	v.PTY = w.Pty
	v.Session = w.Session
	v.Handle = w.Handle
	v.Signal = w.Signal
	v.Data = w.Data
	v.Command = w.Command
	if w.CollectOutput != nil {
		x := bool(*w.CollectOutput)
		v.CollectOutput = &x
	}
	v.Config = make(core.ConfigPatch)
	for k, x := range w.Config {
		v.Config[k] = json.RawMessage(x)
	}
	v.Lines = int(w.Lines)
	if w.Offset != nil {
		x := int64(*w.Offset)
		v.Offset = &x
	}
	return v
}
func encodeResult(v core.Result) *wire.Result {
	w := &wire.Result{}
	w.Name = v.Name
	w.InstanceId = v.InstanceID
	w.InstanceName = v.InstanceName
	if v.StartedAt != nil {
		x := v.StartedAt.Format(time.RFC3339Nano)
		w.StartedAt = &x
	}
	w.TaskElapsedMs = v.TaskElapsedMS
	if v.WaitElapsedMS != nil {
		x := int64(*v.WaitElapsedMS)
		w.WaitElapsedMs = &x
	}
	w.TimeoutSource = v.TimeoutSource
	w.FilteredBytes = v.FilteredBytes
	w.FilteredLines = v.FilteredLines
	w.StrippedBytes = v.StrippedBytes
	w.LineFragments = v.LineFragments
	w.Interactive = v.Interactive
	w.Pty = v.PTY
	if v.StdinOpen != nil {
		x := bool(*v.StdinOpen)
		w.StdinOpen = &x
	}
	if v.Peer != nil {
		w.Peer = encodePeerIdentity(*v.Peer)
	}
	w.DisconnectReason = v.DisconnectReason
	w.Session = v.Session
	w.Handle = v.Handle
	w.State = v.State
	w.Comment = v.Comment
	w.ExitSignal = v.ExitSignal
	if v.Signal != nil {
		w.Signal = encodeSignalResult(*v.Signal)
	}
	w.Output = v.Output
	w.Reason = v.Reason
	w.Connected = v.Connected
	w.Running = v.Running
	w.InputState = v.InputState
	w.InputBytes = int64(v.InputBytes)
	w.WrittenBytes = int64(v.WrittenBytes)
	if v.ExitCode != nil {
		x := int64(*v.ExitCode)
		w.ExitCode = &x
	}
	w.Warning = v.Warning
	w.Error = v.Error
	w.NextAction = v.NextAction
	w.Start = v.Start
	w.End = v.End
	w.More = v.More
	w.DroppedBytes = v.DroppedBytes
	w.MinimumOutputBytes = int64(v.MinimumOutputBytes)
	if v.QueuedBytes != nil {
		x := int64(*v.QueuedBytes)
		w.QueuedBytes = &x
	}
	return w
}
func decodeResult(w *wire.Result) core.Result {
	var v core.Result
	if w == nil {
		return v
	}
	v.Name = w.Name
	v.InstanceID = w.InstanceId
	v.InstanceName = w.InstanceName
	if w.StartedAt != nil {
		x, _ := time.Parse(time.RFC3339Nano, *w.StartedAt)
		v.StartedAt = &x
	}
	v.TaskElapsedMS = w.TaskElapsedMs
	if w.WaitElapsedMs != nil {
		x := int64(*w.WaitElapsedMs)
		v.WaitElapsedMS = &x
	}
	v.TimeoutSource = w.TimeoutSource
	v.FilteredBytes = w.FilteredBytes
	v.FilteredLines = w.FilteredLines
	v.StrippedBytes = w.StrippedBytes
	v.LineFragments = w.LineFragments
	v.Interactive = w.Interactive
	v.PTY = w.Pty
	if w.StdinOpen != nil {
		x := bool(*w.StdinOpen)
		v.StdinOpen = &x
	}
	if w.Peer != nil {
		x := decodePeerIdentity(w.Peer)
		v.Peer = &x
	}
	v.DisconnectReason = w.DisconnectReason
	v.Session = w.Session
	v.Handle = w.Handle
	v.State = w.State
	v.Comment = w.Comment
	v.ExitSignal = w.ExitSignal
	if w.Signal != nil {
		x := decodeSignalResult(w.Signal)
		v.Signal = &x
	}
	v.Output = w.Output
	v.Reason = w.Reason
	v.Connected = w.Connected
	v.Running = w.Running
	v.InputState = w.InputState
	v.InputBytes = int(w.InputBytes)
	v.WrittenBytes = int(w.WrittenBytes)
	if w.ExitCode != nil {
		x := int(*w.ExitCode)
		v.ExitCode = &x
	}
	v.Warning = w.Warning
	v.Error = w.Error
	v.NextAction = w.NextAction
	v.Start = w.Start
	v.End = w.End
	v.More = w.More
	v.DroppedBytes = w.DroppedBytes
	v.MinimumOutputBytes = int(w.MinimumOutputBytes)
	if w.QueuedBytes != nil {
		x := int(*w.QueuedBytes)
		v.QueuedBytes = &x
	}
	return v
}
func encodePeerIdentity(v core.PeerIdentity) *wire.PeerIdentity {
	w := &wire.PeerIdentity{}
	w.Server = v.Server
	w.HostKey = v.HostKey
	w.HostKeyChanged = v.HostKeyChanged
	return w
}
func decodePeerIdentity(w *wire.PeerIdentity) core.PeerIdentity {
	var v core.PeerIdentity
	if w == nil {
		return v
	}
	v.Server = w.Server
	v.HostKey = w.HostKey
	v.HostKeyChanged = w.HostKeyChanged
	return v
}
func encodeSignalResult(v core.SignalResult) *wire.SignalResult {
	w := &wire.SignalResult{}
	w.Name = v.Name
	w.RequestState = v.RequestState
	w.Error = v.Error
	return w
}
func decodeSignalResult(w *wire.SignalResult) core.SignalResult {
	var v core.SignalResult
	if w == nil {
		return v
	}
	v.Name = w.Name
	v.RequestState = w.RequestState
	v.Error = w.Error
	return v
}
func encodeEvent(v core.Event) *wire.Event {
	w := &wire.Event{}
	w.Instance = v.Instance
	w.Seq = v.Seq
	w.Time = v.Time.Format(time.RFC3339Nano)
	w.Kind = v.Kind
	w.Session = v.Session
	w.Handle = v.Handle
	w.Epoch = v.Epoch
	w.Name = v.Name
	w.Protocol = v.Protocol
	w.Mode = v.Mode
	w.Encoding = v.Encoding
	w.ReadOnly = v.ReadOnly
	w.Disposed = v.Disposed
	w.EverConnected = v.EverConnected
	w.Data = v.Data
	w.Command = v.Command
	w.Cwd = v.Cwd
	w.Status = encodeResult(v.Status)
	if v.Receipt != nil {
		w.Receipt = encodeResult(*v.Receipt)
	}
	return w
}
func decodeEvent(w *wire.Event) core.Event {
	var v core.Event
	if w == nil {
		return v
	}
	v.Instance = w.Instance
	v.Seq = w.Seq
	v.Time, _ = time.Parse(time.RFC3339Nano, w.Time)
	v.Kind = w.Kind
	v.Session = w.Session
	v.Handle = w.Handle
	v.Epoch = w.Epoch
	v.Name = w.Name
	v.Protocol = w.Protocol
	v.Mode = w.Mode
	v.Encoding = w.Encoding
	v.ReadOnly = w.ReadOnly
	v.Disposed = w.Disposed
	v.EverConnected = w.EverConnected
	v.Data = w.Data
	v.Command = w.Command
	v.Cwd = w.Cwd
	v.Status = decodeResult(w.Status)
	if w.Receipt != nil {
		x := decodeResult(w.Receipt)
		v.Receipt = &x
	}
	return v
}
func encodeHistory(v history.State) *wire.History {
	w := &wire.History{}
	w.Recording = v.Recording
	w.Error = v.Error
	w.LastSeq = v.LastSeq
	w.WrittenSeq = v.WrittenSeq
	w.DurableSeq = v.DurableSeq
	w.LostEvents = v.LostEvents
	return w
}
func decodeHistory(w *wire.History) history.State {
	var v history.State
	if w == nil {
		return v
	}
	v.Recording = w.Recording
	v.Error = w.Error
	v.LastSeq = w.LastSeq
	v.WrittenSeq = w.WrittenSeq
	v.DurableSeq = w.DurableSeq
	v.LostEvents = w.LostEvents
	return v
}
