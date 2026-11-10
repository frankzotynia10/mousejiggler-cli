package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Window is a daily time range in minutes since midnight, [Start, End).
type Window struct {
	Start, End int
}

func (w Window) String() string {
	return fmt.Sprintf("%02d:%02d-%02d:%02d", w.Start/60, w.Start%60, w.End/60, w.End%60)
}

// Schedule is a set of weekdays plus the daily windows active on those days.
type Schedule struct {
	Days    [7]bool // indexed by time.Weekday
	Windows []Window
}

var dayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

func parseDay(s string) (time.Weekday, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) >= 3 {
		if d, ok := dayNames[s[:3]]; ok {
			return d, nil
		}
	}
	return 0, fmt.Errorf("unknown day %q (use Mon, Tue, ...)", s)
}

// ParseDays accepts "Mon-Fri", "Mon,Wed,Fri", "Sat-Sun", "Fri-Mon" (wraps), or "all".
func ParseDays(s string) ([7]bool, error) {
	var days [7]bool
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "all") || s == "*" {
		for i := range days {
			days[i] = true
		}
		return days, nil
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if a, b, ok := strings.Cut(part, "-"); ok {
			from, err := parseDay(a)
			if err != nil {
				return days, err
			}
			to, err := parseDay(b)
			if err != nil {
				return days, err
			}
			for d := from; ; d = (d + 1) % 7 {
				days[d] = true
				if d == to {
					break
				}
			}
		} else {
			d, err := parseDay(part)
			if err != nil {
				return days, err
			}
			days[d] = true
		}
	}
	for _, on := range days {
		if on {
			return days, nil
		}
	}
	return days, fmt.Errorf("no days in %q", s)
}

func parseClock(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, fmt.Errorf("bad time %q (use 24h HH:MM)", s)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 24 || mm < 0 || mm > 59 || (hh == 24 && mm != 0) {
		return 0, fmt.Errorf("bad time %q (use 24h HH:MM)", s)
	}
	return hh*60 + mm, nil
}

// ParseWindow accepts "08:05-12:05" (24h clock, end after start, same day).
func ParseWindow(s string) (Window, error) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return Window{}, fmt.Errorf("bad window %q (use HH:MM-HH:MM)", s)
	}
	start, err := parseClock(a)
	if err != nil {
		return Window{}, err
	}
	end, err := parseClock(b)
	if err != nil {
		return Window{}, err
	}
	if end <= start {
		return Window{}, fmt.Errorf("window %q: end must be after start (24h clock, e.g. 13:30-17:30)", s)
	}
	return Window{start, end}, nil
}

// Active reports whether t falls inside a scheduled window.
func (s Schedule) Active(t time.Time) bool {
	if !s.Days[t.Weekday()] {
		return false
	}
	min := t.Hour()*60 + t.Minute()
	for _, w := range s.Windows {
		if min >= w.Start && min < w.End {
			return true
		}
	}
	return false
}

// Next returns the window containing t, or the next one to start after t.
func (s Schedule) Next(t time.Time) (start, end time.Time, ok bool) {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	for i := 0; i < 8; i++ {
		d := day.AddDate(0, 0, i)
		if !s.Days[d.Weekday()] {
			continue
		}
		var best *Window
		for j := range s.Windows {
			w := &s.Windows[j]
			e := d.Add(time.Duration(w.End) * time.Minute)
			if !e.After(t) {
				continue
			}
			if best == nil || w.Start < best.Start {
				best = w
			}
		}
		if best != nil {
			return d.Add(time.Duration(best.Start) * time.Minute), d.Add(time.Duration(best.End) * time.Minute), true
		}
	}
	return time.Time{}, time.Time{}, false
}

// DaysString renders days compactly in Mon..Sun order, e.g. "Mon-Fri",
// "Mon,Wed,Fri", "Mon-Wed,Sat-Sun".
func DaysString(days [7]bool) string {
	order := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}
	var parts []string
	for i := 0; i < len(order); {
		if !days[order[i]] {
			i++
			continue
		}
		j := i
		for j+1 < len(order) && days[order[j+1]] {
			j++
		}
		a := order[i].String()[:3]
		switch {
		case j == i:
			parts = append(parts, a)
		default:
			parts = append(parts, a+"-"+order[j].String()[:3])
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}
