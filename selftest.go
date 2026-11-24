package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// selfTest checks, on this machine, that a jiggle resets the system idle
// timer, comparing the old zero-distance move. The result goes to the
// console, the log and a message box, so it can't be missed.
func selfTest() error {
	lg := openLog()
	var report []string
	line := func(s string) {
		fmt.Println(s)
		lg.Printf("test: %s", s)
		report = append(report, s)
	}
	try := func(name string, f func() error) (bool, error) {
		fmt.Printf("%s: hands off the mouse and keyboard (8s)...\n", name)
		time.Sleep(8 * time.Second)
		before := idleSeconds()
		if before < 5 {
			return false, errors.New("input detected during the test; run it again without touching anything")
		}
		if err := f(); err != nil {
			return false, err
		}
		time.Sleep(500 * time.Millisecond)
		after := idleSeconds()
		ok := after < before
		verdict := "IGNORED"
		if ok {
			verdict = "RESETS idle"
		}
		line(fmt.Sprintf("%s: idle %ds -> %ds, %s", name, before, after, verdict))
		return ok, nil
	}
	fail := func(err error) error {
		line("FAIL: " + err.Error())
		notify(strings.Join(report, "\n"), false)
		return err
	}
	if _, err := try("zero-distance move (old)", zeroJiggle); err != nil {
		return fail(err)
	}
	ok, err := try("1px out-and-back (current)", jiggle)
	if err != nil {
		return fail(err)
	}
	if !ok {
		return fail(errors.New("the jiggle did not reset the idle timer; Teams will go Away"))
	}
	line("PASS: jiggles count as activity on this machine")
	notify(strings.Join(report, "\n"), true)
	return nil
}
