// mousejiggler-cli: a headless, scheduled "zen" mouse jiggler for Windows.
//
// It sends zero-distance mouse-move events (the cursor never moves) and holds
// a display-required power request during scheduled windows, so monitors wake
// up and stay on without interfering with normal use.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	appName  = "mousejiggler-cli"
	taskName = "MouseJigglerCLI"
)

type config struct {
	days     string
	windows  multiFlag
	interval time.Duration
	sched    Schedule
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func usage() {
	fmt.Fprintf(os.Stderr, `%s - headless scheduled mouse jiggler (cursor never moves)

Usage:
  jiggler <command> [flags]

Commands:
  install     Copy jiggler to %%LOCALAPPDATA%%\Programs\%s, register a
              Task Scheduler task (logon + window starts, wakes PC) and start it
  uninstall   Stop and remove the scheduled task
  run         Run the scheduler loop in the foreground (what the task runs)
  once        Send a single zen jiggle now and exit
  status      Show schedule, whether it's active now, and the next window

Flags (install / run / status):
  --days      Days of week, e.g. Mon-Fri, Mon,Wed,Fri, all   (default Mon-Fri)
  --window    Daily window HH:MM-HH:MM (24h), repeatable
              (default 08:05-12:05 and 13:30-17:30)
  --interval  Time between jiggles inside a window             (default 1m)

Log: %s
`, appName, appName, logPath())
}

func parseConfig(args []string) (*config, error) {
	cfg := &config{}
	fs := flag.NewFlagSet("jiggler", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.days, "days", "Mon-Fri", "")
	fs.Var(&cfg.windows, "window", "")
	fs.DurationVar(&cfg.interval, "interval", time.Minute, "")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if len(cfg.windows) == 0 {
		cfg.windows = multiFlag{"08:05-12:05", "13:30-17:30"}
	}
	if cfg.interval < 5*time.Second {
		return nil, errors.New("--interval must be at least 5s")
	}
	days, err := ParseDays(cfg.days)
	if err != nil {
		return nil, err
	}
	cfg.sched.Days = days
	for _, w := range cfg.windows {
		win, err := ParseWindow(w)
		if err != nil {
			return nil, err
		}
		cfg.sched.Windows = append(cfg.sched.Windows, win)
	}
	return cfg, nil
}

// taskArgs reproduces the config as command-line args for the scheduled task.
func (c *config) taskArgs() string {
	parts := []string{"run", "--days", strings.ReplaceAll(c.days, " ", "")}
	for _, w := range c.sched.Windows {
		parts = append(parts, "--window", w.String())
	}
	parts = append(parts, "--interval", c.interval.String())
	return strings.Join(parts, " ")
}

func appDir() string {
	dir, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, appName)
}

func logPath() string { return filepath.Join(appDir(), "jiggler.log") }

// openLog logs to file (and stdout when attached to a console). The file is
// truncated once it passes 1 MB so it never needs cleanup.
func openLog() *log.Logger {
	_ = os.MkdirAll(appDir(), 0o755)
	p := logPath()
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(p); err == nil && fi.Size() > 1<<20 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	var w io.Writer = os.Stdout
	if f, err := os.OpenFile(p, flags, 0o644); err == nil {
		w = io.MultiWriter(f, os.Stdout)
	}
	return log.New(w, "", log.LstdFlags)
}

func main() {
	attachParentConsole()
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := strings.ToLower(os.Args[1])
	if cmd == "help" || cmd == "-h" || cmd == "--help" || cmd == "/?" {
		usage()
		return
	}
	cfg, err := parseConfig(os.Args[2:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	switch cmd {
	case "run":
		err = runLoop(cfg, openLog())
	case "once":
		err = jiggle()
		if err == nil {
			fmt.Println("jiggled")
		}
	case "status":
		printStatus(cfg)
	case "install":
		err = install(cfg)
	case "uninstall":
		err = uninstall()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func printStatus(cfg *config) {
	now := time.Now()
	fmt.Printf("days:     %s\n", cfg.days)
	for _, w := range cfg.sched.Windows {
		fmt.Printf("window:   %s\n", w)
	}
	fmt.Printf("interval: %s\n", cfg.interval)
	fmt.Printf("active:   %v\n", cfg.sched.Active(now))
	if s, e, ok := cfg.sched.Next(now); ok {
		fmt.Printf("next:     %s -> %s\n", s.Format("Mon Jan 2 15:04"), e.Format("15:04"))
	}
	fmt.Printf("log:      %s\n", logPath())
	taskStatus()
}

// runLoop checks the schedule every few seconds. Inside a window it holds the
// display awake and jiggles every interval (immediately on window entry).
// Outside a window it releases the power request and does nothing.
func runLoop(cfg *config, lg *log.Logger) error {
	release, err := singleInstance()
	if err != nil {
		lg.Printf("another instance is already running; exiting")
		return nil
	}
	defer release()

	lg.Printf("started: days=%s windows=%v interval=%s", cfg.days, cfg.windows, cfg.interval)
	keeper := newAwakeKeeper() // pins an OS thread for the power request
	defer keeper.Close()

	active := false
	var last time.Time
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		now := time.Now()
		in := cfg.sched.Active(now)
		if in != active {
			active = in
			keeper.Set(in)
			if in {
				lg.Printf("window entered: keeping display awake")
				last = time.Time{}
			} else {
				lg.Printf("window ended: display may sleep normally")
			}
		}
		// The time gap check also catches resume-from-sleep mid-window.
		if active && now.Sub(last) >= cfg.interval {
			if err := jiggle(); err != nil {
				lg.Printf("jiggle failed (session locked?): %v", err)
			}
			last = now
		}
		<-tick.C
	}
}
