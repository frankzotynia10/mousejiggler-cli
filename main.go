// mousejiggler-cli: a headless, scheduled "zen" mouse jiggler for Windows.
//
// It sends zero-distance mouse-move events (the cursor never moves) and holds
// a display-required power request during scheduled windows, so monitors wake
// up and stay on without interfering with normal use.
//
// Run with no arguments for a settings window; every setting is also
// available on the command line for automation.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

const (
	appName  = "mousejiggler-cli"
	taskName = "MouseJigglerCLI"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func usage() {
	fmt.Fprintf(os.Stderr, `%s - headless scheduled mouse jiggler (cursor never moves)

Usage:
  jiggler                      Open the settings window
  jiggler <command> [flags]

Commands:
  gui         Open the settings window
  install     Save settings, copy jiggler to %%LOCALAPPDATA%%\Programs\%s,
              register the scheduled task (logon + window starts, wakes PC)
              and (re)start it. Re-run any time to apply changes.
  uninstall   Stop and remove the scheduled task
  status      Show settings, whether a window is active, next window, task
              state. --json for machine-readable output, --short for one
              word: ACTIVE, IDLE (outside a window), PAUSED or OFF (no task).
  pause       Pause jiggling without touching the schedule. Default: rest
              of today. --for 3h | --days 2 | --until 13:30 |
              --until 2026-11-14 | --until "2026-11-14 08:00"
  resume      Cancel a pause
  toggle      Resume if paused, otherwise pause for the rest of today
              (one-button control, e.g. a Stream Deck key)
  once        Send a single zen jiggle now and exit
  run         Run the scheduler loop in the foreground (what the task runs)

Flags (install / run / status). Any flag left out keeps the saved value:
  --days      Days of week, e.g. Mon-Fri, Mon,Wed,Fri, all
  --window    Daily window HH:MM-HH:MM (24h). Repeat for several windows;
              giving any --window replaces all saved windows.
  --interval  Time between jiggles inside a window, e.g. 60s, 1m

Defaults: Mon-Fri, 08:05-12:05 and 13:30-17:30, every 1m
Config:   %s
Log:      %s
`, appName, appName, configPath(), logPath())
}

// parseConfig starts from the saved config and applies any flags given.
func parseConfig(args []string) (*config, error) {
	base, err := loadConfig()
	if err != nil {
		return nil, err
	}
	fs := flag.NewFlagSet("jiggler", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	days := fs.String("days", base.days, "")
	interval := fs.Duration("interval", base.interval, "")
	var windows multiFlag
	fs.Var(&windows, "window", "")
	jsonOut := fs.Bool("json", false, "")
	shortOut := fs.Bool("short", false, "")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if len(windows) == 0 {
		windows = base.windows
	}
	c, err := newConfig(*days, windows, *interval)
	if err != nil {
		return nil, err
	}
	jsonOutput = *jsonOut
	shortOutput = *shortOut
	return c, nil
}

var jsonOutput, shortOutput bool

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
	cmd := "gui"
	if len(os.Args) >= 2 {
		cmd = strings.ToLower(os.Args[1])
	}
	if cmd == "help" || cmd == "-h" || cmd == "--help" || cmd == "/?" {
		usage()
		return
	}
	if cmd == "gui" {
		if err := runGUI(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	var args []string
	if len(os.Args) > 2 {
		args = os.Args[2:]
	}
	switch cmd {
	case "pause":
		until, err := parsePauseUntil(args, time.Now())
		if err == nil {
			err = setPause(until)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Printf("paused until %s\n", until.Format("Mon Jan 2 15:04"))
		return
	case "resume":
		if err := clearPause(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println("resumed")
		return
	case "toggle":
		var err error
		if _, paused := pausedUntil(time.Now()); paused {
			if err = clearPause(); err == nil {
				fmt.Println("resumed")
			}
		} else {
			until := endOfDay(time.Now())
			if err = setPause(until); err == nil {
				fmt.Printf("paused until %s\n", until.Format("Mon Jan 2 15:04"))
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	cfg, err := parseConfig(args)
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
		if shortOutput {
			fmt.Println(shortState(cfg, time.Now(), taskStatus()))
		} else if jsonOutput {
			b, _ := json.MarshalIndent(statusOf(cfg, time.Now()), "", "  ")
			fmt.Println(string(b))
		} else {
			fmt.Print(statusText(cfg, time.Now()))
		}
	case "install":
		var msg string
		if msg, err = install(cfg); err == nil {
			fmt.Println(msg)
		}
	case "uninstall":
		var msg string
		if msg, err = uninstall(); err == nil {
			fmt.Println(msg)
		}
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type status struct {
	Days      string   `json:"days"`
	Windows   []string `json:"windows"`
	Interval  string   `json:"interval"`
	Active    bool     `json:"active"`
	Paused    string   `json:"paused_until,omitempty"`
	NextStart string   `json:"next_start,omitempty"`
	NextEnd   string   `json:"next_end,omitempty"`
	Task      string   `json:"task"`
	Config    string   `json:"config"`
	Log       string   `json:"log"`
}

func statusOf(cfg *config, now time.Time) status {
	s := status{
		Days: cfg.days, Windows: cfg.windows, Interval: cfg.interval.String(),
		Active: cfg.sched.Active(now), Task: taskStatus(),
		Config: configPath(), Log: logPath(),
	}
	if u, ok := pausedUntil(now); ok {
		s.Active = false
		s.Paused = u.Format(time.RFC3339)
	}
	if a, b, ok := cfg.sched.Next(now); ok {
		s.NextStart, s.NextEnd = a.Format(time.RFC3339), b.Format(time.RFC3339)
	}
	return s
}

// shortState is a one-word state for button labels and scripts.
func shortState(cfg *config, now time.Time, task string) string {
	switch {
	case task == "not installed":
		return "OFF"
	case func() bool { _, p := pausedUntil(now); return p }():
		return "PAUSED"
	case cfg.sched.Active(now):
		return "ACTIVE"
	default:
		return "IDLE"
	}
}

func statusText(cfg *config, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "days:     %s\n", cfg.days)
	fmt.Fprintf(&b, "windows:  %s\n", strings.Join(cfg.windows, ", "))
	fmt.Fprintf(&b, "interval: %s\n", cfg.interval)
	if u, ok := pausedUntil(now); ok {
		fmt.Fprintf(&b, "active:   false (PAUSED until %s)\n", u.Format("Mon Jan 2 15:04"))
	} else {
		fmt.Fprintf(&b, "active:   %v\n", cfg.sched.Active(now))
	}
	if s, e, ok := cfg.sched.Next(now); ok {
		fmt.Fprintf(&b, "next:     %s -> %s\n", s.Format("Mon Jan 2 15:04"), e.Format("15:04"))
	}
	fmt.Fprintf(&b, "task:     %s\n", taskStatus())
	return b.String()
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

	active, paused := false, false
	var last time.Time
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		now := time.Now()
		until, p := pausedUntil(now)
		if p != paused {
			paused = p
			if p {
				lg.Printf("paused until %s", until.Format("Mon Jan 2 15:04"))
			} else {
				lg.Printf("pause over")
			}
		}
		in := !paused && cfg.sched.Active(now)
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
