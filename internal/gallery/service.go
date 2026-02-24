package gallery

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"tyr-blog-img/internal/database"
)

type ObjectStore interface {
	PutObject(ctx context.Context, key string, data []byte, contentType string) error
	DeleteObject(ctx context.Context, key string) error
}

type Service struct {
	DB        *database.Client
	Store     ObjectStore
	Processor ImageProcessor

	muH sync.Mutex
	muV sync.Mutex
}

type StoreInput struct {
	ID           string
	Source       string
	SourceKey    string
	SourceURL    string
	SourcePostID string
	RawData      []byte
	PublishedAt  int64
	CollectedAt  int64
}

type StoreResult struct {
	Added       bool
	SkipReason  string
	Image       database.GalleryImage
	Counts      database.GalleryCounts
	ContentHash string
}

func NewService(db *database.Client, store ObjectStore, processor ImageProcessor) *Service {
	if processor == nil {
		processor = NewHybridWebPProcessor()
	}
	return &Service{
		DB:        db,
		Store:     store,
		Processor: processor,
	}
}

func (s *Service) StoreToGallery(ctx context.Context, in StoreInput) (StoreResult, error) {
	if s == nil || s.DB == nil || s.Store == nil || s.Processor == nil {
		return StoreResult{}, fmt.Errorf("gallery service not fully configured")
	}

	in.Source = strings.TrimSpace(in.Source)
	in.SourceKey = strings.TrimSpace(in.SourceKey)
	in.SourceURL = strings.TrimSpace(in.SourceURL)
	in.SourcePostID = strings.TrimSpace(in.SourcePostID)
	in.ID = strings.TrimSpace(in.ID)

	if in.Source == "" {
		in.Source = "unknown"
	}
	if in.SourceKey == "" {
		return StoreResult{}, fmt.Errorf("source_key is required")
	}
	if len(in.RawData) == 0 {
		return StoreResult{}, fmt.Errorf("raw image data is empty")
	}

	// 1) Source-level blocklist check (before heavy work)
	blocked, err := s.DB.IsBlocked(ctx, in.SourceKey)
	if err != nil {
		return StoreResult{}, err
	}
	if blocked {
		return StoreResult{SkipReason: "blocked_source"}, nil
	}

	// 2) Source-level dedupe check (before decode/transcode)
	existsSource, err := s.DB.ExistsGallerySourceKey(ctx, in.SourceKey)
	if err != nil {
		return StoreResult{}, err
	}
	if existsSource {
		return StoreResult{SkipReason: "duplicate_source"}, nil
	}

	// 3) Prepare image (hash + dimensions + orientation + webp bytes)
	prepared, err := s.Processor.Prepare(ctx, in.RawData)
	if err != nil {
		return StoreResult{}, err
	}

	// 4) Content-level dedupe (after bytes/hash available)
	existsHash, err := s.DB.ExistsGallerySHA256(ctx, prepared.SHA256)
	if err != nil {
		return StoreResult{}, err
	}
	if existsHash {
		return StoreResult{SkipReason: "duplicate_hash", ContentHash: prepared.SHA256}, nil
	}

	// 5) Per-orientation critical section (single instance MVP)
	lock := s.orientationLock(prepared.Orientation)
	lock.Lock()
	defer lock.Unlock()

	// Recheck in critical section to reduce race window.
	existsSource, err = s.DB.ExistsGallerySourceKey(ctx, in.SourceKey)
	if err != nil {
		return StoreResult{}, err
	}
	if existsSource {
		return StoreResult{SkipReason: "duplicate_source_race", ContentHash: prepared.SHA256}, nil
	}
	existsHash, err = s.DB.ExistsGallerySHA256(ctx, prepared.SHA256)
	if err != nil {
		return StoreResult{}, err
	}
	if existsHash {
		return StoreResult{SkipReason: "duplicate_hash_race", ContentHash: prepared.SHA256}, nil
	}

	// 6) Allocate seq as late as possible (after dedupe + prepare succeeds)
	seq, err := s.DB.NextGallerySeq(ctx, prepared.Orientation)
	if err != nil {
		return StoreResult{}, err
	}
	r2Key := fmt.Sprintf("ri/%s/%d.webp", prepared.Orientation, seq)

	// 7) Upload to R2 first; if this fails, no seq is persisted in D1.
	if err := s.Store.PutObject(ctx, r2Key, prepared.WebPBytes, prepared.ContentType); err != nil {
		return StoreResult{}, fmt.Errorf("upload r2 %s: %w", r2Key, err)
	}

	collectedAt := in.CollectedAt
	if collectedAt <= 0 {
		collectedAt = time.Now().Unix()
	}
	img := database.GalleryImage{
		ID:           pickID(in.ID, in.SourceKey, prepared.SHA256),
		Source:       in.Source,
		SourceKey:    in.SourceKey,
		SourceURL:    in.SourceURL,
		SourcePostID: in.SourcePostID,
		SHA256:       prepared.SHA256,
		Orientation:  prepared.Orientation,
		Seq:          seq,
		R2Key:        r2Key,
		Width:        prepared.Width,
		Height:       prepared.Height,
		Bytes:        prepared.Bytes,
		MimeType:     prepared.ContentType,
		PublishedAt:  in.PublishedAt,
		CollectedAt:  collectedAt,
		Status:       "active",
	}

	// 8) Persist D1 record. If this fails, try cleanup R2 object to avoid orphans.
	if err := s.DB.InsertGalleryImage(ctx, img); err != nil {
		_ = s.Store.DeleteObject(context.Background(), r2Key)
		return StoreResult{}, fmt.Errorf("insert gallery image: %w", err)
	}

	counts, err := s.DB.CountGalleryActive(ctx)
	if err != nil {
		return StoreResult{
			Added:       true,
			Image:       img,
			ContentHash: prepared.SHA256,
		}, nil
	}

	return StoreResult{
		Added:       true,
		Image:       img,
		Counts:      counts,
		ContentHash: prepared.SHA256,
	}, nil
}

func (s *Service) orientationLock(orientation string) *sync.Mutex {
	if strings.EqualFold(strings.TrimSpace(orientation), "v") {
		return &s.muV
	}
	return &s.muH
}

func pickID(preferred, sourceKey, sha string) string {
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		return preferred
	}
	sourceKey = strings.TrimSpace(sourceKey)
	if sourceKey != "" {
		return sourceKey
	}
	sha = strings.TrimSpace(sha)
	if len(sha) > 24 {
		return "sha256_" + sha[:24]
	}
	if sha != "" {
		return "sha256_" + sha
	}
	return fmt.Sprintf("img_%d", time.Now().UnixNano())
}
