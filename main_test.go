package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSafeFilename(t *testing.T) {
	tests := map[string]string{
		"photo.jpg":             "photo.jpg",
		"../../secret.txt":      "secret.txt",
		`..\\..\\windows.exe`:   "windows.exe",
		"bad\x00:name.txt":      "badname.txt",
		"  holiday photo.jpg  ": "holiday photo.jpg",
	}
	for input, want := range tests {
		if got := safeFilename(input); got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestUploadMultipleAndRenameCollision(t *testing.T) {
	dir := t.TempDir()
	s := &server{uploadDir: dir, maxBytes: 1 << 20}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, content := range []string{"first", "second"} {
		part, err := w.CreateFormFile("files", "same.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.upload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response uploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Files) != 2 || response.Files[0].SavedAs != "same.txt" || response.Files[1].SavedAs != "same (1).txt" {
		t.Fatalf("unexpected response: %+v", response)
	}
	for _, name := range []string{"same.txt", "same (1).txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUploadOverLimitCleansUp(t *testing.T) {
	dir := t.TempDir()
	s := &server{uploadDir: dir, maxBytes: 7}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, file := range []struct{ name, content string }{{"one.txt", "1234"}, {"two.txt", "5678"}} {
		part, err := w.CreateFormFile("files", file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(file.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.upload(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("upload directory contains %d files after rejected request", len(entries))
	}
}
