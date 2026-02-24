package app

import (
	"context"
	"fmt"
	neturl "net/url"
	"regexp"
	"strings"
)

const maxTGLinksPerMessage = 3

var (
	urlPattern       = regexp.MustCompile(`https?://[^\s]+`)
	pixivIDPattern   = regexp.MustCompile(`^\d+$`)
	yandeIDPattern   = regexp.MustCompile(`^\d+$`)
	twitterIDPattern = regexp.MustCompile(`^\d+$`)
	punctuationTrim  = ".,;:!?)]}>'\"\uFF0C\u3002\uFF01\uFF1F\u3001\uFF09\u3011\u300B"
)

type linkType string

const (
	linkPixiv   linkType = "pixiv"
	linkYande   linkType = "yande"
	linkTwitter linkType = "twitter"
)

type supportedLink struct {
	Type linkType
	ID   string
	URL  string
}

type ingestStats struct {
	FirstID    string
	Title      string
	Downloaded int
	Skipped    int
	Failed     int
}

func extractSupportedLinks(parts ...string) []supportedLink {
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		return nil
	}
	raw := urlPattern.FindAllString(text, -1)
	if len(raw) == 0 {
		return nil
	}

	out := make([]supportedLink, 0, len(raw))
	seen := map[string]struct{}{}
	for _, token := range raw {
		clean := strings.TrimRight(token, punctuationTrim)
		u, err := neturl.Parse(clean)
		if err != nil {
			continue
		}
		host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
		pathVal := strings.Trim(u.EscapedPath(), "/")
		segments := strings.Split(pathVal, "/")

		if host == "pixiv.net" {
			for i := 0; i+1 < len(segments); i++ {
				if segments[i] == "artworks" && pixivIDPattern.MatchString(segments[i+1]) {
					id := segments[i+1]
					key := string(linkPixiv) + ":" + id
					if _, ok := seen[key]; ok {
						break
					}
					seen[key] = struct{}{}
					out = append(out, supportedLink{Type: linkPixiv, ID: id, URL: clean})
					break
				}
			}
		}

		if host == "yande.re" && len(segments) >= 3 && segments[0] == "post" && segments[1] == "show" && yandeIDPattern.MatchString(segments[2]) {
			id := segments[2]
			key := string(linkYande) + ":" + id
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, supportedLink{Type: linkYande, ID: id, URL: clean})
		}

		if isTwitterHost(host) {
			username, id, ok := parseTwitterPath(segments)
			if !ok {
				continue
			}
			key := string(linkTwitter) + ":" + id
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, supportedLink{Type: linkTwitter, ID: id, URL: canonicalTwitterURL(username, id)})
		}
	}
	return out
}

func isTwitterHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	return host == "x.com" || host == "twitter.com" || host == "mobile.twitter.com"
}

func parseTwitterPath(parts []string) (username, id string, ok bool) {
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] != "status" || !twitterIDPattern.MatchString(parts[i+1]) {
			continue
		}
		id = parts[i+1]
		if i > 0 {
			username = normalizeTwitterUsername(parts[i-1])
		}
		return username, id, true
	}
	return "", "", false
}

func canonicalTwitterURL(username, tweetID string) string {
	username = normalizeTwitterUsername(username)
	if username == "" {
		return fmt.Sprintf("https://x.com/i/status/%s", tweetID)
	}
	return fmt.Sprintf("https://x.com/%s/status/%s", username, tweetID)
}

func normalizeTwitterUsername(username string) string {
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "@")
	return username
}

func (a *App) handleTGLinks(ctx context.Context, links []supportedLink) (*TGIngestResult, error) {
	if len(links) > maxTGLinksPerMessage {
		links = links[:maxTGLinksPerMessage]
	}
	var summaries []string
	var first *TGIngestResult
	for _, item := range links {
		var (
			res *TGIngestResult
			err error
		)
		switch item.Type {
		case linkPixiv:
			res, err = a.ingestPixivFromLink(ctx, item)
		case linkYande:
			res, err = a.ingestYandeFromLink(ctx, item)
		case linkTwitter:
			res, err = a.ingestTwitterFromLink(ctx, item)
		default:
			continue
		}
		if err != nil {
			summaries = append(summaries, fmt.Sprintf("%s %s 失败：%v", strings.ToUpper(string(item.Type)), item.ID, err))
			continue
		}
		if res != nil {
			if first == nil {
				first = res
			}
			if strings.TrimSpace(res.Summary) != "" {
				summaries = append(summaries, res.Summary)
			}
		}
	}
	if first == nil {
		return &TGIngestResult{Summary: strings.Join(summaries, "\n")}, nil
	}
	first.Summary = strings.Join(summaries, "\n")
	return first, nil
}
