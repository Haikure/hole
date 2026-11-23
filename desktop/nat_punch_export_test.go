package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"hole/core"
)

type punchExportEngine struct {
	*fakeEngine
	process   core.NATPunchProcess
	reads     int
	transport string
	after     uint64
}

func (e *punchExportEngine) NATPunchProcess(transport string, after uint64) core.NATPunchProcess {
	e.reads++
	e.transport, e.after = transport, after
	return e.process
}

func punchExportFixture(t *testing.T) *punchExportEngine {
	t.Helper()
	e := &punchExportEngine{fakeEngine: newFakeEngine()}
	e.state.EngineState, e.state.RunRequested = "running", true
	e.state.CoreVersion, e.state.CoreBuildID = "1.1.0", "export-fixture"
	const fixture = `{
		"transport_id":"pair","peer_id":"peer","sequence":"12","events_dropped":"3","events_evicted":"4",
		"attempts":[
			{"generation":"9007199254740993","phase":"punch","state":"failed","stage":"calibration",
			 "rounds":[{"round":1,"trial":1,"mode":"single",
				"local_model":{"status":"complete","type":"restricted_cone","mapping_behavior":"endpoint_independent",
					"ip_evidence":[{"ip":"198.51.100.1","trend":"stable"},{"ip":"198.51.100.2","trend":"stable"}]},
				"peer_model":{"status":"complete","type":"symmetric","public_ip":"203.0.113.1"},
				"diagnostic":{"error":"calibration no longer follows allocation model"}}],
			 "discovery":{"transport_id":"nat-discovery","events":[{"kind":"mapped","mapped_address":"198.51.100.3:40000"}]}},
			{"generation":"9007199254740994","phase":"punch","state":"connected","stage":"connected","rounds":[]}
		],
		"events":[
			{"sequence":"10","generation":"9007199254740993","kind":"signal_sent","packet":"calibrated","result":"ok"},
			{"sequence":"11","generation":"9007199254740993","kind":"signal_received","packet":"calibrated","result":"refresh"},
			{"sequence":"12","generation":"9007199254740994","kind":"finished","result":"connected"}
		]
	}`
	if err := json.Unmarshal([]byte(fixture), &e.process); err != nil {
		t.Fatal(err)
	}
	return e
}

func punchExportParams(t *testing.T, directory string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(natProcessExportRequest{TransportID: "pair", Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestNATPunchExportPreservesFullHistoryBeyondBridgeLimit(t *testing.T) {
	e := punchExportFixture(t)
	for i := 0; i < 600; i++ {
		e.process.Events = append(e.process.Events, core.NATPunchProcessEvent{
			Sequence: uint64(13 + i), Generation: 9007199254740993,
			Kind: "stun_packet", Detail: strings.Repeat("x", 4096),
		})
	}
	e.process.Sequence = 612
	directory := filepath.Join(t.TempDir(), "exports")
	params := punchExportParams(t, directory)
	messages, err := exchange(t, wireRequest("hello", "hello", "")+
		wireRequest("export", "export_nat_punch_process", string(params))+
		wireRequest("snapshot", "snapshot", ""), e)
	if err != nil || len(messages) != 3 {
		t.Fatalf("messages=%d err=%v", len(messages), err)
	}
	for _, message := range messages {
		if message.Error != nil {
			t.Fatal(message.Error)
		}
	}
	var hello helloResult
	if err := json.Unmarshal(messages[0].Result, &hello); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, method := range hello.Methods {
		found = found || method == "export_nat_punch_process"
	}
	if !found {
		t.Fatal("export capability was not advertised")
	}
	var result struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(messages[1].Result, &result); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= MaxOutputBytes || filepath.Dir(result.Path) != directory ||
		!strings.HasPrefix(filepath.Base(result.Path), "hole-nat-punch-") {
		t.Fatalf("unexpected export: size=%d path=%s", len(data), result.Path)
	}
	var document natProcessExportDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Format != "hole-nat-punch-process" || document.Version != 1 ||
		document.CoreVersion != e.state.CoreVersion || document.CoreBuildID != e.state.CoreBuildID {
		t.Fatalf("wrong export metadata: %+v", document)
	}
	if _, err := time.Parse(time.RFC3339Nano, document.ExportedAt); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document.Process, e.process) {
		t.Fatal("export lost attempts, discovery, model fields, events or exact generation numbers")
	}
	if e.reads != 1 || e.transport != "pair" || e.after != 0 ||
		!reflect.DeepEqual(e.calls, []string{"close"}) || !e.state.RunRequested {
		t.Fatalf("export changed engine lifecycle or reread the trace: reads=%d calls=%v", e.reads, e.calls)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || strings.HasPrefix(entries[0].Name(), ".") {
		t.Fatalf("temporary export remains: %v %v", entries, err)
	}
	info, err := os.Stat(result.Path)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatalf("export permissions: %v %v", info, err)
	}
}

func TestNATPunchExportRejectsInvalidParametersBeforeReading(t *testing.T) {
	directory := t.TempDir()
	valid := punchExportParams(t, directory)
	for _, raw := range []string{
		`null`, `{}`, `{"transport_id":"pair","directory":"relative"}`,
		`{"transport_id":"","directory":"/tmp"}`,
		strings.TrimSuffix(string(valid), "}") + `,"generation":"1"}`,
	} {
		e := punchExportFixture(t)
		_, fault, shutdown := (&host{engine: e}).dispatch(request{Method: "export_nat_punch_process", Params: []byte(raw)})
		if fault == nil || fault.Code != invalidParams || shutdown || e.reads != 0 || len(e.calls) != 0 {
			t.Fatalf("params=%s fault=%+v reads=%d calls=%v", raw, fault, e.reads, e.calls)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid request wrote files: %v %v", entries, err)
	}
}

func TestNATPunchExportEmptyRecordsAndWriteFailure(t *testing.T) {
	for _, scenario := range []string{"empty", "wrong transport", "not a directory", "unsupported"} {
		t.Run(scenario, func(t *testing.T) {
			e := punchExportFixture(t)
			directory := filepath.Join(t.TempDir(), "exports")
			backend := engine(e)
			want := "process_empty"
			switch scenario {
			case "empty":
				e.process.Attempts = nil
			case "wrong transport":
				e.process.TransportID = "other"
			case "not a directory":
				if err := os.WriteFile(directory, []byte("keep this file"), 0600); err != nil {
					t.Fatal(err)
				}
				want = "export_failed"
			case "unsupported":
				backend, want = e.fakeEngine, "method_not_found"
			}
			_, fault, shutdown := (&host{engine: backend}).dispatch(request{
				Method: "export_nat_punch_process", Params: punchExportParams(t, directory),
			})
			if fault == nil || fault.Data.Code != want || shutdown || len(e.calls) != 0 {
				t.Fatalf("fault=%+v calls=%v", fault, e.calls)
			}
			if scenario == "not a directory" {
				data, err := os.ReadFile(directory)
				if err != nil || string(data) != "keep this file" {
					t.Fatal("export overwrote the existing destination")
				}
			} else if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("failed export created its destination", err)
			}
		})
	}
}

func TestNATPunchExportCreatesNewFilesForRepeatedExports(t *testing.T) {
	e := punchExportFixture(t)
	directory := t.TempDir()
	document := natProcessExportDocument{Format: "hole-nat-punch-process", Version: 1, Process: e.process}
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	first, err := writeNATProcessExport(directory, now, document)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	document.CoreBuildID = "second export"
	second, err := writeNATProcessExport(directory, now, document)
	if err != nil || first == second {
		t.Fatalf("first=%s second=%s err=%v", first, second, err)
	}
	retained, err := os.ReadFile(first)
	if err != nil || string(retained) != string(original) {
		t.Fatal("repeated export replaced the earlier file")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
}
