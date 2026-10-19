package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	pb "github.com/nekopass/nekopass/internal/protocol"
	"github.com/nekopass/nekopass/internal/release"
	bolt "go.etcd.io/bbolt"
)

func (e *Engine) SetUpdateDirectory(directory string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.updateDirectory = directory
	e.updateDownloadBaseSupported = directory != "" && updaterDownloadBaseSupported()
	return e.state.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte("meta")).Get([]byte("update"))
		if len(data) > 0 {
			return json.Unmarshal(data, &e.updateStatus)
		}
		return nil
	})
}
func updaterDownloadBaseSupported() bool {
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, filepath.Join(filepath.Dir(executable), "nekopass-update"), "--capabilities").Output()
	var capabilities struct {
		DownloadBase bool `json:"download_base"`
	}
	return err == nil && len(data) <= 4096 && json.Unmarshal(data, &capabilities) == nil && capabilities.DownloadBase
}

func (e *Engine) saveUpdateStatus() error {
	data, err := json.Marshal(e.updateStatus)
	if err != nil {
		return err
	}
	return e.state.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("meta")).Put([]byte("update"), data) })
}
func (e *Engine) configureUpdate(request *pb.UpdateRequest) error {
	if request == nil {
		return nil
	}
	if request.Generation < 1 || !release.ValidVersion(request.Version) {
		return errors.New("invalid update request")
	}
	if request.DownloadBase != "" {
		if _, err := release.NormalizeDownloadBase(request.DownloadBase); err != nil {
			return err
		}
	}
	if e.updateStatus != nil && request.Generation <= e.updateStatus.Generation {
		return nil
	}
	e.updateStatus = &pb.UpdateStatus{Generation: request.Generation, Version: request.Version, State: "queued"}
	if e.updateDirectory == "" {
		e.updateStatus.State = "failed"
		e.updateStatus.Error = "请先用新版安装脚本接入节点更新服务"
		return e.saveUpdateStatus()
	}
	if request.DownloadBase != "" && !e.updateDownloadBaseSupported {
		e.updateStatus.State = "failed"
		e.updateStatus.Error = "更新组件不支持自定义下载源，请重新执行新版节点安装命令"
		return e.saveUpdateStatus()
	}
	// Persist the consumed generation before a root helper can restart this process.
	if err := e.saveUpdateStatus(); err != nil {
		return err
	}
	payload := map[string]any{"generation": request.Generation, "version": request.Version}
	if request.DownloadBase != "" {
		payload["download_base"], _ = release.NormalizeDownloadBase(request.DownloadBase)
	}
	data, _ := json.Marshal(payload)
	if len(data) > 4096 {
		e.updateStatus.State, e.updateStatus.Error = "failed", "更新下载地址过长，请缩短系统设置中的版本下载根地址"
		return e.saveUpdateStatus()
	}
	tmp, err := os.CreateTemp(e.updateDirectory, ".update-request-*")
	if err == nil {
		name := tmp.Name()
		defer os.Remove(name)
		if _, err = tmp.Write(data); err == nil {
			err = tmp.Sync()
		}
		closeErr := tmp.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(name, filepath.Join(e.updateDirectory, "update-request.json"))
		}
	}
	if err != nil {
		e.updateStatus.State = "failed"
		e.updateStatus.Error = "无法写入节点更新任务"
		return e.saveUpdateStatus()
	}
	return nil
}
func (e *Engine) readUpdateStatus() *pb.UpdateStatus {
	if e.updateStatus == nil || e.updateDirectory == "" {
		return e.updateStatus
	}
	name := filepath.Join(e.updateDirectory, "update-result.json")
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return e.updateStatus
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return e.updateStatus
	}
	var result struct {
		Generation int64  `json:"generation"`
		Version    string `json:"version"`
		State      string `json:"state"`
		Error      string `json:"error"`
	}
	if json.Unmarshal(data, &result) != nil || result.Generation != e.updateStatus.Generation || result.Version != e.updateStatus.Version || len(result.Error) > 500 {
		return e.updateStatus
	}
	if result.State != "running" && result.State != "completed" && result.State != "failed" {
		return e.updateStatus
	}
	if release.AtLeast(release.Version, result.Version) {
		result.State, result.Error = "completed", ""
	} else if result.State == "completed" {
		result.State = "failed"
		result.Error = "更新后的节点版本不匹配，请查看更新服务日志"
	}
	return &pb.UpdateStatus{Generation: result.Generation, Version: result.Version, State: result.State, Error: result.Error}
}
