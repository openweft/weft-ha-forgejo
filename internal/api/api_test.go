package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReady_503WhenDown(t *testing.T) {
	s := New(":0", "forgejo-ha-abc", "node-1", "dc1")
	s.Update(State{Up: false, InstallName: "forgejo-ha-abc", NodeName: "node-1", DC: "dc1"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ready", nil)
	s.ready(w, r)
	if w.Result().StatusCode != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", w.Result().StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Result().Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "fail" {
		t.Errorf("status = %q, want %q (IETF vocab)", body["status"], "fail")
	}
}

func TestReady_200WhenUp(t *testing.T) {
	s := New(":0", "forgejo-ha-abc", "node-1", "dc1")
	s.Update(State{Up: true, InstallName: "forgejo-ha-abc", NodeName: "node-1", DC: "dc1"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ready", nil)
	s.ready(w, r)
	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("got %d, want 200", w.Result().StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Result().Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "pass" {
		t.Errorf("status = %q, want %q (IETF vocab)", body["status"], "pass")
	}
}

func TestInfo_RendersJSON(t *testing.T) {
	s := New(":0", "forgejo-ha-abc", "node-1", "dc1")
	s.Update(State{Up: true, InstallName: "forgejo-ha-abc", NodeName: "node-1", DC: "dc1", Version: "10.0.0"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/info", nil)
	s.info(w, r)
	if ct := w.Result().Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type : got %q, want application/json", ct)
	}
	var body struct {
		Install string `json:"install"`
		Version string `json:"version"`
		Up      bool   `json:"up"`
	}
	if err := json.NewDecoder(w.Result().Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Install != "forgejo-ha-abc" || body.Version != "10.0.0" || !body.Up {
		t.Errorf("body : got %+v", body)
	}
}
