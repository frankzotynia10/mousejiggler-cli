package main

import (
	"errors"
	"fmt"
	"time"
)

// selfTest checks, on this machine, that a jiggle resets the system idle
// timer. It also tries the old zero-distance move for comparison.
func selfTest() error {
	try := func(name string, f func() error) (bool, error) {
		fmt.Printf("%s: hands off the mouse and keyboard...", name)
		time.Sleep(8 * time.Second)
		before := idleSeconds()
		if before < 5 {
			return false, errors.New("you touched something; run it again hands-off")
		}
		if err := f(); err != nil {
			return false, err
		}
		time.Sleep(500 * time.Millisecond)
		after := idleSeconds()
		ok := after < before
		fmt.Printf(" idle %ds -> %ds  %s\n", before, after, map[bool]string{true: "RESETS idle", false: "IGNORED"}[ok])
		return ok, nil
	}
	if _, err := try("zero-distance move (old)", zeroJiggle); err != nil {
		return err
	}
	ok, err := try("1px out-and-back (current)", jiggle)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("the jiggle did not reset the idle timer; Teams will go Away")
	}
	fmt.Println("PASS: jiggles count as activity on this machine")
	return nil
}
