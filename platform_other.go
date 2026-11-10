//go:build !windows

package main

import "errors"

// Non-Windows stubs so the schedule logic builds and tests anywhere.

var errUnsupported = errors.New("only supported on Windows")

func jiggle() error                   { return errUnsupported }
func attachParentConsole()            {}
func singleInstance() (func(), error) { return func() {}, nil }
func install(*config) error           { return errUnsupported }
func uninstall() error                { return errUnsupported }
func taskStatus()                     {}

type awakeKeeper struct{}

func newAwakeKeeper() *awakeKeeper { return &awakeKeeper{} }
func (*awakeKeeper) Set(bool)      {}
func (*awakeKeeper) Close()        {}
