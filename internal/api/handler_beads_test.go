package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestBeadCRUD(t *testing.T) {
	state := newFakeState(t)
	srv := New(state)

	// Create a bead.
	body := `{"rig":"myrig","title":"Fix login bug","type":"task"}`
	req := newPostRequest("/v0/beads", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var created beads.Bead
	json.NewDecoder(rec.Body).Decode(&created) //nolint:errcheck
	if created.Title != "Fix login bug" {
		t.Errorf("Title = %q, want %q", created.Title, "Fix login bug")
	}
	if created.ID == "" {
		t.Fatal("created bead has no ID")
	}

	// Get the bead.
	req = httptest.NewRequest("GET", "/v0/bead/"+created.ID, nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got beads.Bead
	json.NewDecoder(rec.Body).Decode(&got) //nolint:errcheck
	if got.Title != "Fix login bug" {
		t.Errorf("Title = %q, want %q", got.Title, "Fix login bug")
	}

	// Close the bead.
	req = newPostRequest("/v0/bead/"+created.ID+"/close", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("close status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify closed.
	req = httptest.NewRequest("GET", "/v0/bead/"+created.ID, nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	json.NewDecoder(rec.Body).Decode(&got) //nolint:errcheck
	if got.Status != "closed" {
		t.Errorf("Status = %q, want %q", got.Status, "closed")
	}
}

func TestBeadListFiltering(t *testing.T) {
	state := newFakeState(t)
	store := state.stores["myrig"]
	store.Create(beads.Bead{Title: "Open task", Type: "task"})                           //nolint:errcheck
	store.Create(beads.Bead{Title: "Message", Type: "message"})                          //nolint:errcheck
	store.Create(beads.Bead{Title: "Labeled", Type: "task", Labels: []string{"urgent"}}) //nolint:errcheck
	srv := New(state)

	// Filter by type.
	req := httptest.NewRequest("GET", "/v0/beads?type=message", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp struct {
		Items []beads.Bead `json:"items"`
		Total int          `json:"total"`
	}
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp.Total != 1 {
		t.Errorf("type filter: Total = %d, want 1", resp.Total)
	}

	// Filter by label.
	req = httptest.NewRequest("GET", "/v0/beads?label=urgent", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp.Total != 1 {
		t.Errorf("label filter: Total = %d, want 1", resp.Total)
	}
}

func TestBeadListCrossRig(t *testing.T) {
	state := newFakeState(t)
	store2 := beads.NewMemStore()
	state.stores["rig2"] = store2

	state.stores["myrig"].Create(beads.Bead{Title: "Bead from rig1"}) //nolint:errcheck
	store2.Create(beads.Bead{Title: "Bead from rig2"})                //nolint:errcheck
	srv := New(state)

	req := httptest.NewRequest("GET", "/v0/beads", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp struct {
		Items []beads.Bead `json:"items"`
		Total int          `json:"total"`
	}
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp.Total != 2 {
		t.Errorf("cross-rig: Total = %d, want 2", resp.Total)
	}
}

func TestBeadGetNotFound(t *testing.T) {
	state := newFakeState(t)
	srv := New(state)

	req := httptest.NewRequest("GET", "/v0/bead/nonexistent", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestBeadReady(t *testing.T) {
	state := newFakeState(t)
	store := state.stores["myrig"]
	store.Create(beads.Bead{Title: "Open"}) //nolint:errcheck
	b2, _ := store.Create(beads.Bead{Title: "Closed"})
	store.Close(b2.ID) //nolint:errcheck
	srv := New(state)

	req := httptest.NewRequest("GET", "/v0/beads/ready", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp struct {
		Items []beads.Bead `json:"items"`
		Total int          `json:"total"`
	}
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp.Total != 1 {
		t.Errorf("ready: Total = %d, want 1", resp.Total)
	}
}

func TestBeadUpdate(t *testing.T) {
	state := newFakeState(t)
	store := state.stores["myrig"]
	b, _ := store.Create(beads.Bead{Title: "Test"})
	srv := New(state)

	desc := "updated description"
	body := `{"description":"` + desc + `","labels":["new-label"]}`
	req := newPostRequest("/v0/bead/"+b.ID+"/update", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify update.
	got, _ := store.Get(b.ID)
	if got.Description != desc {
		t.Errorf("Description = %q, want %q", got.Description, desc)
	}
	if len(got.Labels) != 1 || got.Labels[0] != "new-label" {
		t.Errorf("Labels = %v, want [new-label]", got.Labels)
	}
}

func TestBeadCreateValidation(t *testing.T) {
	state := newFakeState(t)
	srv := New(state)

	// Missing title.
	req := newPostRequest("/v0/beads", bytes.NewBufferString(`{"rig":"myrig"}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
