package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	pb "github.com/nekopass/nekopass/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var uninstallUnit = regexp.MustCompile("^nekopass-agent(?:-[a-z0-9]{1,16})?-uninstall[.]path$")

// SetUninstallDirectory is called before the engine connects to the panel.
func (e *Engine) SetUninstallDirectory(directory, unit string) {
	if !uninstallUnit.MatchString(unit) || directory != filepath.Join("/var/lib", strings.TrimSuffix(unit, "-uninstall.path")) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", unit).Run() == nil {
		e.uninstallDirectory = directory
	}
}

func validUninstallTicket(ticket string) bool {
	if len(ticket) != 64 || strings.ToLower(ticket) != ticket {
		return false
	}
	_, err := hex.DecodeString(ticket)
	return err == nil
}

func (e *Engine) stageUninstall(ticket string) error {
	if ticket == "" || e.uninstallDirectory == "" {
		return nil
	}
	if !validUninstallTicket(ticket) {
		return errors.New("invalid uninstall task")
	}
	if ticket == e.uninstallTicket && time.Since(e.uninstallWrittenAt) < 10*time.Second {
		return nil
	}
	request := filepath.Join(e.uninstallDirectory, "uninstall-request.json")
	if _, err := os.Lstat(request); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(e.uninstallDirectory, ".uninstall-request-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	err = json.NewEncoder(tmp).Encode(map[string]string{"ticket": ticket})
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// The watcher must see a complete request with a single filename.
	if err = os.Rename(tmp.Name(), request); err != nil {
		return err
	}
	e.uninstallTicket, e.uninstallWrittenAt = ticket, time.Now()
	return nil
}

// ReportUninstall is run by the installed helper before and after it acts.
func ReportUninstall(server, token, ticket, detail string, checkOnly bool) error {
	if server == "" || token == "" || !validUninstallTicket(ticket) || len(detail) > 500 || (checkOnly && detail != "") {
		return errors.New("invalid managed uninstall report")
	}
	target, transport, err := controlEndpoint(server)
	if err != nil {
		return errors.New("invalid managed uninstall endpoint")
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(transport))
	if err != nil {
		return errors.New("cannot connect to panel to report uninstall")
	}
	defer conn.Close()
	client := pb.NewControlClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	request := &pb.UninstallReport{Ticket: ticket, Error: detail, CheckOnly: checkOnly}
	for {
		attempt, stop := context.WithTimeout(ctx, 4*time.Second)
		_, err = client.ReportUninstall(attempt, request, grpc.WaitForReady(true))
		stop()
		switch status.Code(err) {
		case codes.OK:
			return nil
		case codes.Unavailable, codes.DeadlineExceeded:
			select {
			case <-ctx.Done():
				return errors.New("panel did not confirm uninstall report")
			case <-time.After(time.Second):
			}
		default:
			return errors.New("panel rejected uninstall report")
		}
	}
}
