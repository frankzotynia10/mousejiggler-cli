package main

import (
	"testing"
	"time"
)

func mustSched(t *testing.T, days string, windows ...string) Schedule {
	t.Helper()
	d, err := ParseDays(days)
	if err != nil {
		t.Fatal(err)
	}
	s := Schedule{Days: d}
	for _, w := range windows {
		win, err := ParseWindow(w)
		if err != nil {
			t.Fatal(err)
		}
		s.Windows = append(s.Windows, win)
	}
	return s
}

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseDays(t *testing.T) {
	d, _ := ParseDays("Mon-Fri")
	if d[time.Saturday] || d[time.Sunday] || !d[time.Monday] || !d[time.Friday] {
		t.Fatalf("Mon-Fri wrong: %v", d)
	}
	d, _ = ParseDays("Fri-Mon")
	if !d[time.Saturday] || !d[time.Sunday] || d[time.Wednesday] {
		t.Fatalf("Fri-Mon wrap wrong: %v", d)
	}
	d, _ = ParseDays("monday, wed")
	if !d[time.Monday] || !d[time.Wednesday] || d[time.Tuesday] {
		t.Fatalf("list wrong: %v", d)
	}
	if _, err := ParseDays("Funday"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseWindow(t *testing.T) {
	if _, err := ParseWindow("17:30-13:30"); err == nil {
		t.Fatal("expected error for reversed window")
	}
	w, err := ParseWindow("13:30-17:30")
	if err != nil || w.String() != "13:30-17:30" {
		t.Fatalf("got %v %v", w, err)
	}
}

func TestActive(t *testing.T) {
	s := mustSched(t, "Mon-Fri", "08:05-12:05", "13:30-17:30")
	// 2026-11-09 is a Monday.
	cases := map[string]bool{
		"2026-11-09 08:04": false,
		"2026-11-09 08:05": true,
		"2026-11-09 12:04": true,
		"2026-11-09 12:05": false,
		"2026-11-09 13:00": false,
		"2026-11-09 13:30": true,
		"2026-11-09 17:29": true,
		"2026-11-09 17:30": false,
		"2026-11-13 09:00": true,  // Friday
		"2026-11-14 09:00": false, // Saturday
	}
	for ts, want := range cases {
		if got := s.Active(at(ts)); got != want {
			t.Errorf("%s: got %v want %v", ts, got, want)
		}
	}
}

func TestNext(t *testing.T) {
	s := mustSched(t, "Mon-Fri", "08:05-12:05", "13:30-17:30")
	cases := map[string]string{
		"2026-11-09 07:00": "2026-11-09 08:05", // before first window
		"2026-11-09 09:00": "2026-11-09 08:05", // inside -> current window
		"2026-11-09 12:30": "2026-11-09 13:30", // lunch
		"2026-11-13 18:00": "2026-11-16 08:05", // Friday evening -> Monday
	}
	for ts, want := range cases {
		start, _, ok := s.Next(at(ts))
		if !ok || !start.Equal(at(want)) {
			t.Errorf("%s: got %v want %s", ts, start, want)
		}
	}
}

func TestDaysString(t *testing.T) {
	for in, want := range map[string]string{
		"Mon-Fri":     "Mon-Fri",
		"all":         "Mon-Sun",
		"fri,mon,wed": "Mon,Wed,Fri",
		"Sat-Sun":     "Sat-Sun",
		"Mon-Tue,Thu": "Mon-Tue,Thu",
	} {
		d, err := ParseDays(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := DaysString(d); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}
