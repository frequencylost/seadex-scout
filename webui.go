package main

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cplieger/seadex-scout/internal/config"
	"github.com/cplieger/seadex-scout/internal/webui"
)

// The built-in dashboard is configured from the environment rather than from
// config.yaml, so the upstream config schema (which rejects unknown keys) stays
// unchanged.
const (
	envWebUIPort     = "SEADEX_SCOUT_WEBUI_PORT"
	envWebUIBind     = "SEADEX_SCOUT_WEBUI_BIND"
	envWebUIUsername = "SEADEX_SCOUT_WEBUI_USERNAME"
	envWebUIPassword = "SEADEX_SCOUT_WEBUI_PASSWORD"

	defaultWebUIPort = "9119"
	webUIStateFile   = "webui-state.json"
	webUISaveEvery   = time.Minute
	webUIStopTimeout = 5 * time.Second
)

// activeWebUI is set by main in daemon mode when the dashboard is on; run
// starts it. Every other mode leaves it nil.
var activeWebUI *webUIRuntime

type webUIRuntime struct {
	store *webui.Store
	addr  string
}

// installWebUI wraps the default logger so the dashboard sees every log line
// from here on, and loads the dashboard's saved state from beside the config
// file. It returns nil when SEADEX_SCOUT_WEBUI_PORT turns the dashboard off.
func installWebUI(configPath string) *webUIRuntime {
	port := strings.TrimSpace(cmp.Or(os.Getenv(envWebUIPort), defaultWebUIPort))
	switch strings.ToLower(port) {
	case "off", "0", "false", "disabled":
		slog.Info("dashboard disabled", "env", envWebUIPort)
		return nil
	}
	addr := net.JoinHostPort(strings.TrimSpace(os.Getenv(envWebUIBind)), port)

	store := webui.NewStore(filepath.Join(filepath.Dir(configPath), webUIStateFile))
	loadErr := store.Load()
	slog.SetDefault(slog.New(webui.NewHandler(slog.Default().Handler(), store)))
	if loadErr != nil {
		slog.Warn("dashboard could not read its saved state; starting empty", "error", loadErr)
	}
	return &webUIRuntime{store: store, addr: addr}
}

// start serves the dashboard and saves its state every minute until ctx ends.
// The returned func stops the server and makes a last save. A port that cannot
// be bound is logged and the scout keeps running without its dashboard.
func (w *webUIRuntime) start(ctx context.Context, cfg *config.Config) func() {
	log := slog.Default()
	var runner *webui.ReportRunner
	if exe, err := os.Executable(); err == nil {
		runner = webui.NewReportRunner(ctx, exe, w.store)
	} else {
		log.Warn("dashboard cannot start reports: the executable path is unknown", "error", err)
	}
	var sonarr, radarr webui.Arr
	if cfg.SonarrEnabled() {
		sonarr = webui.Arr{URL: cfg.SonarrURL, APIKey: cfg.SonarrAPIKey}
	}
	if cfg.RadarrEnabled() {
		radarr = webui.Arr{URL: cfg.RadarrURL, APIKey: cfg.RadarrAPIKey}
	}
	polled := make(chan struct{})
	go func() {
		defer close(polled)
		webui.NewMonitoredPoller(sonarr, radarr, w.store, log).Run(ctx)
	}()

	srv := webui.NewServer(webui.Options{
		Addr:      w.addr,
		Username:  cmp.Or(os.Getenv(envWebUIUsername), "admin"),
		Password:  os.Getenv(envWebUIPassword),
		ReportDir: cfg.ReportDir,
		Store:     w.store,
		Runner:    runner,
		Log:       log,
	})

	served := make(chan struct{})
	go func() {
		defer close(served)
		ln, err := net.Listen("tcp", w.addr)
		if err != nil {
			log.Error("dashboard could not listen; the scout keeps running without it",
				"addr", w.addr, "error", err)
			return
		}
		log.Info("dashboard listening", "addr", ln.Addr().String(),
			"auth", os.Getenv(envWebUIPassword) != "")
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("dashboard stopped", "error", err)
		}
	}()

	quit, saved := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(saved)
		t := time.NewTicker(webUISaveEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-quit:
				return
			case <-t.C:
				if err := w.store.Save(); err != nil {
					log.Warn("dashboard could not save its state", "error", err)
				}
			}
		}
	}()

	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), webUIStopTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		close(quit)
		<-served
		<-saved
		<-polled
		if err := w.store.Save(); err != nil {
			log.Warn("dashboard could not save its state", "error", err)
		}
	}
}
