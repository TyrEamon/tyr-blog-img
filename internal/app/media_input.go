package app

import (
	"path"
	"strings"

	"github.com/go-telegram/bot/models"
)

type incomingMediaKind string

const (
	incomingMediaImage     incomingMediaKind = "image"
	incomingMediaVideo     incomingMediaKind = "video"
	incomingMediaAnimation incomingMediaKind = "animation"
)

type incomingMedia struct {
	FileID       string
	FileUniqueID string
	Kind         incomingMediaKind
	Filename     string
}

func (m incomingMedia) isImage() bool {
	return m.Kind == incomingMediaImage
}

func extractIncomingMedia(msg *models.Message) (incomingMedia, bool) {
	if msg == nil {
		return incomingMedia{}, false
	}
	if len(msg.Photo) > 0 {
		p := msg.Photo[len(msg.Photo)-1]
		return incomingMedia{
			FileID:       strings.TrimSpace(p.FileID),
			FileUniqueID: strings.TrimSpace(p.FileUniqueID),
			Kind:         incomingMediaImage,
		}, true
	}
	if msg.Document != nil {
		return incomingMedia{
			FileID:       strings.TrimSpace(msg.Document.FileID),
			FileUniqueID: strings.TrimSpace(msg.Document.FileUniqueID),
			Kind:         classifyDocumentKind(msg.Document),
			Filename:     strings.TrimSpace(msg.Document.FileName),
		}, true
	}
	if msg.Video != nil {
		return incomingMedia{
			FileID:       strings.TrimSpace(msg.Video.FileID),
			FileUniqueID: strings.TrimSpace(msg.Video.FileUniqueID),
			Kind:         incomingMediaVideo,
			Filename:     strings.TrimSpace(msg.Video.FileName),
		}, true
	}
	if msg.Animation != nil {
		return incomingMedia{
			FileID:       strings.TrimSpace(msg.Animation.FileID),
			FileUniqueID: strings.TrimSpace(msg.Animation.FileUniqueID),
			Kind:         incomingMediaAnimation,
			Filename:     strings.TrimSpace(msg.Animation.FileName),
		}, true
	}
	return incomingMedia{}, false
}

func classifyDocumentKind(doc *models.Document) incomingMediaKind {
	if doc == nil {
		return incomingMediaImage
	}
	mime := strings.ToLower(strings.TrimSpace(doc.MimeType))
	name := strings.ToLower(strings.TrimSpace(doc.FileName))
	ext := strings.ToLower(path.Ext(name))
	if mime == "image/gif" || ext == ".gif" {
		return incomingMediaAnimation
	}
	if strings.HasPrefix(mime, "video/") {
		return incomingMediaVideo
	}
	switch ext {
	case ".mp4", ".mov", ".m4v", ".webm", ".mkv":
		return incomingMediaVideo
	}
	return incomingMediaImage
}
