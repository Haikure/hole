package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hole/core"
)

type natProcessExportRequest struct {
	TransportID string `json:"transport_id"`
	Directory   string `json:"directory"`
}

type natProcessExportDocument struct {
	Format      string               `json:"format"`
	Version     int                  `json:"version"`
	ExportedAt  string               `json:"exported_at"`
	CoreVersion string               `json:"core_version"`
	CoreBuildID string               `json:"core_build_id"`
	Process     core.NATPunchProcess `json:"nat_punch_process"`
}

// Export the raw retained trace directly to disk: it includes all attempts and
// discoveries and can exceed the bridge's per-message output limit.
func (h *host) exportNATPunchProcess(raw json.RawMessage) (any, *rpcError, bool) {
	reader, ok := h.engine.(interface {
		NATPunchProcess(string, uint64) core.NATPunchProcess
	})
	if !ok {
		return nil, rpcFault(methodNotFound, "method_not_found", "当前核心未提供打洞过程导出"), false
	}
	var query natProcessExportRequest
	if !onlyFields(raw, "transport_id", "directory") || decodeStrict(raw, &query) != nil ||
		strings.TrimSpace(query.TransportID) == "" || len(query.TransportID) > 256 ||
		!filepath.IsAbs(query.Directory) || len(query.Directory) > 4096 {
		return nil, rpcFault(invalidParams, "invalid_params", "需要有效的 transport_id 和绝对导出目录"), false
	}
	process := reader.NATPunchProcess(query.TransportID, 0)
	if process.TransportID != query.TransportID || len(process.Attempts) == 0 {
		return nil, rpcFault(coreError, "process_empty", "当前设备暂无可导出的过程记录"), false
	}
	snapshot := h.engine.Snapshot()
	now := time.Now().UTC()
	document := natProcessExportDocument{
		Format: "hole-nat-punch-process", Version: 1, ExportedAt: now.Format(time.RFC3339Nano),
		CoreVersion: snapshot.CoreVersion, CoreBuildID: snapshot.CoreBuildID, Process: process,
	}
	path, err := writeNATProcessExport(query.Directory, now, document)
	if err != nil {
		return nil, rpcFault(coreError, "export_failed", "无法保存打洞过程，请检查导出目录和剩余空间"), false
	}
	return struct {
		Path string `json:"path"`
	}{path}, nil, false
}

func writeNATProcessExport(directory string, now time.Time, document natProcessExportDocument) (string, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(directory, ".hole-nat-punch-"+now.Format("20060102-150405")+"-*.json")
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(directory, strings.TrimPrefix(filepath.Base(file.Name()), "."))
	if err := os.Rename(file.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
