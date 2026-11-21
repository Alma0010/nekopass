package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageUninstallWritesScopedTicket(t *testing.T) {
	directory := t.TempDir()
	engine := &Engine{uninstallDirectory: directory}
	path := filepath.Join(directory, "uninstall-request.json")
	if err := engine.stageUninstall("../../other-agent"); err == nil {
		t.Fatal("accepted an invalid uninstall ticket")
	}
	ticket := strings.Repeat("a", 64)
	if err := engine.stageUninstall(ticket); err != nil {
		t.Fatal(err)
	}
	if err := engine.stageUninstall(strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]string
	if err = json.Unmarshal(data, &request); err != nil || len(request) != 1 || request["ticket"] != ticket {
		t.Fatal("staging did not preserve the in-flight request")
	}
}
