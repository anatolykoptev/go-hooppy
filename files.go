package hooppy

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// UploadMedia uploads a photo or video file and returns the attachment metadata.
// fileID is a UUID identifying the upload; if empty, a random UUID v4 is generated.
// Files larger than Config.MaxUploadBytes (default 50 MB) are rejected before reading.
func (c *Client) UploadMedia(ctx context.Context, path, fileID string) (*UploadMediaResponse, error) {
	f, size, err := openFileForUpload(path, c.maxUploadBytes)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fileID == "" {
		id, err := uuidv4()
		if err != nil {
			return nil, err
		}
		fileID = id
	}
	var resp UploadMediaResponse
	if err := c.doMultipartStream(ctx, pathUploadMedia, "file", filepath.Base(path), size, f, map[string]string{
		"file_id": fileID,
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UploadDocument uploads a document file (PDF, archive, audio, etc.).
// Files larger than Config.MaxUploadBytes (default 50 MB) are rejected before reading.
func (c *Client) UploadDocument(ctx context.Context, path, fileID string) (*UploadDocumentResponse, error) {
	f, size, err := openFileForUpload(path, c.maxUploadBytes)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fileID == "" {
		id, err := uuidv4()
		if err != nil {
			return nil, err
		}
		fileID = id
	}
	var resp UploadDocumentResponse
	if err := c.doMultipartStream(ctx, pathUploadDocument, "file", filepath.Base(path), size, f, map[string]string{
		"file_id": fileID,
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// openFileForUpload opens a file for reading, validates it is a regular file,
// and enforces the max size limit. Returns the open file handle, its size, or an error.
func openFileForUpload(path string, maxBytes int64) (*os.File, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("hooppy: stat file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("hooppy: not a regular file: %s", path)
	}
	if info.Size() > maxBytes {
		return nil, 0, fmt.Errorf("hooppy: file %s (%d bytes) exceeds max upload size %d bytes", path, info.Size(), maxBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("hooppy: open file: %w", err)
	}
	return f, info.Size(), nil
}

// uuidv4 generates a random UUID v4 string. Returns an error if crypto/rand
// fails (entropy starvation); the caller should propagate the error rather
// than crashing the process.
func uuidv4() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", fmt.Errorf("hooppy: crypto/rand failed: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
