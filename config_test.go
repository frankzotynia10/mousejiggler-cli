package main

import (
	"testing"
	"time"
)

func TestConfigRoundTripAndFlags(t *testing.T) {
	t.Setenv("MOUSEJIGGLER_HOME", t.TempDir())

	c, err := loadConfig()
	if err != nil || c.days != "Mon-Fri" || len(c.windows) != 2 || c.interval != time.Minute {
		t.Fatalf("defaults: %+v %v", c, err)
	}

	c, err = parseConfig([]string{"--days", "Mon,Wed", "--window", "9:00-10:00", "--interval", "30s"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}

	// Flags not given fall back to the saved config.
	c2, err := parseConfig([]string{"--interval", "2m"})
	if err != nil {
		t.Fatal(err)
	}
	if c2.days != "Mon,Wed" || len(c2.windows) != 1 || c2.windows[0] != "09:00-10:00" || c2.interval != 2*time.Minute {
		t.Fatalf("merge: %+v", c2)
	}
}
