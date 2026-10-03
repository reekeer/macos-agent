package tart

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type Tart struct {
	Bin string
}

type VM struct {
	Name   string
	Source string
	State  string
	SizeGb float64
}

var percent = regexp.MustCompile(`(\d{1,3}(?:\.\d+)?)%`)

func Find() (*Tart, error) {
	for _, p := range []string{"/usr/local/bin/tart", "/opt/homebrew/bin/tart", "/Applications/tart.app/Contents/MacOS/tart"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return &Tart{Bin: p}, nil
		}
	}
	if p, err := exec.LookPath("tart"); err == nil {
		return &Tart{Bin: p}, nil
	}
	return nil, errors.New("tart is not installed")
}

func (t *Tart) Cmd(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, t.Bin, args...)
}

func (t *Tart) out(ctx context.Context, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := t.Cmd(ctx, args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	text := strings.TrimSpace(buf.String())
	if err != nil {
		if text == "" {
			return "", fmt.Errorf("tart %s: %w", args[0], err)
		}
		return text, fmt.Errorf("tart %s: %s", args[0], lastLine(text))
	}
	return text, nil
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func (t *Tart) Version(ctx context.Context) string {
	v, err := t.out(ctx, "--version")
	if err != nil {
		return ""
	}
	return v
}

func ParseList(raw []byte) ([]VM, error) {
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	out := make([]VM, 0, len(items))
	for _, it := range items {
		vm := VM{}
		vm.Name, _ = it["Name"].(string)
		vm.Source, _ = it["Source"].(string)
		vm.State, _ = it["State"].(string)
		if size, ok := it["Size"].(float64); ok {
			vm.SizeGb = size
		}
		if vm.State == "" {
			if running, ok := it["Running"].(bool); ok && running {
				vm.State = "running"
			}
		}
		out = append(out, vm)
	}
	return out, nil
}

func (t *Tart) List(ctx context.Context) ([]VM, error) {
	raw, err := t.out(ctx, "list", "--format", "json")
	if err != nil {
		return nil, err
	}
	return ParseList([]byte(raw))
}

func (t *Tart) Has(ctx context.Context, ref string) (bool, error) {
	vms, err := t.List(ctx)
	if err != nil {
		return false, err
	}
	for _, vm := range vms {
		if vm.Source == "OCI" && vm.Name == ref {
			return true, nil
		}
	}
	return false, nil
}

func (t *Tart) Clone(ctx context.Context, src, dst string) error {
	_, err := t.out(ctx, "clone", src, dst)
	return err
}

func (t *Tart) Set(ctx context.Context, name string, cpu, memoryMB int) error {
	_, err := t.out(ctx, "set", name, "--cpu", strconv.Itoa(cpu), "--memory", strconv.Itoa(memoryMB))
	return err
}

func (t *Tart) Run(name string, dirs []string) (*exec.Cmd, error) {
	args := []string{"run", name, "--no-graphics", "--net-softnet"}
	for _, d := range dirs {
		args = append(args, "--dir="+d)
	}
	cmd := exec.Command(t.Bin, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd, cmd.Start()
}

func (t *Tart) Exec(ctx context.Context, name string, args ...string) (string, error) {
	return t.out(ctx, append([]string{"exec", name}, args...)...)
}

func (t *Tart) ExecCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	return t.Cmd(ctx, append([]string{"exec", name}, args...)...)
}

func (t *Tart) Stop(ctx context.Context, name string) error {
	_, err := t.out(ctx, "stop", name)
	return err
}

func (t *Tart) Delete(ctx context.Context, name string) error {
	_, err := t.out(ctx, "delete", name)
	return err
}

func (t *Tart) Prune(ctx context.Context, args ...string) error {
	_, err := t.out(ctx, append([]string{"prune"}, args...)...)
	return err
}

func Progress(line string) string {
	m := percent.FindAllStringSubmatch(line, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1] + "%"
}

func splitProgress(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func (t *Tart) Pull(ctx context.Context, ref string, progress func(string)) error {
	cmd := t.Cmd(ctx, "pull", ref)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	scan := bufio.NewScanner(pipe)
	scan.Split(splitProgress)
	last := ""
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		last = line
		if p := Progress(line); p != "" {
			progress(p)
		}
	}
	if err := cmd.Wait(); err != nil {
		if last != "" {
			return fmt.Errorf("tart pull: %s", last)
		}
		return fmt.Errorf("tart pull: %w", err)
	}
	return nil
}
