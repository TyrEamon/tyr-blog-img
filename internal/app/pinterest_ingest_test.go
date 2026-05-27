package app

import "testing"

func TestExtractSupportedLinksPinterest(t *testing.T) {
	links := extractSupportedLinks(
		"https://pin.it/GuMLYBeYs",
		"https://jp.pinterest.com/pin/1076852960912705984/sent/?sfo=1",
	)
	if len(links) != 2 {
		t.Fatalf("links len = %d, want 2", len(links))
	}
	if links[0].Type != linkPinterest || links[0].ID != "GuMLYBeYs" {
		t.Fatalf("short link = %#v, want pinterest short token", links[0])
	}
	if links[1].Type != linkPinterest || links[1].ID != "1076852960912705984" {
		t.Fatalf("pin link = %#v, want pinterest pin id", links[1])
	}
}

func TestPinterestImageRankingPrefersOriginalsAcrossExtensions(t *testing.T) {
	candidates := []string{
		"https://i.pinimg.com/736x/85/6e/68/856e6890efed9fe7450aacf81fe94f62.jpg",
		"https://i.pinimg.com/236x/85/6e/68/856e6890efed9fe7450aacf81fe94f62.jpg",
		"https://i.pinimg.com/originals/85/6e/68/856e6890efed9fe7450aacf81fe94f62.png",
		"https://i.pinimg.com/1200x/85/6e/68/856e6890efed9fe7450aacf81fe94f62.jpg",
	}

	var expanded []string
	for _, candidate := range candidates {
		expanded = append(expanded, expandPinterestImageURL(candidate)...)
	}
	sortPinterestImageURLs(expanded, candidates)

	want := "https://i.pinimg.com/originals/85/6e/68/856e6890efed9fe7450aacf81fe94f62.png"
	if len(expanded) == 0 || expanded[0] != want {
		t.Fatalf("first expanded = %q, want %q", expanded[0], want)
	}
}
