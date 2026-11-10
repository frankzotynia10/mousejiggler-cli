package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A pause is a single timestamp in pause.json. While now < until, the run
// loop treats every window as inactive. It's separate from config.json so
// pausing never touches the schedule and needs no reinstall.

func pausePath() string { return filepath.Join(appDir(), "pause.json") }

type pauseFile struct {
	Until time.Time `json:"until"`
}

// pausedUntil returns the pause end if a pause is in effect at now.
func pausedUntil(now time.Time) (time.Time, bool) {
	b, err := os.ReadFile(pausePath())
	if err != nil {
		return time.Time{}, false
	}
	var p pauseFile
	if json.Unmarshal(b, &p) != nil || !now.Before(p.Until) {
		return time.Time{}, false
	}
	return p.Until, true
}

func setPause(until time.Time) error {
	if err := os.MkdirAll(appDir(), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(pauseFile{Until: until})
	tmp := pausePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, pausePath())
}

func clearPause() error {
	err := os.Remove(pausePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func endOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, t.Location())
}

// parsePauseUntil computes the pause end from pause's flags.
//
//	(none)                rest of today (until midnight)
//	--for 3h              for a duration
//	--days 2              today and tomorrow (until midnight after day N)
//	--until 13:30         until that time (today, or tomorrow if already past)
//	--until 2026-11-14    until midnight starting that date
//	--until "2026-11-14 08:00"
func parsePauseUntil(args []string, now time.Time) (time.Time, error) {
	fs := flag.NewFlagSet("pause", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dur := fs.Duration("for", 0, "")
	days := fs.Int("days", 0, "")
	until := fs.String("until", "", "")
	if err := fs.Parse(args); err != nil {
		return time.Time{}, err
	}
	if fs.NArg() > 0 {
		return time.Time{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	set := 0
	for _, b := range []bool{*dur != 0, *days != 0, *until != ""} {
		if b {
			set++
		}
	}
	if set > 1 {
		return time.Time{}, errors.New("use only one of --for, --days, --until")
	}
	switch {
	case *dur != 0:
		if *dur < 0 {
			return time.Time{}, errors.New("--for must be positive")
		}
		return now.Add(*dur), nil
	case *days != 0:
		if *days < 0 {
			return time.Time{}, errors.New("--days must be positive")
		}
		return endOfDay(now).AddDate(0, 0, *days-1), nil
	case *until != "":
		s := strings.TrimSpace(*until)
		for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
			if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
				if !t.After(now) {
					return time.Time{}, fmt.Errorf("--until %s is in the past", s)
				}
				return t, nil
			}
		}
		min, err := parseClock(s)
		if err != nil {
			return time.Time{}, fmt.Errorf("--until: use HH:MM, YYYY-MM-DD or \"YYYY-MM-DD HH:MM\"")
		}
		y, m, d := now.Date()
		t := time.Date(y, m, d, 0, min, 0, 0, now.Location())
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		return t, nil
	default:
		return endOfDay(now), nil
	}
}
