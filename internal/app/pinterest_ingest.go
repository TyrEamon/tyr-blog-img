package app

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	neturl "net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"tyr-blog-img/internal/gallery"
)

const (
	pinterestPageTimeout     = 30 * time.Second
	pinterestDownloadTimeout = 90 * time.Second
	pinterestDownloadRetries = 2
	pinterestRetryBackoff    = 1500 * time.Millisecond
	pinterestMaxPageBytes    = 8 << 20
)

var pinterestImageURLPattern = regexp.MustCompile(`https?://i\.pinimg\.com/[^\s"'<>\\]+?\.(?:jpg|jpeg|png|webp)(?:\?[^\s"'<>\\]+)?`)

func (a *App) ingestPinterestFromLink(ctx context.Context, item supportedLink) (*TGIngestResult, error) {
	pin, err := fetchPinterestPin(ctx, item.URL)
	if err != nil {
		return nil, err
	}
	if pin.ID == "" {
		return nil, fmt.Errorf("pinterest pin id not found")
	}
	if pin.ImageURL == "" {
		return nil, fmt.Errorf("pinterest image not found")
	}

	data, err := downloadWithHeadersRetry(ctx, pin.ImageURL, pin.SourceURL, pinterestDownloadTimeout, pinterestDownloadRetries, pinterestRetryBackoff)
	if err != nil {
		return nil, fmt.Errorf("pinterest image download: %w", err)
	}

	sourceKey := "pinterest_" + pin.ID
	storeRes, err := a.Gallery.StoreToGallery(ctx, gallery.StoreInput{
		Source:       "pinterest",
		SourceKey:    sourceKey,
		SourceURL:    pin.SourceURL,
		SourcePostID: pin.ID,
		RawData:      data,
		CollectedAt:  time.Now().Unix(),
	})
	if err != nil {
		return nil, err
	}

	status := "skipped"
	added := 0
	skipped := 1
	if storeRes.Added {
		status = fmt.Sprintf("stored %s/%d", storeRes.Image.Orientation, storeRes.Image.Seq)
		added = 1
		skipped = 0
	} else if strings.TrimSpace(storeRes.SkipReason) != "" {
		status = "skipped: " + strings.TrimSpace(storeRes.SkipReason)
	}

	return &TGIngestResult{
		ID:        sourceKey,
		Title:     "Pinterest/" + pin.ID,
		SourceURL: pin.SourceURL,
		Summary:   fmt.Sprintf("Pinterest %s done: +%d, skipped %d (%s)", pin.ID, added, skipped, status),
	}, nil
}

type pinterestPin struct {
	ID        string
	SourceURL string
	ImageURL  string
}

func fetchPinterestPin(ctx context.Context, rawURL string) (pinterestPin, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return pinterestPin{}, fmt.Errorf("pinterest url is empty")
	}

	page, finalURL, err := fetchPinterestPage(ctx, rawURL)
	if err != nil {
		return pinterestPin{}, err
	}

	pinID := extractPinterestPinID(finalURL)
	if pinID == "" {
		pinID = extractPinterestPinID(rawURL)
	}
	if pinID == "" {
		pinID = extractPinterestPinID(page)
	}

	candidates := extractPinterestImageCandidates(page)
	imageURL, err := choosePinterestImageURL(ctx, candidates, finalURL)
	if err != nil {
		return pinterestPin{}, err
	}

	sourceURL := canonicalPinterestURL(pinID)
	if sourceURL == "" {
		sourceURL = finalURL
	}
	return pinterestPin{
		ID:        pinID,
		SourceURL: sourceURL,
		ImageURL:  imageURL,
	}, nil
}

func fetchPinterestPage(ctx context.Context, rawURL string) (body string, finalURL string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}
	setPinterestHeaders(req, "https://www.pinterest.com/")

	client := &http.Client{Timeout: pinterestPageTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", resp.Request.URL.String(), fmt.Errorf("pinterest status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, pinterestMaxPageBytes))
	if err != nil {
		return "", resp.Request.URL.String(), err
	}
	return string(data), resp.Request.URL.String(), nil
}

func setPinterestHeaders(req *http.Request, referer string) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ja,en-US;q=0.9,en;q=0.8,zh-CN;q=0.7,zh;q=0.6")
	if strings.TrimSpace(referer) != "" {
		req.Header.Set("Referer", referer)
	}
}

func extractPinterestPinID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if u, err := neturl.Parse(raw); err == nil {
		segments := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
		if id, ok := parsePinterestPath(segments); ok {
			return id
		}
	}
	re := regexp.MustCompile(`/pin/(\d+)`)
	if m := re.FindStringSubmatch(raw); len(m) >= 2 {
		return m[1]
	}
	return ""
}

func canonicalPinterestURL(pinID string) string {
	pinID = strings.TrimSpace(pinID)
	if pinID == "" {
		return ""
	}
	return fmt.Sprintf("https://www.pinterest.com/pin/%s/", pinID)
}

func extractPinterestImageCandidates(page string) []string {
	var candidates []string
	for _, variant := range pinterestTextVariants(page) {
		candidates = append(candidates, pinterestImageURLPattern.FindAllString(variant, -1)...)
	}

	out := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, item := range candidates {
		item = cleanPinterestURL(item)
		if item == "" || !strings.Contains(item, "://i.pinimg.com/") {
			continue
		}
		key := strings.Split(item, "#")[0]
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func pinterestTextVariants(text string) []string {
	unescaped := html.UnescapeString(text)
	slashUnescaped := strings.NewReplacer(`\/`, `/`, `\u002F`, `/`, `\u002f`, `/`).Replace(unescaped)
	if slashUnescaped == text {
		return []string{text}
	}
	return []string{text, unescaped, slashUnescaped}
}

func cleanPinterestURL(raw string) string {
	raw = html.UnescapeString(strings.TrimSpace(raw))
	raw = strings.NewReplacer(`\/`, `/`, `\u002F`, `/`, `\u002f`, `/`).Replace(raw)
	raw = strings.TrimRight(raw, `\`)
	return raw
}

func choosePinterestImageURL(ctx context.Context, candidates []string, referer string) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("no pinterest image candidates")
	}

	expanded := make([]string, 0, len(candidates)*16)
	seen := make(map[string]struct{}, len(candidates)*16)
	for _, candidate := range candidates {
		for _, url := range expandPinterestImageURL(candidate) {
			if _, ok := seen[url]; ok {
				continue
			}
			seen[url] = struct{}{}
			expanded = append(expanded, url)
		}
	}
	sortPinterestImageURLs(expanded, candidates)

	var lastStatus string
	for i, imageURL := range expanded {
		if i >= 48 {
			break
		}
		ok, status, contentType := probePinterestImage(ctx, imageURL, referer)
		lastStatus = fmt.Sprintf("%d %s", status, contentType)
		if ok {
			return imageURL, nil
		}
	}
	return "", fmt.Errorf("no downloadable pinterest image found (last probe: %s)", strings.TrimSpace(lastStatus))
}

func expandPinterestImageURL(raw string) []string {
	raw = cleanPinterestURL(raw)
	u, err := neturl.Parse(raw)
	if err != nil || strings.ToLower(u.Hostname()) != "i.pinimg.com" {
		return []string{raw}
	}

	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) < 2 {
		return []string{raw}
	}

	size := parts[0]
	sizes := []string{"originals", "1200x", "736x", "564x"}
	if containsString(sizes, size) {
		sizes = append([]string{size}, removeString(sizes, size)...)
	}

	currentExt := strings.ToLower(path.Ext(parts[len(parts)-1]))
	exts := []string{}
	if currentExt != "" {
		exts = append(exts, currentExt)
	}
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		if !containsString(exts, ext) {
			exts = append(exts, ext)
		}
	}

	out := make([]string, 0, len(sizes)*len(exts))
	for _, nextSize := range sizes {
		for _, ext := range exts {
			nextParts := append([]string{nextSize}, parts[1:]...)
			nextParts[len(nextParts)-1] = strings.TrimSuffix(nextParts[len(nextParts)-1], path.Ext(nextParts[len(nextParts)-1])) + ext
			nextURL := *u
			nextURL.Path = "/" + strings.Join(nextParts, "/")
			nextURL.RawQuery = ""
			out = append(out, nextURL.String())
		}
	}
	return out
}

func sortPinterestImageURLs(urls []string, sourceCandidates []string) {
	groupCounts := map[string]int{}
	for _, candidate := range sourceCandidates {
		groupCounts[pinterestImageSignature(candidate)]++
	}
	sort.SliceStable(urls, func(i, j int) bool {
		return groupCounts[pinterestImageSignature(urls[i])]*1000+pinterestImageScore(urls[i]) >
			groupCounts[pinterestImageSignature(urls[j])]*1000+pinterestImageScore(urls[j])
	})
}

func pinterestImageSignature(raw string) string {
	u, err := neturl.Parse(cleanPinterestURL(raw))
	if err != nil || strings.ToLower(u.Hostname()) != "i.pinimg.com" {
		return raw
	}
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) < 2 {
		return strings.ToLower(u.EscapedPath())
	}
	tail := append([]string{}, parts[1:]...)
	tail[len(tail)-1] = strings.TrimSuffix(tail[len(tail)-1], path.Ext(tail[len(tail)-1]))
	return strings.ToLower(strings.Join(tail, "/"))
}

func pinterestImageScore(raw string) int {
	raw = strings.ToLower(cleanPinterestURL(raw))
	score := 0
	switch {
	case strings.Contains(raw, "/originals/"):
		score += 500
	case strings.Contains(raw, "/1200x/"):
		score += 400
	case strings.Contains(raw, "/736x/"):
		score += 300
	case strings.Contains(raw, "/564x/"):
		score += 200
	}
	if strings.HasSuffix(raw, ".png") {
		score += 10
	} else if strings.HasSuffix(raw, ".jpg") || strings.HasSuffix(raw, ".jpeg") {
		score += 8
	} else if strings.HasSuffix(raw, ".webp") {
		score += 5
	}
	if strings.Contains(raw, "/avatars/") || strings.Contains(raw, "/75x75") || strings.Contains(raw, "/60x60") || strings.Contains(raw, "/30x30") {
		score -= 1000
	}
	return score
}

func probePinterestImage(ctx context.Context, imageURL, referer string) (bool, int, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, imageURL, nil)
	if err != nil {
		return false, 0, ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	if strings.TrimSpace(referer) != "" {
		req.Header.Set("Referer", referer)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, 0, ""
	}
	defer resp.Body.Close()

	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	return resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.HasPrefix(contentType, "image/"), resp.StatusCode, contentType
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func removeString(items []string, value string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item != value {
			out = append(out, item)
		}
	}
	return out
}
