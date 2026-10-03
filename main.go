package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/reekeer/macos-agent/internal/agent"
	"github.com/reekeer/macos-agent/internal/config"
	"github.com/reekeer/macos-agent/internal/logx"
	"github.com/reekeer/macos-agent/internal/paths"
	"github.com/reekeer/macos-agent/internal/tart"
)

var version = "dev"

const plist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string><string>run</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>ProcessType</key><string>Interactive</string>
  <key>EnvironmentVariables</key><dict><key>PATH</key><string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string></dict>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`

func main() {
	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "run":
		err = run()
	case "install":
		err = install(os.Args[2:])
	case "uninstall":
		err = uninstall()
	case "version":
		fmt.Println(version)
	default:
		err = fmt.Errorf("usage: reekeer-agent [run|install|uninstall|version]")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	out, err := logx.Open(filepath.Join(paths.Logs(), "agent.log"), 10<<20, 3)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(out, nil))
	log.Info("agent starting", "version", version, "panel", cfg.Panel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	a := agent.New(cfg, version, log)
	err = a.Run(ctx)
	log.Info("agent stopped", "restart", a.Restart())
	return err
}

func gui() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

func launchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return nil
}

func install(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	panelURL := fs.String("panel", "", "panel URL")
	token := fs.String("token", "", "host token from the panel")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := tart.Find(); err != nil {
		return err
	}
	if err := config.Save(config.Config{Panel: *panelURL, Token: *token}); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}
	if self != paths.Bin() {
		raw, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		if err := os.WriteFile(paths.Bin()+".new", raw, 0o755); err != nil {
			return err
		}
		if err := os.Rename(paths.Bin()+".new", paths.Bin()); err != nil {
			return err
		}
	}
	for _, d := range []string{paths.Logs(), paths.Cache(), paths.Tools(), filepath.Dir(paths.Plist())} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	launchd := filepath.Join(paths.Logs(), "launchd.log")
	body := fmt.Sprintf(plist, paths.Label, paths.Bin(), launchd, launchd)
	if err := os.WriteFile(paths.Plist(), []byte(body), 0o644); err != nil {
		return err
	}
	launchctl("bootout", gui()+"/"+paths.Label)
	time.Sleep(time.Second)
	if err := launchctl("bootstrap", gui(), paths.Plist()); err != nil {
		return err
	}
	fmt.Println("reekeer-agent is running. Logs:", filepath.Join(paths.Logs(), "agent.log"))
	return nil
}

func uninstall() error {
	launchctl("bootout", gui()+"/"+paths.Label)
	if t, err := tart.Find(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		agent.Cleanup(ctx, t, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	}
	os.Remove(paths.Plist())
	if err := os.RemoveAll(paths.Base()); err != nil {
		return err
	}
	fmt.Println("reekeer-agent removed")
	return nil
}
