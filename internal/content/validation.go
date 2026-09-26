package content

import (
	"context"
	"strings"
)

type ValidationIssue struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationResult struct {
	Errors   []ValidationIssue `json:"errors"`
	Warnings []ValidationIssue `json:"warnings"`
}

func (r ValidationResult) Valid() bool { return len(r.Errors) == 0 }

type LinkChecker interface {
	InternalExists(context.Context, string) (bool, error)
	ExternalStatus(context.Context, string) (int, error)
}

type ValidationOptions struct {
	CheckExternal bool
}

func ValidateForPublication(ctx context.Context, note Note, checker LinkChecker, options ValidationOptions) ValidationResult {
	result := ValidationResult{}
	if strings.TrimSpace(note.Title) == "" {
		result.Errors = append(result.Errors, ValidationIssue{"title", "title is required"})
	}
	if !validSlug(note.Slug) {
		result.Errors = append(result.Errors, ValidationIssue{"slug", "slug is invalid"})
	}
	if strings.TrimSpace(note.Markdown) == "" {
		result.Errors = append(result.Errors, ValidationIssue{"markdown", "content is required"})
	}
	if note.DomainID == "" {
		result.Errors = append(result.Errors, ValidationIssue{"domain_id", "domain is required"})
	}
	if _, _, err := RenderHTML(note.Markdown); err != nil {
		result.Errors = append(result.Errors, ValidationIssue{"markdown", err.Error()})
	}
	for _, link := range markdownLinks(note.Markdown) {
		if strings.HasPrefix(link, "/") {
			if checker == nil {
				continue
			}
			exists, err := checker.InternalExists(ctx, link)
			if err != nil {
				result.Warnings = append(result.Warnings, ValidationIssue{"markdown", "could not verify internal link " + link})
			} else if !exists {
				result.Errors = append(result.Errors, ValidationIssue{"markdown", "internal link does not exist: " + link})
			}
			continue
		}
		if !ValidURL(link) {
			result.Errors = append(result.Errors, ValidationIssue{"markdown", "unsafe or invalid link: " + link})
			continue
		}
		if !options.CheckExternal || checker == nil {
			continue
		}
		status, err := checker.ExternalStatus(ctx, link)
		if err != nil {
			result.Warnings = append(result.Warnings, ValidationIssue{"markdown", "could not verify external link " + link})
		} else if status >= 400 {
			result.Errors = append(result.Errors, ValidationIssue{"markdown", "external link is unavailable: " + link})
		}
	}
	return result
}

func CanTransition(from, to Status) bool {
	return (from == StatusDraft && (to == StatusPublished || to == StatusArchived)) ||
		(from == StatusPublished && to == StatusArchived)
}

func markdownLinks(markdown string) []string {
	var links []string
	for rest := markdown; ; {
		start := strings.Index(rest, "](")
		if start < 0 {
			break
		}
		rest = rest[start+2:]
		end := strings.IndexByte(rest, ')')
		if end < 0 {
			break
		}
		links = append(links, strings.TrimSpace(rest[:end]))
		rest = rest[end+1:]
	}
	return links
}
