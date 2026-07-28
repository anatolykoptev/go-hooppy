package hooppy

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
)

// UploadMedia uploads a photo or video file and returns the attachment metadata.
// fileID is a UUID identifying the upload; if empty, a random UUID v4 is generated.
func (c *Client) UploadMedia(ctx context.Context, path, fileID string) (*UploadMediaResponse, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("hooppy: read file: %w", err)
	}
	if fileID == "" {
		fileID = mustUUIDv4()
	}
	var resp UploadMediaResponse
	if err := c.doMultipart(ctx, pathUploadMedia, "file", filepath.Base(path), data, map[string]string{
		"file_id": fileID,
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UploadDocument uploads a document file (PDF, archive, audio, etc.).
func (c *Client) UploadDocument(ctx context.Context, path, fileID string) (*UploadDocumentResponse, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("hooppy: read file: %w", err)
	}
	if fileID == "" {
		fileID = mustUUIDv4()
	}
	var resp UploadDocumentResponse
	if err := c.doMultipart(ctx, pathUploadDocument, "file", filepath.Base(path), data, map[string]string{
		"file_id": fileID,
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// mustUUIDv4 generates a random UUID v4 string. Panics only if crypto/rand
// fails, which should not happen on a functioning system.
func mustUUIDv4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("hooppy: crypto/rand failed: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
