package slot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/reekeer/macos-agent/internal/panel"
	"github.com/reekeer/macos-agent/internal/tart"
)

const (
	Prefix      = "rk-"
	bootTimeout = 4 * time.Minute
	jobLimit    = 6 * time.Hour
	tick        = 10 * time.Second
	idleWait    = 5 * time.Second
)

const prepare = `set -e
rm -rf "$HOME/actions-runner" && mkdir -p "$HOME/actions-runner"
tar xzf "/Volumes/My Shared Files/tools/actions-runner.tar.gz" -C "$HOME/actions-runner"
ln -sfn "/Volumes/My Shared Files/cache" "$HOME/.rk-cache"
C="$HOME/.rk-cache"
mkdir -p "$C/homebrew" "$C/npm" "$C/pip" "$C/yarn" "$C/pnpm" "$C/gradle" "$C/cocoapods"
cat > "$HOME/actions-runner/.env" <<EOF
HOMEBREW_CACHE=$C/homebrew
npm_config_cache=$C/npm
PIP_CACHE_DIR=$C/pip
YARN_CACHE_FOLDER=$C/yarn
PNPM_STORE_DIR=$C/pnpm
GRADLE_USER_HOME=$C/gradle
CP_HOME_DIR=$C/cocoapods
EOF`

type Env struct {
	Tart  *tart.Tart
	Panel *panel.Client
	Log   *slog.Logger
	Cache string
	Tools string
	Ready func() (image string, reason string)
}

type Slot struct {
	env    *Env
	pool   string
	wake   chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	plan   panel.SlotPlan
	state  string
	vm     string
	runner string
	since  time.Time
	err    string
}

func New(env *Env, plan panel.SlotPlan) *Slot {
	return &Slot{env: env, pool: plan.Pool, plan: plan, wake: make(chan struct{}, 1), done: make(chan struct{})}
}

func (s *Slot) Pool() string          { return s.pool }
func (s *Slot) Done() <-chan struct{} { return s.done }

func (s *Slot) Update(p panel.SlotPlan) {
	s.mu.Lock()
	s.plan = p
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Slot) Plan() panel.SlotPlan {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.plan
}

func (s *Slot) VM() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vm
}

func (s *Slot) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == "busy"
}

func (s *Slot) Report() (panel.SlotReport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == "" {
		return panel.SlotReport{}, false
	}
	since := s.since
	return panel.SlotReport{Pool: s.pool, VM: s.vm, State: s.state, Runner: s.runner, Since: &since, Error: s.err}, true
}

func (s *Slot) set(state, vm, runner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != state {
		s.since = time.Now()
	}
	s.state, s.vm, s.runner, s.err = state, vm, runner, ""
}

func (s *Slot) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state, s.vm, s.runner, s.err, s.since = "failed", "", "", err.Error(), time.Now()
}

func (s *Slot) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == "failed" {
		s.state, s.err = "", ""
	}
}

func (s *Slot) idle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != "failed" {
		s.state, s.vm, s.runner, s.err = "", "", "", ""
	}
}

func (s *Slot) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-s.wake:
	case <-time.After(d):
	}
}

func (s *Slot) Run(ctx context.Context) {
	defer close(s.done)
	failures := 0
	for ctx.Err() == nil {
		p := s.Plan()
		if !p.Keep {
			return
		}
		if !p.Start {
			s.clear()
			s.sleep(ctx, idleWait)
			continue
		}
		image, reason := s.env.Ready()
		if reason != "" {
			s.fail(errors.New(reason))
			s.sleep(ctx, 30*time.Second)
			continue
		}
		if err := s.cycle(ctx, image); err != nil && ctx.Err() == nil {
			failures++
			s.env.Log.Warn("slot failed", "pool", s.pool, "err", err)
			s.fail(err)
			s.sleep(ctx, min(time.Duration(failures)*30*time.Second, 5*time.Minute))
			continue
		}
		failures = 0
	}
}

func (s *Slot) cycle(ctx context.Context, image string) error {
	t := s.env.Tart
	p := s.Plan()
	vm := fmt.Sprintf("%s%s-%d", Prefix, s.pool, time.Now().Unix())
	s.set("cloning", vm, "")
	defer s.teardown(vm)
	if err := t.Clone(ctx, image, vm); err != nil {
		return err
	}
	if err := t.Set(ctx, vm, p.CPU, p.MemoryGb*1024); err != nil {
		return err
	}
	cache := filepath.Join(s.env.Cache, s.pool)
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return err
	}
	run, err := t.Run(vm, []string{"cache:" + cache, "tools:" + s.env.Tools + ":ro"})
	if err != nil {
		return err
	}
	vmDone := make(chan error, 1)
	go func() { vmDone <- run.Wait() }()
	s.set("booting", vm, "")
	if err := s.waitGuest(ctx, vm, vmDone); err != nil {
		return err
	}
	if _, err := t.Exec(ctx, vm, "bash", "-c", prepare); err != nil {
		return err
	}
	jit, err := s.env.Panel.JIT(ctx, s.pool, vm)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runner := t.ExecCmd(runCtx, vm, "bash", "-lc", "cd \"$HOME/actions-runner\" && exec ./run.sh --jitconfig '"+jit.Config+"'")
	if err := runner.Start(); err != nil {
		s.release(jit.Name)
		return err
	}
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- runner.Wait() }()
	s.set("ready", vm, jit.Name)
	s.env.Log.Info("runner online", "pool", s.pool, "vm", vm, "runner", jit.Name)
	return s.watch(ctx, cancel, vm, jit.Name, vmDone, runnerDone)
}

func (s *Slot) watch(ctx context.Context, cancel context.CancelFunc, vm, name string, vmDone, runnerDone <-chan error) error {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	idleSince := time.Now()
	var busyAt time.Time
	for {
		select {
		case <-ctx.Done():
			if busyAt.IsZero() {
				s.release(name)
			}
			return ctx.Err()
		case err := <-vmDone:
			return fmt.Errorf("vm stopped: %v", err)
		case <-runnerDone:
			if busyAt.IsZero() {
				s.release(name)
			} else {
				s.env.Log.Info("job finished", "pool", s.pool, "runner", name, "took", time.Since(busyAt).Round(time.Second))
			}
			return nil
		case <-s.wake:
		case <-ticker.C:
		}
		if s.working(ctx, vm) {
			if busyAt.IsZero() {
				busyAt = time.Now()
				s.set("busy", vm, name)
			}
			if time.Since(busyAt) > jobLimit {
				cancel()
				return errors.New("job ran longer than 6h")
			}
			continue
		}
		if !busyAt.IsZero() {
			continue
		}
		p := s.Plan()
		idle := p.IdleMinutes > 0 && !p.Start && time.Since(idleSince) > time.Duration(p.IdleMinutes)*time.Minute
		if !p.Keep || idle {
			cancel()
			s.release(name)
			return nil
		}
	}
}

func (s *Slot) working(ctx context.Context, vm string) bool {
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return s.env.Tart.ExecCmd(c, vm, "pgrep", "-q", "-f", "Runner.Worker").Run() == nil
}

func (s *Slot) waitGuest(ctx context.Context, vm string, vmDone <-chan error) error {
	deadline := time.Now().Add(bootTimeout)
	for time.Now().Before(deadline) {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := s.env.Tart.ExecCmd(c, vm, "true").Run()
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-vmDone:
			return fmt.Errorf("vm exited while booting: %v", err)
		case <-time.After(3 * time.Second):
		}
	}
	return errors.New("vm didn't boot in 4 minutes")
}

func (s *Slot) release(name string) {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.env.Panel.Release(c, s.pool, name); err != nil {
		s.env.Log.Warn("release failed", "runner", name, "err", err)
	}
}

func (s *Slot) teardown(vm string) {
	s.mu.Lock()
	if s.state != "failed" {
		s.state, s.since = "stopping", time.Now()
	}
	s.mu.Unlock()
	c, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s.env.Tart.Stop(c, vm)
	if err := s.env.Tart.Delete(c, vm); err != nil {
		s.env.Log.Warn("delete failed", "vm", vm, "err", err)
	}
	s.idle()
}
