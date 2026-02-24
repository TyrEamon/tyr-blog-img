package gallery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	_ "image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	_ "golang.org/x/image/webp"
)

type PreparedImage struct {
	WebPBytes    []byte
	SHA256       string
	Width        int
	Height       int
	Orientation  string
	Bytes        int64
	ContentType  string
	OriginalMIME string
}

type ImageProcessor interface {
	Prepare(ctx context.Context, data []byte) (PreparedImage, error)
}

type HybridWebPProcessor struct {
	CWebPBinary     string
	Quality         int
	Method          int
	PassThroughWebP bool
}

func NewHybridWebPProcessor() *HybridWebPProcessor {
	return &HybridWebPProcessor{
		CWebPBinary:     "cwebp",
		Quality:         84,
		Method:          4,
		PassThroughWebP: true,
	}
}

// StrictWebPProcessor remains available for debugging/manual pipelines.
// It validates image metadata and only accepts WebP input.
type StrictWebPProcessor struct{}

func (p *StrictWebPProcessor) Prepare(_ context.Context, data []byte) (PreparedImage, error) {
	if len(data) == 0 {
		return PreparedImage{}, fmt.Errorf("empty image data")
	}

	hash := sha256.Sum256(data)
	sha := hex.EncodeToString(hash[:])
	mime := strings.ToLower(strings.TrimSpace(http.DetectContentType(data)))

	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return PreparedImage{}, fmt.Errorf("decode image config: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return PreparedImage{}, fmt.Errorf("invalid image size")
	}

	orientation := "h"
	if cfg.Height > cfg.Width {
		orientation = "v"
	}

	if format != "webp" && !strings.Contains(mime, "webp") {
		return PreparedImage{}, fmt.Errorf("non-webp input is not supported yet; got format=%s mime=%s", format, mime)
	}

	return PreparedImage{
		WebPBytes:    data,
		SHA256:       sha,
		Width:        cfg.Width,
		Height:       cfg.Height,
		Orientation:  orientation,
		Bytes:        int64(len(data)),
		ContentType:  "image/webp",
		OriginalMIME: mime,
	}, nil
}

func (p *HybridWebPProcessor) Prepare(ctx context.Context, data []byte) (PreparedImage, error) {
	if len(data) == 0 {
		return PreparedImage{}, fmt.Errorf("empty image data")
	}
	if p == nil {
		p = NewHybridWebPProcessor()
	}

	mime := strings.ToLower(strings.TrimSpace(http.DetectContentType(data)))
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return PreparedImage{}, fmt.Errorf("decode image config: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return PreparedImage{}, fmt.Errorf("invalid image size")
	}

	orientation := "h"
	if cfg.Height > cfg.Width {
		orientation = "v"
	}

	webpBytes := data
	if !(p.PassThroughWebP && (format == "webp" || strings.Contains(mime, "webp"))) {
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return PreparedImage{}, fmt.Errorf("decode image: %w", err)
		}
		webpBytes, err = p.encodeWithCWebP(ctx, decoded)
		if err != nil {
			return PreparedImage{}, err
		}

		// Validate and refresh dimensions from the actual stored payload.
		outCfg, _, err := image.DecodeConfig(bytes.NewReader(webpBytes))
		if err != nil {
			return PreparedImage{}, fmt.Errorf("decode webp output config: %w", err)
		}
		cfg = outCfg
		if cfg.Height > cfg.Width {
			orientation = "v"
		} else {
			orientation = "h"
		}
	}

	hash := sha256.Sum256(webpBytes)
	sha := hex.EncodeToString(hash[:])

	return PreparedImage{
		WebPBytes:    webpBytes,
		SHA256:       sha,
		Width:        cfg.Width,
		Height:       cfg.Height,
		Orientation:  orientation,
		Bytes:        int64(len(webpBytes)),
		ContentType:  "image/webp",
		OriginalMIME: mime,
	}, nil
}

func (p *HybridWebPProcessor) encodeWithCWebP(ctx context.Context, img image.Image) ([]byte, error) {
	bin := "cwebp"
	quality := 84
	method := 4
	if p != nil {
		if strings.TrimSpace(p.CWebPBinary) != "" {
			bin = strings.TrimSpace(p.CWebPBinary)
		}
		if p.Quality >= 0 && p.Quality <= 100 {
			quality = p.Quality
		}
		if p.Method >= 0 && p.Method <= 6 {
			method = p.Method
		}
	}

	tmpDir, err := os.MkdirTemp("", "tyr-blog-img-webp-*")
	if err != nil {
		return nil, fmt.Errorf("mktemp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	inPath := filepath.Join(tmpDir, "input.png")
	outPath := filepath.Join(tmpDir, "output.webp")

	inFile, err := os.Create(inPath)
	if err != nil {
		return nil, fmt.Errorf("create temp png: %w", err)
	}
	if err := png.Encode(inFile, img); err != nil {
		_ = inFile.Close()
		return nil, fmt.Errorf("encode temp png: %w", err)
	}
	if err := inFile.Close(); err != nil {
		return nil, fmt.Errorf("close temp png: %w", err)
	}

	args := []string{
		"-quiet",
		"-mt",
		"-q", strconv.Itoa(quality),
		"-m", strconv.Itoa(method),
		inPath,
		"-o", outPath,
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("cwebp failed: %s", msg)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("read webp output: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("cwebp produced empty output")
	}
	return data, nil
}
