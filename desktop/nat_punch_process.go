package desktop

import (
	"encoding/json"
	"strconv"

	"hole/core"
)

type natProcessRequest struct {
	TransportID   string `json:"transport_id"`
	AfterSequence string `json:"after_sequence"`
	Generation    string `json:"generation"`
	Stream        string `json:"stream"`
	Limit         int    `json:"limit"`
}

type natProcessAttempt struct {
	Generation uint64 `json:"generation,string"`
	Phase      string `json:"phase"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	ElapsedMS  int64  `json:"elapsed_ms,string"`
	State      string `json:"state"`
	Stage      string `json:"stage"`
}

type natProcessResult struct {
	Process      core.NATPunchProcess `json:"nat_punch_process"`
	Generation   uint64               `json:"selected_generation,string"`
	Stream       string               `json:"stream"`
	Attempts     []natProcessAttempt  `json:"attempts"`
	HasDiscovery bool                 `json:"has_discovery"`
	More         bool                 `json:"more"`
}

func (h *host) natPunchProcess(raw json.RawMessage) (any, *rpcError, bool) {
	reader, ok := h.engine.(interface {
		NATPunchProcess(string, uint64) core.NATPunchProcess
	})
	if !ok {
		return nil, rpcFault(methodNotFound, "method_not_found", "当前核心未提供打洞过程记录"), false
	}
	var query natProcessRequest
	if !onlyFields(raw, "transport_id", "after_sequence", "generation", "stream", "limit") || decodeStrict(raw, &query) != nil || len(query.TransportID) == 0 || len(query.TransportID) > 256 {
		return nil, rpcFault(invalidParams, "invalid_params", "需要有效的 transport_id 与过程读取参数"), false
	}
	if query.AfterSequence == "" {
		query.AfterSequence = "0"
	}
	after, err := strconv.ParseUint(query.AfterSequence, 10, 64)
	if err != nil || len(query.AfterSequence) > 20 {
		return nil, rpcFault(invalidParams, "invalid_params", "after_sequence 需要十进制序号字符串"), false
	}
	var generation uint64
	if query.Generation != "" {
		generation, err = strconv.ParseUint(query.Generation, 10, 64)
		if err != nil || len(query.Generation) > 20 {
			return nil, rpcFault(invalidParams, "invalid_params", "generation 需要十进制代次字符串"), false
		}
	}
	if query.Stream == "" {
		query.Stream = "connection"
	}
	if query.Stream != "connection" && query.Stream != "discovery" {
		return nil, rpcFault(invalidParams, "invalid_params", "stream 需要 connection 或 discovery"), false
	}
	if query.Limit == 0 {
		query.Limit = 256
	}
	if query.Limit < 1 || query.Limit > 256 {
		return nil, rpcFault(invalidParams, "invalid_params", "limit 需在 1–256 之间"), false
	}
	readAfter := after
	if query.Stream == "discovery" {
		readAfter = 0
	}
	process := reader.NATPunchProcess(query.TransportID, readAfter)
	result := natProcessResult{Stream: query.Stream, Attempts: []natProcessAttempt{}, Process: core.NATPunchProcess{
		TransportID: query.TransportID, Attempts: []core.NATPunchProcessRun{}, Events: []core.NATPunchProcessEvent{},
	}}
	selected := -1
	for i, run := range process.Attempts {
		result.Attempts = append(result.Attempts, natProcessAttempt{
			Generation: run.Generation, Phase: run.Phase, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
			ElapsedMS: run.ElapsedMS, State: run.State, Stage: run.Stage,
		})
		if query.Generation == "" || run.Generation == generation {
			selected = i
		}
	}
	if selected < 0 {
		if len(process.Attempts) > 0 && query.Generation != "" {
			return nil, rpcFault(coreError, "process_evicted", "这次连接尝试已移出保留窗口"), false
		}
		return result, nil, false
	}
	run := process.Attempts[selected]
	result.Generation, result.HasDiscovery = run.Generation, run.Discovery != nil
	if query.Stream == "discovery" {
		if run.Discovery == nil {
			return result, nil, false
		}
		process = *run.Discovery
		if after > process.Sequence {
			after = 0
		}
	} else {
		run.Discovery = nil
		process.Attempts = []core.NATPunchProcessRun{run}
	}
	events := make([]core.NATPunchProcessEvent, 0, query.Limit)
	for _, event := range process.Events {
		if query.Stream == "connection" && event.Generation != run.Generation || query.Stream == "discovery" && event.Sequence <= after {
			continue
		}
		if len(events) == query.Limit {
			process.Sequence, result.More = events[len(events)-1].Sequence, true
			break
		}
		events = append(events, event)
	}
	process.Events = events
	result.Process = process
	return result, nil, false
}
