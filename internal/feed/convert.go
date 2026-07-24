package feed

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"

	"github.com/Tiim/goread/internal/model"
	"github.com/mmcdole/gofeed"
)

// itemToArticle converts a gofeed Item into a model.Article for the given
// feed. The returned article has no ID set; the caller is responsible for
// deciding whether to Create or Update (carrying over ID/Read state) based on
// identity resolution.
func itemToArticle(feedID int64, item *gofeed.Item) *model.Article {
	content := item.Content
	if content == "" {
		content = item.Description
	}

	publishedAt := item.PublishedParsed
	updatedAt := item.UpdatedParsed
	hashDate := publishedAt
	if hashDate == nil {
		hashDate = updatedAt
	}

	return &model.Article{
		FeedID:      feedID,
		GUID:        item.GUID,
		Title:       item.Title,
		Author:      authorName(item.Author, item.Authors),
		PublishedAt: publishedAt,
		UpdatedAt:   updatedAt,
		Link:        item.Link,
		Summary:     item.Description,
		Content:     content,
		ContentText: htmlToText(content),
		ContentType: "text/html",
		State:       model.ArticleStatePresent,
		ContentHash: ContentHash(item.Link, hashDate),
		Metadata:    itemMetadata(item),
	}
}

func authorName(primary *gofeed.Person, authors []*gofeed.Person) string {
	if primary != nil && primary.Name != "" {
		return primary.Name
	}
	for _, a := range authors {
		if a != nil && a.Name != "" {
			return a.Name
		}
	}
	return ""
}

func itemMetadata(item *gofeed.Item) string {
	if len(item.Enclosures) == 0 && len(item.Categories) == 0 {
		return ""
	}
	data := struct {
		Enclosures []*gofeed.Enclosure `json:"enclosures,omitempty"`
		Categories []string            `json:"categories,omitempty"`
	}{item.Enclosures, item.Categories}
	b, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return string(b)
}

var htmlTagRE = regexp.MustCompile(`(?s)<[^>]*>`)

// htmlToText produces a rough plain-text rendering of HTML content, used to
// populate the article's searchable text content field.
func htmlToText(s string) string {
	if s == "" {
		return ""
	}
	stripped := htmlTagRE.ReplaceAllString(s, " ")
	unescaped := html.UnescapeString(stripped)
	return strings.Join(strings.Fields(unescaped), " ")
}
