package paths

import (
	"os"
	"path/filepath"
)

const Label = "io.reekeer.agent"

func Home() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp"
	}
	return home
}

func Base() string {
	return filepath.Join(Home(), "Library", "Application Support", "reekeer-agent")
}

func Bin() string    { return filepath.Join(Base(), "reekeer-agent") }
func Config() string { return filepath.Join(Base(), "config.json") }
func Logs() string   { return filepath.Join(Base(), "logs") }
func Cache() string  { return filepath.Join(Base(), "cache") }
func Tools() string  { return filepath.Join(Base(), "tools") }
func TartHome() string {
	if v := os.Getenv("TART_HOME"); v != "" {
		return v
	}
	return filepath.Join(Home(), ".tart")
}
func Plist() string {
	return filepath.Join(Home(), "Library", "LaunchAgents", Label+".plist")
}
