package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadMedia_SizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, MaxUploadBytes: 10})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// Create a file larger than the 10-byte limit.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "big.txt")
	if err := os.WriteFile(path, make([]byte, 100), 0644); err != nil {
		t.Fatalf("writefile: %v", err)
	}
	_, err = c.UploadMedia(context.Background(), path, "")
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
}

func TestUploadMedia_NonRegularFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	// /dev/null is not a regular file.
	_, err := c.UploadMedia(context.Background(), "/dev/null", "")
	if err == nil {
		t.Fatal("expected error for non-regular file")
	}
}

func TestUploadMedia_NonexistentFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.UploadMedia(context.Background(), "/nonexistent/path/file.txt", "")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestUploadMedia_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"photo":{"id":"1","type":"photo","name":"photo.jpg"}}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "photo.jpg")
	if err := os.WriteFile(path, []byte("fake-jpeg-data"), 0644); err != nil {
		t.Fatalf("writefile: %v", err)
	}
	resp, err := c.UploadMedia(context.Background(), path, "custom-file-id")
	if err != nil {
		t.Fatalf("UploadMedia: %v", err)
	}
	if resp.Photo.ID != "1" {
		t.Errorf("Photo.ID = %q, want %q", resp.Photo.ID, "1")
	}
}

func TestUploadMedia_AutoUUID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"photo":{"id":"auto-uuid-123"}}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "doc.pdf")
	if err := os.WriteFile(path, []byte("pdf-data"), 0644); err != nil {
		t.Fatalf("writefile: %v", err)
	}
	_, err := c.UploadMedia(context.Background(), path, "")
	if err != nil {
		t.Fatalf("UploadMedia with auto UUID: %v", err)
	}
}

func TestUploadDocument_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"document":{"id":"2","name":"doc.pdf"}}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "doc.pdf")
	if err := os.WriteFile(path, []byte("pdf-content"), 0644); err != nil {
		t.Fatalf("writefile: %v", err)
	}
	resp, err := c.UploadDocument(context.Background(), path, "")
	if err != nil {
		t.Fatalf("UploadDocument: %v", err)
	}
	if resp.Document.ID != "2" {
		t.Errorf("Document.ID = %q, want %q", resp.Document.ID, "2")
	}
}

func TestUploadDocument_SizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":2}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, MaxUploadBytes: 5})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "big.pdf")
	if err := os.WriteFile(path, make([]byte, 100), 0644); err != nil {
		t.Fatalf("writefile: %v", err)
	}
	_, err = c.UploadDocument(context.Background(), path, "")
	if err == nil {
		t.Fatal("expected error for oversized document")
	}
}
