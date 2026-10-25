//go:build windows

package main

import (
	"context"
	"golang.org/x/sys/windows/svc"
	"meowzond/internal/probe"
	"os"
	"path/filepath"
)

type serviceHandler struct {
	config probe.Config
}

func (h *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	r, e := probe.New(h.config)
	if e != nil {
		return false, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		logFile, e := openServiceLog(h.config)
		if e != nil {
			done <- r.Run(ctx)
			return
		}
		defer logFile.Close()
		done <- r.RunWithLog(ctx, logFile)
	}()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case e := <-done:
			if e != nil {
				return false, 1
			}
			return false, 0
		case change := <-requests:
			switch change.Cmd {
			case svc.Interrogate:
				status <- change.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		}
	}
}

func openServiceLog(c probe.Config) (*os.File, error) {
	dir := filepath.Dir(c.Spool)
	if dir == "." || dir == "" {
		dir = os.TempDir()
	}
	_ = os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, "probe-service.log")
	if info, err := os.Stat(path); err == nil && info.Size() > 1<<20 {
		_ = os.Remove(path)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
}

func runService(c probe.Config) (bool, error) {
	is, e := svc.IsWindowsService()
	if e != nil || !is {
		return false, e
	}
	return true, svc.Run(probeServiceName, &serviceHandler{config: c})
}
