package app

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const twitterAuthorStatePrefix = "twitter_author_last_"

type twitterRSSFeed struct {
	Channel struct {
		Items []twitterRSSItem `xml:"item"`
	} `xml:"channel"`
}

type twitterRSSItem struct {
	Title string `xml:"title"`
	Link  string `xml:"link"`
	GUID  string `xml:"guid"`
}

type twitterAuthorCandidate struct {
	Link   supportedLink
	ID     int64
	Source string
}

func (a *App) StartTwitterAuthorCrawler(ctx context.Context) {
	if a.Cfg == nil || !a.Cfg.HasTwitterAuthorCrawler() {
		log.Println("Twitter author crawler disabled")
		return
	}
	go func() {
		a.crawlTwitterAuthorsOnce(ctx)
		ticker := time.NewTicker(time.Duration(maxInt(a.Cfg.TwitterAuthorIntervalMin, 60)) * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.crawlTwitterAuthorsOnce(ctx)
			}
		}
	}()
}

func (a *App) crawlTwitterAuthorsOnce(ctx context.Context) {
	log.Printf("Twitter author crawl started (users=%d, sources=%d)", len(a.Cfg.TwitterAuthorUsers), len(a.Cfg.TwitterRSSSources))
	for _, rawUser := range a.Cfg.TwitterAuthorUsers {
		if ctx.Err() != nil {
			return
		}
		user := normalizeTwitterUsername(rawUser)
		if user == "" {
			continue
		}
		if err := a.crawlTwitterAuthorUser(ctx, user); err != nil {
			log.Printf("Twitter author crawl failed user=%s err=%v", user, err)
		}
		time.Sleep(1500 * time.Millisecond)
	}
	log.Println("Twitter author crawl finished")
}

func (a *App) crawlTwitterAuthorUser(ctx context.Context, user string) error {
	stateKey := twitterAuthorStatePrefix + strings.ToLower(user)
	lastValue, ok, err := a.DB.GetCrawlerState(ctx, stateKey)
	if err != nil {
		return fmt.Errorf("get crawler state: %w", err)
	}
	lastID, _ := strconv.ParseInt(strings.TrimSpace(lastValue), 10, 64)
	if !ok {
		lastID = 0
	}

	links, rssURL, err := a.fetchTwitterAuthorLinks(ctx, user)
	if err != nil {
		return err
	}
	if len(links) == 0 {
		return nil
	}

	candidates := make([]twitterAuthorCandidate, 0, len(links))
	for _, link := range links {
		idNum, parseErr := strconv.ParseInt(link.ID, 10, 64)
		if parseErr != nil || idNum <= 0 {
			continue
		}
		if lastID > 0 && idNum <= lastID {
			continue
		}
		candidates = append(candidates, twitterAuthorCandidate{Link: link, ID: idNum, Source: rssURL})
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	if limit := a.Cfg.TwitterAuthorFetchLimit; limit > 0 && len(candidates) > limit {
		candidates = candidates[len(candidates)-limit:]
	}

	highestSuccessID := lastID
	for _, c := range candidates {
		if ctx.Err() != nil {
			return nil
		}
		if _, err := a.ingestTwitterFromLink(ctx, c.Link); err != nil {
			log.Printf("Twitter author ingest failed user=%s tweet=%s err=%v", user, c.Link.ID, err)
			continue
		}
		if c.ID > highestSuccessID {
			highestSuccessID = c.ID
		}
		time.Sleep(1200 * time.Millisecond)
	}
	if highestSuccessID > lastID {
		if err := a.DB.SetCrawlerState(ctx, stateKey, strconv.FormatInt(highestSuccessID, 10)); err != nil {
			log.Printf("Twitter author state update failed user=%s err=%v", user, err)
		}
	}
	return nil
}

func (a *App) fetchTwitterAuthorLinks(ctx context.Context, user string) ([]supportedLink, string, error) {
	var errs []string
	for _, source := range a.Cfg.TwitterRSSSources {
		feedURL := buildTwitterRSSURL(source, user)
		items, err := fetchTwitterRSSItems(ctx, feedURL)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", feedURL, err))
			continue
		}
		links := extractTwitterLinksFromRSSItems(items)
		if len(links) == 0 {
			errs = append(errs, fmt.Sprintf("%s: no twitter links", feedURL))
			continue
		}
		return links, feedURL, nil
	}
	if len(errs) == 0 {
		return nil, "", fmt.Errorf("no rss sources configured")
	}
	return nil, "", fmt.Errorf("all rss sources failed: %s", strings.Join(errs, " | "))
}

func buildTwitterRSSURL(template, user string) string {
	t := strings.TrimSpace(template)
	u := neturl.PathEscape(normalizeTwitterUsername(user))
	if strings.Contains(t, "{user}") {
		return strings.ReplaceAll(t, "{user}", u)
	}
	if strings.Contains(t, "%s") {
		return fmt.Sprintf(t, u)
	}
	return strings.TrimRight(t, "/") + "/" + u
}

func fetchTwitterRSSItems(ctx context.Context, feedURL string) ([]twitterRSSItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tyr-blog-img/1.0")
	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var feed twitterRSSFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, err
	}
	return feed.Channel.Items, nil
}

func extractTwitterLinksFromRSSItems(items []twitterRSSItem) []supportedLink {
	out := make([]supportedLink, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		for _, link := range extractSupportedLinks(item.Link, item.GUID, item.Title) {
			if link.Type != linkTwitter {
				continue
			}
			key := string(link.Type) + ":" + link.ID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, link)
		}
	}
	return out
}
