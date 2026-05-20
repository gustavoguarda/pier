package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kardianos/service"

	"github.com/gustavoguarda/pier/internal/config"
	"github.com/gustavoguarda/pier/internal/filestore"
	"github.com/gustavoguarda/pier/internal/saas"
)

type program struct {
	cfg    *config.Config
	store  *filestore.Store
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *program) Start(_ service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})

	go func() {
		defer close(p.done)
		if err := saas.Run(ctx, p.cfg, p.store); err != nil {
			slog.Error("agent stopped", "error", err)
		}
	}()
	return nil
}

func (p *program) Stop(_ service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		<-p.done
	}
	return nil
}

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	svcAction := flag.String("service", "", "control the system service: install, uninstall, start, stop, restart")
	svcName := flag.String("service-name", "pier-agent", "system service name")
	svcDisplayName := flag.String("service-display-name", "Pier Agent", "system service display name")
	flag.Parse()

	// Resolve config path to absolute. When running as a Windows Service the
	// working directory is C:\Windows\System32, so a relative path like
	// "config.yaml" would fail to load. Resolving here means the same flag
	// works in interactive mode (relative paths are convenient) and in
	// service-installed mode (Arguments[] below gets the absolute form).
	absCfgPath, err := filepath.Abs(*cfgPath)
	if err != nil {
		slog.Error("failed to resolve config path", "error", err)
		os.Exit(1)
	}

	cfg, err := config.Load(absCfgPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	prog := &program{
		cfg:   cfg,
		store: filestore.New(cfg.BasePath),
	}

	svcConfig := &service.Config{
		Name:        *svcName,
		DisplayName: *svcDisplayName,
		Description: "Bridges the Lifleg SaaS portal and the local file share.",
		Arguments:   []string{"-config", absCfgPath},
	}

	s, err := service.New(prog, svcConfig)
	if err != nil {
		slog.Error("failed to build service", "error", err)
		os.Exit(1)
	}

	if *svcAction != "" {
		if err := service.Control(s, *svcAction); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("service action '%s' applied\n", *svcAction)
		return
	}

	if err := s.Run(); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}
