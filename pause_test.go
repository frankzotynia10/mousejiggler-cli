package main

import (
	"testing"
	"time"
)

func TestParsePauseUntil(t *testing.T) {
	now := at("2026-11-10 09:15") // Tuesday
	cases := []struct {
		args []string
		want string
	}{
		{nil, "2026-11-11 00:00"},
		{[]string{"--for", "3h"}, "2026-11-10 12:15"},
		{[]string{"--days", "1"}, "2026-11-11 00:00"},
		{[]string{"--days", "2"}, "2026-11-12 00:00"},
		{[]string{"--until", "13:30"}, "2026-11-10 13:30"},
		{[]string{"--until", "08:00"}, "2026-11-11 08:00"}, // already past -> tomorrow
		{[]string{"--until", "2026-11-16"}, "2026-11-16 00:00"},
		{[]string{"--until", "2026-11-16 08:00"}, "2026-11-16 08:00"},
	}
	for _, c := range cases {
		got, err := parsePauseUntil(c.args, now)
		if err != nil || !got.Equal(at(c.want)) {
			t.Errorf("%v: got %v %v want %s", c.args, got, err, c.want)
		}
	}
	for _, bad := range [][]string{
		{"--for", "1h", "--days", "1"},
		{"--until", "2026-11-01"},
		{"--until", "noon"},
	} {
		if _, err := parsePauseUntil(bad, now); err == nil {
			t.Errorf("%v: expected error", bad)
		}
	}
}

func TestPauseFile(t *testing.T) {
	t.Setenv("MOUSEJIGGLER_HOME", t.TempDir())
	now := time.Now()
	if _, ok := pausedUntil(now); ok {
		t.Fatal("should not be paused")
	}
	if err := setPause(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok := pausedUntil(now); !ok {
		t.Fatal("should be paused")
	}
	if _, ok := pausedUntil(now.Add(2 * time.Hour)); ok {
		t.Fatal("pause should have expired")
	}
	if err := clearPause(); err != nil {
		t.Fatal(err)
	}
	if err := clearPause(); err != nil { // idempotent
		t.Fatal(err)
	}
	if _, ok := pausedUntil(now); ok {
		t.Fatal("should be resumed")
	}
}

func TestShortState(t *testing.T) {
	t.Setenv("MOUSEJIGGLER_HOME", t.TempDir())
	cfg := defaultConfig()
	in, out := at("2026-11-10 09:00"), at("2026-11-10 12:30")
	if got := shortState(cfg, in, "Running"); got != "ACTIVE" {
		t.Errorf("got %s", got)
	}
	if got := shortState(cfg, out, "Running"); got != "IDLE" {
		t.Errorf("got %s", got)
	}
	if got := shortState(cfg, in, "not installed"); got != "OFF" {
		t.Errorf("got %s", got)
	}
	if err := setPause(at("2026-11-11 00:00")); err != nil {
		t.Fatal(err)
	}
	if got := shortState(cfg, in, "Running"); got != "PAUSED" {
		t.Errorf("got %s", got)
	}
}
