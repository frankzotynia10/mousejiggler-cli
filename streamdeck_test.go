package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Fake Stream Deck host: checks registration, shows a key, presses it twice.
func TestStreamDeckProtocol(t *testing.T) {
	t.Setenv("MOUSEJIGGLER_HOME", t.TempDir())
	var running atomic.Bool
	running.Store(true)
	sdLoopRunning = running.Load
	sdTaskStatus = func() string { return "Running" }
	defer func() { sdLoopRunning, sdTaskStatus = loopRunning, taskStatus }()
	type msg struct {
		Event   string         `json:"event"`
		UUID    string         `json:"uuid"`
		Context string         `json:"context"`
		Payload map[string]any `json:"payload"`
	}
	done := make(chan error, 1)
	// Like Stream Deck: refuse any handshake that carries an Origin header.
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("handshake rejected: %v", err)
			return
		}
		defer ws.Close()
		recv := func() msg {
			var m msg
			ws.SetReadDeadline(time.Now().Add(5 * time.Second))
			if err := ws.ReadJSON(&m); err != nil {
				t.Errorf("receive: %v", err)
			}
			return m
		}
		// Wait for the next setTitle, skipping setState/showOk etc.
		title := func() string {
			for i := 0; i < 10; i++ {
				if m := recv(); m.Event == "setTitle" {
					return m.Payload["title"].(string)
				}
			}
			return ""
		}
		if m := recv(); m.Event != "registerPlugin" || m.UUID != "plugin-uuid" {
			t.Errorf("bad register: %+v", m)
		}
		ws.WriteJSON(map[string]string{"event": "willAppear", "context": "key1"})
		if got := title(); got == "" || strings.HasPrefix(got, "PAUSED") {
			t.Errorf("initial title %q", got)
		}
		ws.WriteJSON(map[string]string{"event": "keyDown", "context": "key1"})
		if got := title(); got != "PAUSED\ntoday" {
			t.Errorf("after first press %q", got)
		}
		ws.WriteJSON(map[string]string{"event": "keyDown", "context": "key1"})
		if got := title(); strings.HasPrefix(got, "PAUSED") {
			t.Errorf("after second press %q", got)
		}
		// Background loop dies -> next poll (<=3s) shows STOPPED.
		running.Store(false)
		ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		if got := title(); got != "STOPPED" {
			t.Errorf("after loop died %q", got)
		}
	}))
	defer srv.Close()
	port := srv.Listener.Addr().(interface{ String() string }).String()
	port = port[strings.LastIndex(port, ":")+1:]
	if _, err := strconv.Atoi(port); err != nil {
		t.Fatal(err)
	}
	go func() {
		done <- runStreamDeck([]string{"-port", port, "-pluginUUID", "plugin-uuid", "-registerEvent", "registerPlugin", "-info", "{}"})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("plugin did not exit after host closed connection")
	}
}
