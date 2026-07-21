package transfer

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"testing"
)

func TestMultipartEnvelopeBuildsValidSingleFileBody(t *testing.T) {
	prefix, suffix, contentType, err := multipartEnvelope("movie.mp4")
	if err != nil {
		t.Fatalf("multipartEnvelope failed: %v", err)
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content type failed: %v", err)
	}
	if mediaType != "multipart/form-data" || params["boundary"] == "" {
		t.Fatalf("unexpected content type: %q", contentType)
	}

	fileContent := []byte("file payload")
	body := append(append(append([]byte(nil), prefix...), fileContent...), suffix...)
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	part, err := reader.NextPart()
	if err != nil {
		t.Fatalf("read multipart part failed: %v", err)
	}
	if part.FormName() != "file" || part.FileName() != "movie.mp4" {
		t.Fatalf("unexpected multipart part: form=%q file=%q", part.FormName(), part.FileName())
	}
	got, err := io.ReadAll(part)
	if err != nil {
		t.Fatalf("read part content failed: %v", err)
	}
	if !bytes.Equal(got, fileContent) {
		t.Fatalf("part content mismatch")
	}
	if _, err := reader.NextPart(); err != io.EOF {
		t.Fatalf("expected EOF after single file part, got %v", err)
	}
}
