package content

import (
	"strings"
	"time"
)

type Status string

const (
	StatusDraft     Status = "draft"
	StatusPublished Status = "published"
	StatusArchived  Status = "archived"
)

type Domain struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Weight      int    `json:"weight"`
	SortOrder   int    `json:"sort_order"`
	IsActive    bool   `json:"is_active"`
}

type DomainInput struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Weight      int    `json:"weight"`
	SortOrder   int    `json:"sort_order"`
	IsActive    bool   `json:"is_active"`
}

func (in DomainInput) Validate() error {
	errs := FieldErrors{}
	if name := strings.TrimSpace(in.Name); name == "" || len([]rune(name)) > 200 {
		errs["name"] = "must be between 1 and 200 characters"
	}
	if !validSlug(strings.TrimSpace(in.Slug)) {
		errs["slug"] = "must use lowercase letters, numbers, and single hyphens"
	}
	if len([]rune(in.Description)) > 2000 {
		errs["description"] = "must not exceed 2000 characters"
	}
	if in.Weight < 0 || in.Weight > 100 {
		errs["weight"] = "must be between 0 and 100"
	}
	if in.SortOrder < 1 {
		errs["sort_order"] = "must be at least 1"
	}
	if len(errs) != 0 {
		return errs
	}
	return nil
}

type TagInput struct {
	Name string `json:"name"`
}

type ReferenceInput struct {
	Title    string `json:"title"`
	URL      string `json:"url,omitempty"`
	Citation string `json:"citation,omitempty"`
	Position int    `json:"position"`
}

type NoteInput struct {
	DomainID   string           `json:"domain_id"`
	Title      string           `json:"title"`
	Slug       string           `json:"slug"`
	Summary    string           `json:"summary"`
	Markdown   string           `json:"markdown"`
	Tags       []TagInput       `json:"tags"`
	References []ReferenceInput `json:"references"`
}

type Note struct {
	ID                 string           `json:"id"`
	DomainID           string           `json:"domain_id"`
	AuthorID           string           `json:"author_id"`
	Title              string           `json:"title"`
	Slug               string           `json:"slug"`
	Summary            string           `json:"summary"`
	Markdown           string           `json:"markdown"`
	Status             Status           `json:"status"`
	Version            int              `json:"version"`
	ReadingTimeMinutes int              `json:"reading_time_minutes"`
	PublishedAt        *time.Time       `json:"published_at,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	Tags               []TagInput       `json:"tags"`
	References         []ReferenceInput `json:"references"`
}

type FieldErrors map[string]string

func (e FieldErrors) Error() string { return "invalid note input" }

func (in NoteInput) Validate() error {
	errs := FieldErrors{}
	in.Title = strings.TrimSpace(in.Title)
	in.Slug = strings.TrimSpace(in.Slug)
	if in.DomainID == "" {
		errs["domain_id"] = "is required"
	}
	if in.Title == "" || len([]rune(in.Title)) > 200 {
		errs["title"] = "must be between 1 and 200 characters"
	}
	if !validSlug(in.Slug) {
		errs["slug"] = "must use lowercase letters, numbers, and single hyphens"
	}
	if len([]rune(in.Summary)) > 500 {
		errs["summary"] = "must not exceed 500 characters"
	}
	if len([]rune(in.Markdown)) > 200000 {
		errs["markdown"] = "must not exceed 200000 characters"
	}
	seenTags := map[string]struct{}{}
	for i, tag := range in.Tags {
		key := strings.ToLower(strings.TrimSpace(tag.Name))
		if !validSlug(key) || len(key) > 64 {
			errs["tags["+itoa(i)+"]"] = "must be a valid slug of at most 64 characters"
		} else if _, exists := seenTags[key]; exists {
			errs["tags["+itoa(i)+"]"] = "must be unique"
		}
		seenTags[key] = struct{}{}
	}
	for i, reference := range in.References {
		if strings.TrimSpace(reference.Title) == "" || len([]rune(reference.Title)) > 300 {
			errs["references["+itoa(i)+"].title"] = "is required and must not exceed 300 characters"
		}
		if reference.Position != i+1 {
			errs["references["+itoa(i)+"].position"] = "must be contiguous and start at 1"
		}
		if reference.URL != "" && !ValidURL(reference.URL) {
			errs["references["+itoa(i)+"].url"] = "must be an absolute http or https URL"
		}
	}
	if len(errs) != 0 {
		return errs
	}
	return nil
}

func EstimateReadingTime(markdown string) int {
	words := len(strings.Fields(markdown))
	if words < 1 {
		return 1
	}
	return max(1, (words+199)/200)
}

func validSlug(value string) bool {
	if value == "" || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	previousHyphen := false
	for _, r := range value {
		if r == '-' {
			if previousHyphen {
				return false
			}
			previousHyphen = true
			continue
		}
		previousHyphen = false
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	result := ""
	for value > 0 {
		result = string(rune('0'+value%10)) + result
		value /= 10
	}
	return result
}
