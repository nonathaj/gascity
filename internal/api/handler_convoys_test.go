package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/julianknutsen/gascity/internal/beads"
)

func TestConvoyCreateAndGet(t *testing.T) {
	state := newFakeMutatorState(t)
	srv := New(state)

	// Create a bead to link as convoy item.
	store := state.stores["myrig"]
	item, err := store.Create(beads.Bead{Title: "task-1"})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	// Create convoy with the item.
	body := `{"rig":"myrig","title":"test convoy","items":["` + item.ID + `"]}`
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, newPostRequest("/v0/convoys", strings.NewReader(body)))

	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	var convoy beads.Bead
	if err := json.NewDecoder(rec.Body).Decode(&convoy); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if convoy.Type != "convoy" {
		t.Fatalf("type = %q, want %q", convoy.Type, "convoy")
	}

	// Get convoy.
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/v0/convoy/"+convoy.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d, want 200", rec.Code)
	}
}

func TestConvoyCreateInvalidItem(t *testing.T) {
	state := newFakeMutatorState(t)
	srv := New(state)

	body := `{"rig":"myrig","title":"test","items":["nonexistent"]}`
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, newPostRequest("/v0/convoys", strings.NewReader(body)))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestConvoyAddItems(t *testing.T) {
	state := newFakeMutatorState(t)
	srv := New(state)

	store := state.stores["myrig"]
	convoy, _ := store.Create(beads.Bead{Title: "convoy", Type: "convoy"})
	item, _ := store.Create(beads.Bead{Title: "task"})

	body := `{"items":["` + item.ID + `"]}`
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, newPostRequest("/v0/convoy/"+convoy.ID+"/add", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("add: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
}

func TestConvoyClose(t *testing.T) {
	state := newFakeMutatorState(t)
	srv := New(state)

	store := state.stores["myrig"]
	convoy, _ := store.Create(beads.Bead{Title: "convoy", Type: "convoy"})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, newPostRequest("/v0/convoy/"+convoy.ID+"/close", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("close: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
}

func TestConvoyNotFound(t *testing.T) {
	state := newFakeMutatorState(t)
	srv := New(state)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/v0/convoy/nonexistent", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestConvoyList(t *testing.T) {
	state := newFakeMutatorState(t)
	srv := New(state)

	store := state.stores["myrig"]
	if _, err := store.Create(beads.Bead{Title: "convoy", Type: "convoy"}); err != nil {
		t.Fatalf("create convoy: %v", err)
	}
	if _, err := store.Create(beads.Bead{Title: "task", Type: "task"}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/v0/convoys", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp listResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("total = %d, want 1 (only convoys)", resp.Total)
	}
}
