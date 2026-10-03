package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/reekeer/macos-agent/internal/paths"
)

type Config struct {
	Panel string `json:"panel"`
	Token string `json:"token"`
}

func Load() (Config, error) {
	var c Config
	raw, err := os.ReadFile(paths.Config())
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if !strings.HasPrefix(c.Panel, "https://") && !strings.HasPrefix(c.Panel, "http://") {
		return errors.New("panel must be an http(s) URL")
	}
	if !strings.HasPrefix(c.Token, "rka_") {
		return errors.New("token must start with rka_")
	}
	return nil
}

func Save(c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	c.Panel = strings.TrimRight(c.Panel, "/")
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.Config()), 0o700); err != nil {
		return err
	}
	return os.WriteFile(paths.Config(), raw, 0o600)
}
