package main

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestTaskXML(t *testing.T) {
	cfg, err := parseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	out := taskXML(`PC\frank`, `C:\Users\frank\AppData\Local\Programs\x\jiggler.exe`, cfg.taskArgs(), cfg.sched)
	// encoding="UTF-16" is only true once written as UTF-16; strip for parsing here.
	body := out[strings.Index(out, "?>")+2:]
	var v struct {
		Calendar []struct {
			StartBoundary string `xml:"StartBoundary"`
			Days          struct {
				Inner string `xml:",innerxml"`
			} `xml:"ScheduleByWeek>DaysOfWeek"`
		} `xml:"Triggers>CalendarTrigger"`
		Args string `xml:"Actions>Exec>Arguments"`
		Wake bool   `xml:"Settings>WakeToRun"`
	}
	if err := xml.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Calendar) != 2 ||
		v.Calendar[0].StartBoundary != "2024-01-01T08:05:00" ||
		v.Calendar[1].StartBoundary != "2024-01-01T13:30:00" {
		t.Fatalf("triggers: %+v", v.Calendar)
	}
	if v.Calendar[0].Days.Inner != "<Monday/><Tuesday/><Wednesday/><Thursday/><Friday/>" {
		t.Fatalf("days: %s", v.Calendar[0].Days.Inner)
	}
	if v.Args != "run --days Mon-Fri --window 08:05-12:05 --window 13:30-17:30 --interval 1m0s" {
		t.Fatalf("args: %s", v.Args)
	}
	if !v.Wake {
		t.Fatal("WakeToRun not set")
	}
}
