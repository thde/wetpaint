package main

import "testing"

func TestDefaultURL(t *testing.T) {
	t.Parallel()
	env := func(url string) func(string) string {
		return func(k string) string {
			if k == "WETPAINT_URL" {
				return url
			}
			return ""
		}
	}
	if got := defaultURL(env("")); got != "ws://localhost:8080/ws" {
		t.Errorf("without WETPAINT_URL: got %q, want ws://localhost:8080/ws", got)
	}
	if got := defaultURL(env("wss://play.example.com/ws")); got != "wss://play.example.com/ws" {
		t.Errorf("with WETPAINT_URL: got %q, want wss://play.example.com/ws", got)
	}
}
