package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CAOShurong/coldshelf/internal/catalog"
	"github.com/CAOShurong/coldshelf/internal/server"
)

func TestAPIAndSecurityBoundary(t *testing.T) {
	t.Parallel()
	db, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srv, err := server.New(db, "test")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/health", nil)
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("health response: %d %#v", response.Code, response.Header())
	}
	request = httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	response = httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Location") != "" || !bytes.Contains(response.Body.Bytes(), []byte("Know what’s on the shelf")) {
		t.Fatalf("embedded UI response: %d location=%q", response.Code, response.Header().Get("Location"))
	}

	request = httptest.NewRequest(http.MethodGet, "http://attacker.test/api/health", nil)
	response = httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected hostile Host rejection, got %d", response.Code)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "needle.txt"), []byte("found"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(server.ScanRequest{Name: "Test Drive", Path: root, HashMode: "full"})
	request = httptest.NewRequest(http.MethodPost, "http://localhost/api/scans", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("start scan: %d %s", response.Code, response.Body.String())
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		request = httptest.NewRequest(http.MethodGet, "http://localhost/api/jobs/"+job.ID, nil)
		response = httptest.NewRecorder()
		srv.Handler().ServeHTTP(response, request)
		var status struct{ Status, Error string }
		if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status.Status == "complete" {
			break
		}
		if status.Status == "failed" {
			t.Fatal(status.Error)
		}
		if time.Now().After(deadline) {
			t.Fatal("scan job timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
	hits, err := db.Search(context.Background(), "needle", "", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search after API scan: %#v, %v", hits, err)
	}
}
