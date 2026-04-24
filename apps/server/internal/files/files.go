// Package files handles upload-time processing: MIME sniffing, EXIF
// stripping via re-encode, and image dimension probing. Keeps handler
// code focused on HTTP wiring.
package files

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"  // register GIF decoder
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"

	_ "golang.org/x/image/webp" // register WebP decoder for DecodeConfig / Decode
)

// MaxUploadSize is the wire-level cap on a single upload. The HTTP handler
// wraps the request body in http.MaxBytesReader so anything larger is
// rejected before we allocate on it.
const MaxUploadSize = 50 * 1024 * 1024 // 50 MiB

// Processed is the result of reading a multipart file, sniffing its type,
// and (for images) re-encoding to strip metadata.
type Processed struct {
	Body   []byte // bytes to persist to storage
	MIME   string // server-verified content type (NOT what the client sent)
	Width  int    // zero for non-images
	Height int    // zero for non-images
}

// Ingest reads `src` up to MaxUploadSize bytes, sniffs its real MIME type,
// and for supported image formats re-encodes to strip metadata. Non-image
// files are returned as-is. Always returns server-verified bytes.
func Ingest(src io.Reader, declaredFilename string) (*Processed, error) {
	raw, err := io.ReadAll(io.LimitReader(src, MaxUploadSize+1))
	if err != nil {
		return nil, fmt.Errorf("read upload: %w", err)
	}
	if int64(len(raw)) > MaxUploadSize {
		return nil, fmt.Errorf("upload exceeds %d bytes", MaxUploadSize)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty upload")
	}

	mime := http.DetectContentType(raw)

	// Re-encode images to drop EXIF, XMP, color profiles, etc. Stdlib
	// decoders read these sections but encoders never emit them back.
	switch {
	case strings.HasPrefix(mime, "image/jpeg"):
		return stripJPEG(raw)
	case strings.HasPrefix(mime, "image/png"):
		return stripPNG(raw)
	case strings.HasPrefix(mime, "image/gif"):
		// GIFs frequently contain animation; re-encoding would drop frames.
		// Keep GIFs intact; EXIF is rare in GIF so the privacy risk is low.
		cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
		if err == nil {
			return &Processed{Body: raw, MIME: mime, Width: cfg.Width, Height: cfg.Height}, nil
		}
		return &Processed{Body: raw, MIME: mime}, nil
	case strings.HasPrefix(mime, "image/webp"):
		cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
		if err == nil {
			return &Processed{Body: raw, MIME: mime, Width: cfg.Width, Height: cfg.Height}, nil
		}
		return &Processed{Body: raw, MIME: mime}, nil
	}

	// Sniffer sometimes returns "application/octet-stream" for text files
	// with unusual first bytes; the declared filename can help for .txt /
	// .md fallback. Not relied on for anything security-sensitive.
	_ = declaredFilename
	return &Processed{Body: raw, MIME: mime}, nil
}

func stripJPEG(raw []byte) (*Processed, error) {
	img, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		// Undecodable; pass through verbatim so the user still gets their file.
		return &Processed{Body: raw, MIME: "image/jpeg"}, nil
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return &Processed{Body: raw, MIME: "image/jpeg"}, nil
	}
	b := buf.Bytes()
	return &Processed{
		Body:   b,
		MIME:   "image/jpeg",
		Width:  img.Bounds().Dx(),
		Height: img.Bounds().Dy(),
	}, nil
}

func stripPNG(raw []byte) (*Processed, error) {
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return &Processed{Body: raw, MIME: "image/png"}, nil
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return &Processed{Body: raw, MIME: "image/png"}, nil
	}
	b := buf.Bytes()
	return &Processed{
		Body:   b,
		MIME:   "image/png",
		Width:  img.Bounds().Dx(),
		Height: img.Bounds().Dy(),
	}, nil
}

// StorageKey derives the canonical storage path for a (workspace, sha256)
// pair. Split into two-char shards so directory listings stay manageable
// on filesystems that slow down with >10k entries per dir.
func StorageKey(workspaceID int64, sha string) string {
	if len(sha) < 4 {
		return fmt.Sprintf("ws/%d/%s", workspaceID, sha)
	}
	return fmt.Sprintf("ws/%d/%s/%s/%s", workspaceID, sha[0:2], sha[2:4], sha)
}
