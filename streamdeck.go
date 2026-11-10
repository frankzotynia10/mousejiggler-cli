package main

import (
	"flag"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// Stream Deck plugin mode. The same jiggler.exe ships inside the
// .sdPlugin folder; Stream Deck launches it with
//   -port N -pluginUUID U -registerEvent E -info JSON
// and we talk to it over a local WebSocket. One action, "toggle":
// press = pause rest of today / resume; the key shows the real state
// (ACTIVE / IDLE / PAUSED / OFF), polled every few seconds, so it never
// drifts when you pause from the CLI/GUI or a pause expires.

func isStreamDeckLaunch(args []string) bool {
	for _, a := range args {
		if a == "-registerEvent" {
			return true
		}
	}
	return false
}

type sdEvent struct {
	Event   string `json:"event"`
	Context string `json:"context"`
}

type sdKey struct {
	state int
	title string
}

type sdPlugin struct {
	mu       sync.Mutex
	ws       *websocket.Conn
	contexts map[string]sdKey // visible keys -> last sent state/title

	taskMu   sync.Mutex
	task     string
	taskTime time.Time
}

func runStreamDeck(args []string) error {
	fs := flag.NewFlagSet("streamdeck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	port := fs.Int("port", 0, "")
	uuid := fs.String("pluginUUID", "", "")
	reg := fs.String("registerEvent", "", "")
	fs.String("info", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lg := openLog()
	ws, err := websocket.Dial(fmt.Sprintf("ws://127.0.0.1:%d", *port), "", "http://127.0.0.1/")
	if err != nil {
		lg.Printf("streamdeck: connect: %v", err)
		return err
	}
	p := &sdPlugin{ws: ws, contexts: map[string]sdKey{}}
	if err := p.send(map[string]string{"event": *reg, "uuid": *uuid}); err != nil {
		return err
	}
	go func() {
		for range time.Tick(3 * time.Second) {
			p.refreshAll(false)
		}
	}()
	for {
		var ev sdEvent
		if err := websocket.JSON.Receive(ws, &ev); err != nil {
			return nil // Stream Deck closed the connection (quit/update)
		}
		switch ev.Event {
		case "willAppear":
			p.mu.Lock()
			p.contexts[ev.Context] = sdKey{state: -1}
			p.mu.Unlock()
			p.refreshAll(false)
		case "willDisappear":
			p.mu.Lock()
			delete(p.contexts, ev.Context)
			p.mu.Unlock()
		case "keyDown":
			var err error
			if _, paused := pausedUntil(time.Now()); paused {
				err = clearPause()
			} else {
				err = setPause(endOfDay(time.Now()))
			}
			if err != nil {
				lg.Printf("streamdeck: toggle: %v", err)
				p.send(map[string]string{"event": "showAlert", "context": ev.Context})
			}
			p.refreshAll(true)
		}
	}
}

func (p *sdPlugin) send(v any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return websocket.JSON.Send(p.ws, v)
}

// cachedTaskStatus avoids spawning schtasks on every poll.
func (p *sdPlugin) cachedTaskStatus(force bool) string {
	p.taskMu.Lock()
	defer p.taskMu.Unlock()
	if force || p.task == "" || time.Since(p.taskTime) > 30*time.Second {
		p.task, p.taskTime = taskStatus(), time.Now()
	}
	return p.task
}

// keyFace maps the jiggler state to the key's image state (0 = on art,
// 1 = paused/off art) and title.
func keyFace(now time.Time, task string) (int, string) {
	cfg, err := loadConfig()
	if err != nil {
		return 1, "CONFIG\nERROR"
	}
	switch st := shortState(cfg, now, task); st {
	case "PAUSED":
		until, _ := pausedUntil(now)
		when := until.Format("Mon\n15:04")
		if until.Equal(endOfDay(now)) {
			when = "today"
		}
		return 1, "PAUSED\n" + when
	case "OFF":
		return 1, "OFF"
	default:
		return 0, st
	}
}

func (p *sdPlugin) refreshAll(force bool) {
	state, title := keyFace(time.Now(), p.cachedTaskStatus(force))
	p.mu.Lock()
	var stale []string
	for ctx, k := range p.contexts {
		if force || k.state != state || k.title != title {
			stale = append(stale, ctx)
			p.contexts[ctx] = sdKey{state, title}
		}
	}
	p.mu.Unlock()
	for _, ctx := range stale {
		p.send(map[string]any{"event": "setState", "context": ctx, "payload": map[string]any{"state": state}})
		p.send(map[string]any{"event": "setTitle", "context": ctx, "payload": map[string]any{"title": title, "target": 0}})
	}
}
