package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/reekeer/macos-agent/internal/cache"
	"github.com/reekeer/macos-agent/internal/config"
	"github.com/reekeer/macos-agent/internal/host"
	"github.com/reekeer/macos-agent/internal/panel"
	"github.com/reekeer/macos-agent/internal/paths"
	"github.com/reekeer/macos-agent/internal/runnerpkg"
	"github.com/reekeer/macos-agent/internal/slot"
	"github.com/reekeer/macos-agent/internal/tart"
	"github.com/reekeer/macos-agent/internal/update"
)

const (
	syncEvery    = 5 * time.Second
	houseEvery   = 10 * time.Minute
	runnerEvery  = 24 * time.Hour
	updateEvery  = 6 * time.Hour
	pruneOlder   = "7"
	gib          = int64(1) << 30
	missingTools = "GitHub runner isn't downloaded yet"
)

type Agent struct {
	cfg     config.Config
	version string
	log     *slog.Logger
	tart    *tart.Tart
	panel   *panel.Client
	tartVer string

	mu       sync.Mutex
	slots    map[string]*slot.Slot
	plan     panel.Plan
	havePlan bool
	image    panel.ImageState
	pulling  bool
	freeGb   float64
	errText  string
	offline  bool
	restart  bool
	pullFail time.Time
}

func New(cfg config.Config, version string, log *slog.Logger) *Agent {
	return &Agent{
		cfg:     cfg,
		version: version,
		log:     log,
		panel:   panel.New(cfg.Panel, cfg.Token, version),
		slots:   map[string]*slot.Slot{},
	}
}

func (a *Agent) Restart() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.restart
}

func (a *Agent) Run(ctx context.Context) error {
	for {
		t, err := tart.Find()
		if err == nil {
			a.tart = t
			break
		}
		a.setErr(err.Error())
		a.reportOnly(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
		}
	}
	a.tartVer = a.tart.Version(ctx)
	a.setErr("")
	stopAwake := keepAwake()
	defer stopAwake()
	Cleanup(ctx, a.tart, a.log)
	slotsCtx, cancelSlots := context.WithCancel(context.Background())
	defer func() {
		cancelSlots()
		a.waitSlots()
	}()
	go a.housekeeping(ctx)
	ticker := time.NewTicker(syncEvery)
	defer ticker.Stop()
	for {
		a.sync(ctx, slotsCtx)
		if a.Restart() && !a.busy() {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func keepAwake() func() {
	cmd := exec.Command("caffeinate", "-i", "-m", "-s")
	if err := cmd.Start(); err != nil {
		return func() {}
	}
	return func() {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

func (a *Agent) setErr(text string) {
	a.mu.Lock()
	a.errText = text
	a.mu.Unlock()
}

func (a *Agent) reportOnly(ctx context.Context) {
	a.mu.Lock()
	r := panel.Report{Version: a.version, Error: a.errText, Slots: []panel.SlotReport{}}
	a.mu.Unlock()
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := a.panel.Sync(c, r); err != nil {
		a.log.Warn("sync failed", "err", err)
	}
}

func (a *Agent) report(ctx context.Context) panel.Report {
	stats := host.Stats(ctx, paths.TartHome(), a.tartVer)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.freeGb = stats.DiskFreeGb
	r := panel.Report{Version: a.version, Host: stats, Image: a.image, Error: a.errText, Slots: []panel.SlotReport{}}
	for _, s := range a.slots {
		if rep, ok := s.Report(); ok {
			r.Slots = append(r.Slots, rep)
		}
	}
	return r
}

func (a *Agent) sync(ctx, slotsCtx context.Context) {
	r := a.report(ctx)
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	plan, err := a.panel.Sync(c, r)
	cancel()
	if err != nil {
		a.mu.Lock()
		first := !a.offline
		a.offline = true
		for _, s := range a.slots {
			p := s.Plan()
			p.Start = false
			s.Update(p)
		}
		a.mu.Unlock()
		if first {
			a.log.Warn("panel unreachable", "err", err)
		}
		return
	}
	a.mu.Lock()
	if a.offline {
		a.log.Info("panel reachable again")
	}
	a.offline = false
	a.plan, a.havePlan = plan, true
	a.mu.Unlock()
	a.ensureImage(ctx, plan.Image)
	a.apply(slotsCtx, plan)
}

func (a *Agent) apply(ctx context.Context, plan panel.Plan) {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := map[string]bool{}
	for _, sp := range plan.Slots {
		seen[sp.Pool] = true
		if s, ok := a.slots[sp.Pool]; ok {
			s.Update(sp)
			continue
		}
		if !sp.Keep {
			continue
		}
		s := slot.New(a.env(), sp)
		a.slots[sp.Pool] = s
		go s.Run(ctx)
		go func() {
			<-s.Done()
			a.mu.Lock()
			if a.slots[s.Pool()] == s {
				delete(a.slots, s.Pool())
			}
			a.mu.Unlock()
		}()
	}
	for name, s := range a.slots {
		if !seen[name] {
			s.Update(panel.SlotPlan{Pool: name})
		}
	}
}

func (a *Agent) env() *slot.Env {
	return &slot.Env{
		Tart:  a.tart,
		Panel: a.panel,
		Log:   a.log,
		Cache: paths.Cache(),
		Tools: paths.Tools(),
		Ready: a.ready,
	}
}

func (a *Agent) ready() (string, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case a.offline:
		return "", "Panel is unreachable"
	case !a.image.Present:
		if a.image.Pulling {
			return "", "Image is downloading"
		}
		return "", "Image is missing"
	case a.havePlan && a.freeGb < float64(a.plan.MinFreeGb):
		return "", "Low disk space"
	case runnerpkg.Current(paths.Tools()) == "":
		return "", missingTools
	}
	return a.image.Ref, ""
}

func (a *Agent) ensureImage(ctx context.Context, ref string) {
	a.mu.Lock()
	if ref == "" || (a.image.Ref == ref && (a.image.Present || a.pulling || time.Since(a.pullFail) < 5*time.Minute)) {
		a.mu.Unlock()
		return
	}
	a.image = panel.ImageState{Ref: ref}
	a.mu.Unlock()
	has, err := a.tart.Has(ctx, ref)
	if err != nil {
		a.mu.Lock()
		a.image.Error = err.Error()
		a.mu.Unlock()
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if has {
		a.image.Present = true
		return
	}
	if a.pulling {
		return
	}
	a.pulling, a.image.Pulling = true, true
	go a.pull(ctx, ref)
}

func (a *Agent) pull(ctx context.Context, ref string) {
	a.log.Info("pulling image", "ref", ref)
	err := a.tart.Pull(ctx, ref, func(p string) {
		a.mu.Lock()
		if a.image.Ref == ref {
			a.image.Progress = p
		}
		a.mu.Unlock()
	})
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pulling = false
	if a.image.Ref != ref {
		return
	}
	a.image.Pulling, a.image.Progress = false, ""
	if err != nil {
		a.image.Error = err.Error()
		a.pullFail = time.Now()
		a.log.Warn("pull failed", "ref", ref, "err", err)
		return
	}
	a.image.Present, a.image.Error = true, ""
	a.log.Info("image ready", "ref", ref)
}

func (a *Agent) waitSlots() {
	a.mu.Lock()
	slots := make([]*slot.Slot, 0, len(a.slots))
	for _, s := range a.slots {
		slots = append(slots, s)
	}
	a.mu.Unlock()
	for _, s := range slots {
		<-s.Done()
	}
}

func (a *Agent) busy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.slots {
		if s.Busy() {
			return true
		}
	}
	return false
}

func (a *Agent) activeVMs() map[string]bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]bool{}
	for _, s := range a.slots {
		if vm := s.VM(); vm != "" {
			out[vm] = true
		}
	}
	return out
}

func (a *Agent) housekeeping(ctx context.Context) {
	var lastRunner, lastUpdate time.Time
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if time.Since(lastRunner) > runnerEvery || runnerpkg.Current(paths.Tools()) == "" {
			if v, err := runnerpkg.Ensure(ctx, paths.Tools()); err != nil {
				a.log.Warn("runner download failed", "err", err)
			} else {
				lastRunner = time.Now()
				a.log.Info("github runner ready", "version", v)
			}
		}
		a.clean(ctx)
		if a.version != "dev" && time.Since(lastUpdate) > updateEvery && !a.busy() {
			lastUpdate = time.Now()
			a.selfUpdate(ctx)
		}
		timer.Reset(houseEvery)
	}
}

func (a *Agent) clean(ctx context.Context) {
	active := a.activeVMs()
	if vms, err := a.tart.List(ctx); err == nil {
		for _, vm := range vms {
			if vm.Source == "local" && strings.HasPrefix(vm.Name, slot.Prefix) && !active[vm.Name] {
				a.log.Info("removing orphan vm", "vm", vm.Name)
				a.tart.Stop(ctx, vm.Name)
				a.tart.Delete(ctx, vm.Name)
			}
		}
	}
	if err := a.tart.Prune(ctx, "--entries=caches", "--older-than="+pruneOlder); err != nil {
		a.log.Warn("prune failed", "err", err)
	}
	a.mu.Lock()
	plan, have, free, ref := a.plan, a.havePlan, a.freeGb, a.image.Ref
	a.mu.Unlock()
	if !have {
		return
	}
	if has, err := a.tart.Has(ctx, ref); err == nil && !has {
		a.mu.Lock()
		if a.image.Ref == ref && !a.pulling {
			a.image.Present = false
		}
		a.mu.Unlock()
	}
	budget := int64(plan.CacheGb) * gib
	if free < float64(plan.MinFreeGb) {
		a.tart.Prune(ctx, "--entries=caches", "--older-than=1")
		budget /= 2
	}
	pools := map[string]bool{}
	for _, sp := range plan.Slots {
		pools[sp.Pool] = true
	}
	entries, _ := os.ReadDir(paths.Cache())
	for _, e := range entries {
		dir := filepath.Join(paths.Cache(), e.Name())
		if !e.IsDir() {
			continue
		}
		if !pools[e.Name()] {
			a.log.Info("removing cache of a removed pool", "pool", e.Name())
			os.RemoveAll(dir)
			continue
		}
		if freed, err := cache.Trim(dir, budget); err == nil && freed > 0 {
			a.log.Info("cache trimmed", "pool", e.Name(), "freed_mb", strconv.FormatInt(freed>>20, 10))
		}
	}
}

func (a *Agent) selfUpdate(ctx context.Context) {
	tag, err := update.Latest(ctx)
	if err != nil || tag == "" || tag == a.version {
		return
	}
	if err := update.Apply(ctx, tag, paths.Bin()); err != nil {
		a.log.Warn("update failed", "tag", tag, "err", err)
		return
	}
	a.log.Info("updated, restarting", "from", a.version, "to", tag)
	a.mu.Lock()
	a.restart = true
	a.mu.Unlock()
}

func Cleanup(ctx context.Context, t *tart.Tart, log *slog.Logger) {
	vms, err := t.List(ctx)
	if err != nil {
		log.Warn("list failed", "err", err)
		return
	}
	for _, vm := range vms {
		if vm.Source == "local" && strings.HasPrefix(vm.Name, slot.Prefix) {
			log.Info("removing leftover vm", "vm", vm.Name)
			t.Stop(ctx, vm.Name)
			if err := t.Delete(ctx, vm.Name); err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("delete failed", "vm", vm.Name, "err", err)
			}
		}
	}
}
