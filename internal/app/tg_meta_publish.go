package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"tyr-blog-img/internal/database"
)

var countsAssignPattern = regexp.MustCompile(`(?:var|const|let)\s+counts\s*=\s*\{[^;]*\}\s*;`)

type metadataPublisherStore interface {
	GetObject(ctx context.Context, key string) ([]byte, string, error)
	PutObjectWithCacheControl(ctx context.Context, key string, data []byte, contentType, cacheControl string) error
}

func parseTGCommand(text string) (cmd string, args string) {
	text = strings.TrimSpace(text)
	if text == "" || !strings.HasPrefix(text, "/") {
		return "", ""
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", ""
	}
	token := strings.TrimPrefix(fields[0], "/")
	if token == "" {
		return "", ""
	}
	if i := strings.IndexByte(token, '@'); i >= 0 {
		token = token[:i]
	}
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return "", ""
	}
	if len(fields) > 1 {
		args = strings.TrimSpace(strings.Join(fields[1:], " "))
	}
	return token, args
}

func (a *App) handleTGCommand(ctx context.Context, cmd, args string) (*TGIngestResult, error) {
	_ = args
	switch strings.ToLower(strings.TrimSpace(cmd)) {
	case "updata", "update":
		return a.handleTGUpdateMetadata(ctx)
	case "start", "help":
		return &TGIngestResult{Summary: "Commands:\n/updata - refresh counts.json and random*.js counts from D1 seq"}, nil
	default:
		return &TGIngestResult{Summary: fmt.Sprintf("Unknown command: /%s", strings.TrimSpace(cmd))}, nil
	}
}

func (a *App) handleTGUpdateMetadata(ctx context.Context) (*TGIngestResult, error) {
	if a == nil || a.DB == nil || a.Gallery == nil || a.Gallery.Store == nil {
		return &TGIngestResult{Summary: "metadata publisher is not initialized"}, nil
	}
	store, ok := a.Gallery.Store.(metadataPublisherStore)
	if !ok {
		return &TGIngestResult{Summary: "current object store does not support metadata publish"}, nil
	}

	counts, err := a.currentCountsBySeq(ctx)
	if err != nil {
		return nil, err
	}

	updated := make([]string, 0, 3)

	// counts.json
	countsJSON, err := json.Marshal(counts)
	if err != nil {
		return nil, err
	}
	if err := store.PutObjectWithCacheControl(ctx, "counts.json", countsJSON, "application/json; charset=utf-8", "public, max-age=30"); err != nil {
		return nil, fmt.Errorf("upload counts.json: %w", err)
	}
	updated = append(updated, "counts.json")

	// random.js
	if ok, err := a.patchAndUploadRandomScript(ctx, store, "random.js", counts); err != nil {
		return nil, err
	} else if ok {
		updated = append(updated, "random.js")
	}

	// random-img-only.js (optional but recommended for your blog)
	if ok, err := a.patchAndUploadRandomScript(ctx, store, "random-img-only.js", counts); err != nil {
		// do not fail the whole command if optional script is missing
		updated = append(updated, "random-img-only.js(skip:"+err.Error()+")")
	} else if ok {
		updated = append(updated, "random-img-only.js")
	}

	return &TGIngestResult{
		Summary: fmt.Sprintf("metadata updated\ncounts: h=%d v=%d\nfiles: %s", counts.H, counts.V, strings.Join(updated, ", ")),
	}, nil
}

func (a *App) currentCountsBySeq(ctx context.Context) (database.GalleryCounts, error) {
	if a == nil || a.DB == nil {
		return database.GalleryCounts{}, fmt.Errorf("db not initialized")
	}
	nextH, err := a.DB.NextGallerySeq(ctx, "h")
	if err != nil {
		return database.GalleryCounts{}, fmt.Errorf("calc h next seq: %w", err)
	}
	nextV, err := a.DB.NextGallerySeq(ctx, "v")
	if err != nil {
		return database.GalleryCounts{}, fmt.Errorf("calc v next seq: %w", err)
	}
	counts := database.GalleryCounts{}
	if nextH > 1 {
		counts.H = nextH - 1
	}
	if nextV > 1 {
		counts.V = nextV - 1
	}
	return counts, nil
}

func (a *App) patchAndUploadRandomScript(ctx context.Context, store metadataPublisherStore, key string, counts database.GalleryCounts) (bool, error) {
	data, _, err := store.GetObject(ctx, key)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", key, err)
	}
	patched, err := patchRandomScriptCounts(data, counts)
	if err != nil {
		return false, fmt.Errorf("patch %s: %w", key, err)
	}
	if err := store.PutObjectWithCacheControl(ctx, key, patched, "application/javascript; charset=utf-8", "public, max-age=60"); err != nil {
		return false, fmt.Errorf("upload %s: %w", key, err)
	}
	return true, nil
}

func patchRandomScriptCounts(src []byte, counts database.GalleryCounts) ([]byte, error) {
	if len(src) == 0 {
		return nil, fmt.Errorf("empty script")
	}
	lineBytes, err := json.Marshal(counts)
	if err != nil {
		return nil, err
	}
	replacement := []byte("var counts = " + string(lineBytes) + ";")
	if !countsAssignPattern.Match(src) {
		return nil, fmt.Errorf("counts assignment not found")
	}
	out := countsAssignPattern.ReplaceAll(src, replacement)
	return out, nil
}
