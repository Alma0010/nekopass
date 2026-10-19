package agent

import (
	"encoding/json"
	pb "github.com/nekopass/nekopass/internal/protocol"
	"github.com/nekopass/nekopass/internal/release"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUpdateGenerationSurvivesRestartWithoutReplay(t *testing.T) {
	dir := t.TempDir()
	state, err := OpenState(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	e, err := NewEngine(state, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err = e.SetUpdateDirectory(dir); err != nil {
		t.Fatal(err)
	}
	cfg := &pb.ControlMessage{Update: &pb.UpdateRequest{Generation: 1, Version: "v0.10.0"}}
	if err = e.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "update-request.json")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if json.Unmarshal(data, &request) != nil || request["version"] != "v0.10.0" {
		t.Fatal("bad update request")
	}
	if info, _ := os.Stat(file); runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("request permissions")
	}
	os.Remove(file)
	e.Close()
	restored, err := NewEngine(state, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restored.SetUpdateDirectory(dir)
	if err = restored.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("cached update replayed")
	}
	// Only a matching result generation is accepted; completed needs the actual binary version.
	os.WriteFile(filepath.Join(dir, "update-result.json"), []byte(`{"generation":2,"version":"v0.10.0","state":"completed"}`), 0600)
	report, _ := restored.Report()
	if report.UpdateStatus.State != "queued" {
		t.Fatal("foreign generation accepted")
	}
	os.WriteFile(filepath.Join(dir, "update-result.json"), []byte(`{"generation":1,"version":"v0.10.0","state":"completed"}`), 0600)
	report, _ = restored.Report()
	if release.Version != "v0.10.0" && report.UpdateStatus.State != "failed" {
		t.Fatal("wrong binary version accepted")
	}
	cfg.Update.Generation = 2
	if err = restored.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("new update not queued")
	}
}

func TestUpdateCarriesProxyAndRequiresCapableHelper(t *testing.T) {
	dir := t.TempDir()
	state, err := OpenState(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	e, err := NewEngine(state, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err = e.SetUpdateDirectory(dir); err != nil {
		t.Fatal(err)
	}
	proxy := "https://proxy.example/https://github.com/Alma0010/nekopass/releases/download/"
	req := &pb.UpdateRequest{Generation: 1, Version: "v0.19.3", DownloadBase: proxy}
	e.updateDownloadBaseSupported = false
	if err = e.Apply(&pb.ControlMessage{Update: req}); err != nil {
		t.Fatal(err)
	}
	report, _ := e.Report()
	if report.UpdateDownloadBaseSupported || report.UpdateStatus.State != "failed" {
		t.Fatal("old helper accepted custom source")
	}
	file := filepath.Join(dir, "update-request.json")
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("unsupported helper received custom request")
	}
	req.Generation = 2
	e.updateDownloadBaseSupported = true
	if err = e.Apply(&pb.ControlMessage{Update: req}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil || payload["download_base"] != proxy[:len(proxy)-1] {
		t.Fatal("proxy not carried into privileged helper request")
	}
	report, _ = e.Report()
	if !report.UpdateDownloadBaseSupported {
		t.Fatal("helper capability not advertised")
	}
	req.Generation = 3
	req.DownloadBase = "http://proxy.example/releases"
	if err = e.Apply(&pb.ControlMessage{Update: req}); err == nil {
		t.Fatal("insecure source accepted")
	}
}

func TestManualAgentUpgradeClearsStaleFailure(t *testing.T) {
	dir := t.TempDir()
	previous := release.Version
	defer func() { release.Version = previous }()
	e := &Engine{updateDirectory: dir, updateStatus: &pb.UpdateStatus{Generation: 4, Version: "v0.19.2", State: "queued"}}
	file := filepath.Join(dir, "update-result.json")
	os.WriteFile(file, []byte(`{"generation":4,"version":"v0.19.2","state":"failed","error":"network failure"}`), 0600)
	release.Version = "v0.19.1"
	if e.readUpdateStatus().State != "failed" {
		t.Fatal("real failed update hidden")
	}
	for _, version := range []string{"v0.19.2", "v0.19.3"} {
		release.Version = version
		if status := e.readUpdateStatus(); status.State != "completed" || status.Error != "" {
			t.Fatal("manual upgrade still shows stale failure")
		}
	}
}
