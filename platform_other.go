//go:build !windows

package main

import "errors"

// Non-Windows stubs so the schedule logic builds and tests anywhere.

var errUnsupported = errors.New("only supported on Windows")

func jiggle() error                   { return errUnsupported }
func attachParentConsole()            {}
func singleInstance() (func(), error) { return func() {}, nil }
func install(*config) (string, error) { return "", errUnsupported }
func uninstall() (string, error)      { return "", errUnsupported }
func runGUI() error                   { usage(); return errUnsupported }
func taskStatus() string              { return "n/a (not Windows)" }

type awakeKeeper struct{}

func newAwakeKeeper() *awakeKeeper { return &awakeKeeper{} }
func (*awakeKeeper) Set(bool)      {}
func (*awakeKeeper) Close()        {}
