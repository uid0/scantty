package omsapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListProjectStoragePrintQueue(t *testing.T) {
	body := `[{"stint_id":"PS-AB23CDFG","print_target":"epson_tm","created_at":"2026-06-07T00:00:00Z","label_url":"http://example/label/"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/project-storage/stints/print-queue/" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL)
	entries, err := c.ListProjectStoragePrintQueue(context.Background())
	if err != nil {
		t.Fatalf("ListProjectStoragePrintQueue: %v", err)
	}
	if len(entries) != 1 || entries[0].StintID != "PS-AB23CDFG" || entries[0].LabelURL != "http://example/label/" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestGetBytesAbsoluteURLBypassesBase(t *testing.T) {
	// The label_url comes back absolute from the queue; GetBytes must
	// honor it verbatim rather than concatenating onto baseURL.
	want := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 'p', 'a', 'y', 'l', 'o', 'a', 'd'}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(want)
	}))
	defer srv.Close()

	// Note the different baseURL — GetBytes should call srv.URL anyway
	// because we passed it as an absolute URL.
	c := New("http://nonexistent.example/")
	got, err := c.GetBytes(context.Background(), srv.URL+"/anything/")
	if err != nil {
		t.Fatalf("GetBytes: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("got %x want %x", got, want)
	}
}

func TestGetBytesRelativePathUsesBase(t *testing.T) {
	want := []byte("hello-png")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/some/thing/" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write(want)
	}))
	defer srv.Close()

	c := New(srv.URL)
	got, err := c.GetBytes(context.Background(), "/some/thing/")
	if err != nil {
		t.Fatalf("GetBytes: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestMarkProjectStorageStintPrinted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/PS-XYZ12345/mark-printed/") {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("wrong content type: %q", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.MarkProjectStorageStintPrinted(context.Background(), "PS-XYZ12345", "ok"); err != nil {
		t.Fatalf("MarkProjectStorageStintPrinted: %v", err)
	}
}

func TestGetBytesNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.GetBytes(context.Background(), "/missing/"); err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}
