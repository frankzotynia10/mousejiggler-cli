package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// config is the single source of truth shared by the CLI, the GUI and the
// scheduled task. It is persisted to config.json by install.
type config struct {
	days     string
	windows  []string
	interval time.Duration
	sched    Schedule
}

type savedConfig struct {
	Days     string   `json:"days"`
	Windows  []string `json:"windows"`
	Interval string   `json:"interval"`
}

func defaultConfig() *config {
	c, _ := newConfig("Mon-Fri", []string{"08:05-12:05", "13:30-17:30"}, time.Minute)
	return c
}

// newConfig validates and normalizes a schedule.
func newConfig(days string, windows []string, interval time.Duration) (*config, error) {
	if interval < 5*time.Second {
		return nil, errors.New("interval must be at least 5s")
	}
	d, err := ParseDays(days)
	if err != nil {
		return nil, err
	}
	c := &config{days: DaysString(d), interval: interval}
	c.sched.Days = d
	for _, w := range windows {
		if strings.TrimSpace(w) == "" {
			continue
		}
		win, err := ParseWindow(w)
		if err != nil {
			return nil, err
		}
		c.sched.Windows = append(c.sched.Windows, win)
		c.windows = append(c.windows, win.String())
	}
	if len(c.windows) == 0 {
		return nil, errors.New("at least one time window is required")
	}
	return c, nil
}

// appDir holds config and log. Override with MOUSEJIGGLER_HOME.
func appDir() string {
	if d := os.Getenv("MOUSEJIGGLER_HOME"); d != "" {
		return d
	}
	dir, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, appName)
}

func configPath() string { return filepath.Join(appDir(), "config.json") }
func logPath() string    { return filepath.Join(appDir(), "jiggler.log") }

// loadConfig returns the saved config, or defaults if none has been saved.
func loadConfig() (*config, error) {
	b, err := os.ReadFile(configPath())
	if errors.Is(err, os.ErrNotExist) {
		return defaultConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	var s savedConfig
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", configPath(), err)
	}
	iv, err := time.ParseDuration(s.Interval)
	if err != nil {
		return nil, fmt.Errorf("%s: interval: %w", configPath(), err)
	}
	c, err := newConfig(s.Days, s.Windows, iv)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", configPath(), err)
	}
	return c, nil
}

func (c *config) save() error {
	if err := os.MkdirAll(appDir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(savedConfig{c.days, c.windows, c.interval.String()}, "", "  ")
	tmp := configPath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, configPath())
}
