package content

import "time"

type PublishedNote struct {
	ID                 string
	DomainID           string
	Title              string
	Slug               string
	Summary            string
	Markdown           string
	ReadingTimeMinutes int
	PublishedAt        time.Time
	UpdatedAt          time.Time
	Version            int
	Tags               []TagInput
	References         []ReferenceInput
}

type PublishedNoteSummary struct {
	ID                 string
	DomainID           string
	Title              string
	Slug               string
	Summary            string
	Tags               []TagInput
	ReadingTimeMinutes int
	PublishedAt        time.Time
}

type AdjacentNote struct {
	Slug  string
	Title string
}
