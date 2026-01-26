package view

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/anton-dovnar/git-tree/structs"
	"github.com/go-git/go-git/v5/plumbing"

	svg "github.com/ajstarks/svgo"

	mapset "github.com/deckarep/golang-set/v2"
)

//go:embed resources/*.css resources/*.js resources/*.html
var resources embed.FS

type CommitMessage struct {
	Type       string `json:"type,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	IsBreaking bool   `json:"is_breaking"`
}

type CommitData struct {
	Hash             string        `json:"hash"`
	Author           string        `json:"author"`
	Committer        string        `json:"committer"`
	Message          CommitMessage `json:"message"`
	AuthoredDate     string        `json:"authored_date"`
	CommittedDate    string        `json:"committed_date"`
	AuthoredDateDelta string       `json:"authored_date_delta"`
	CommittedDateDelta string      `json:"committed_date_delta"`
}

var issueRegex = regexp.MustCompile(`(\w+)#(\d+)`)

// prettyDate formats a time as a relative date string (e.g., "2 days ago")
func prettyDate(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)

	if diff < time.Minute {
		return "just now"
	}
	if diff < time.Hour {
		minutes := int(diff.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	}
	if diff < 24*time.Hour {
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}
	if diff < 30*24*time.Hour {
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
	if diff < 365*24*time.Hour {
		months := int(diff.Hours() / (24 * 30))
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	}
	years := int(diff.Hours() / (24 * 365))
	if years == 1 {
		return "1 year ago"
	}
	return fmt.Sprintf("%d years ago", years)
}

// issueLink replaces issue references with HTML links
func issueLink(text string, ghSlug string) string {
	if ghSlug == "" {
		return text
	}
	// Replace pattern like "org#123" with GitHub links
	replaced := issueRegex.ReplaceAllStringFunc(text, func(match string) string {
		parts := issueRegex.FindStringSubmatch(match)
		if len(parts) == 3 {
			org := parts[1]
			num := parts[2]
			// If org matches the repo owner, use the repo slug, otherwise use org
			if strings.HasPrefix(ghSlug, org+"/") {
				return fmt.Sprintf(`<a target="_blank" href="https://github.com/%s/issues/%s">%s#%s</a>`, ghSlug, num, org, num)
			}
			return fmt.Sprintf(`<a target="_blank" href="https://github.com/%s/issues/%s">%s#%s</a>`, org, num, org, num)
		}
		return match
	})
	return replaced
}

// parseCommitMessage parses a commit message into type, scope, and title
func parseCommitMessage(message string) (string, string, string) {
	// Try to parse conventional commit format: "type(scope): title"
	// First, find the first ": " separator
	colonIdx := strings.Index(message, ": ")
	if colonIdx < 0 {
		// No colon found, return entire message as title
		return "", "", message
	}

	// Extract the prefix (type and scope) and title
	prefix := strings.TrimSpace(message[:colonIdx])
	title := strings.TrimSpace(message[colonIdx+2:])

	// Check if prefix contains scope in parentheses
	parenIdx := strings.Index(prefix, "(")
	if parenIdx >= 0 {
		// Extract type and scope
		commitType := strings.TrimSpace(prefix[:parenIdx])
		rest := prefix[parenIdx+1:]
		closeParenIdx := strings.Index(rest, ")")
		if closeParenIdx >= 0 {
			scope := strings.TrimSpace(rest[:closeParenIdx])
			// Validate: type should not contain spaces
			if strings.Contains(commitType, " ") {
				return "", "", message
			}
			return commitType, scope, title
		}
	}

	// No scope, just type
	if strings.Contains(prefix, " ") {
		// Type with spaces is invalid, return full message
		return "", "", message
	}
	return prefix, "", title
}

// GenerateCommitData generates commit data for HTML output
func GenerateCommitData(
	commits map[plumbing.Hash]*structs.CommitInfo,
	ghSlug string,
) map[string]CommitData {
	result := make(map[string]CommitData)

	for hash, ci := range commits {
		if ci == nil || ci.Commit == nil {
			continue
		}
		commit := ci.Commit

		// Parse commit message
		fullMessage := commit.Message
		summary := strings.Split(fullMessage, "\n")[0]
		commitType, scope, title := parseCommitMessage(summary)

		// Extract body (everything after first line)
		body := ""
		lines := strings.Split(fullMessage, "\n")
		if len(lines) > 1 {
			bodyLines := lines[1:]
			// Remove leading empty lines
			for len(bodyLines) > 0 && strings.TrimSpace(bodyLines[0]) == "" {
				bodyLines = bodyLines[1:]
			}
			body = strings.Join(bodyLines, "\n")
			body = strings.TrimSpace(body)
			// Replace " \n" with " " for cleaner display
			body = strings.ReplaceAll(body, " \n", " ")
			body = strings.ReplaceAll(body, " \r\n", " ")
		}

		// Apply issue linking
		title = issueLink(title, ghSlug)
		body = issueLink(body, ghSlug)

		// Format author and committer with email links
		authorHTML := fmt.Sprintf(`<a href="mailto:%s">%s</a>`, html.EscapeString(commit.Author.Email), html.EscapeString(commit.Author.Name))
		committerHTML := fmt.Sprintf(`<a href="mailto:%s">%s</a>`, html.EscapeString(commit.Committer.Email), html.EscapeString(commit.Committer.Name))

		// Format dates
		authoredDate := commit.Author.When.Format(time.RFC3339)
		committedDate := commit.Committer.When.Format(time.RFC3339)
		authoredDateDelta := prettyDate(commit.Author.When)
		committedDateDelta := prettyDate(commit.Committer.When)

		// Check for breaking changes
		isBreaking := strings.Contains(fullMessage, "BREAKING CHANGE:")

		// Use short hash (7 characters)
		hashStr := hash.String()
		if len(hashStr) > 7 {
			hashStr = hashStr[:7]
		}

		result[hash.String()] = CommitData{
			Hash:              hashStr,
			Author:            authorHTML,
			Committer:         committerHTML,
			Message: CommitMessage{
				Type:       commitType,
				Scope:      scope,
				Title:      title,
				Body:       body,
				IsBreaking: isBreaking,
			},
			AuthoredDate:      authoredDate,
			CommittedDate:     committedDate,
			AuthoredDateDelta: authoredDateDelta,
			CommittedDateDelta: committedDateDelta,
		}
	}

	return result
}

// getResource reads a resource file from the embedded filesystem
func getResource(name string) (string, error) {
	data, err := resources.ReadFile("resources/" + name)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// replacePlaceholders replaces ((% placeholder %)) with values
func replacePlaceholders(text string, placeholders map[string]string) string {
	result := text
	for key, value := range placeholders {
		placeholder := fmt.Sprintf("((%% %s %%))", key)
		result = strings.ReplaceAll(result, placeholder, value)
	}
	return result
}

// replaceReferences replaces {{ reference }} with resource content
func replaceReferences(text string) (string, error) {
	result := text
	begin := 0

	for {
		// Find {{ reference }}
		startIdx := strings.Index(result[begin:], "{{")
		if startIdx < 0 {
			break
		}
		startIdx += begin

		endIdx := strings.Index(result[startIdx+2:], "}}")
		if endIdx < 0 {
			break
		}
		endIdx += startIdx + 2

		// Extract reference name
		reference := strings.TrimSpace(result[startIdx+2 : endIdx])

		// Load resource
		resourceContent, err := getResource(reference)
		if err != nil {
			return "", fmt.Errorf("failed to load resource %s: %w", reference, err)
		}

		// Recursively replace references in the resource
		resourceContent, err = replaceReferences(resourceContent)
		if err != nil {
			return "", err
		}

		// Replace the placeholder
		placeholder := result[startIdx : endIdx+2]
		result = strings.Replace(result, placeholder, resourceContent, 1)

		begin = startIdx + len(resourceContent)
	}

	return result, nil
}

// GenerateSVGString generates SVG as a string instead of writing to a writer
func GenerateSVGString(
	commits map[plumbing.Hash]*structs.CommitInfo,
	positions map[plumbing.Hash][2]int,
	heads map[plumbing.Hash][]*plumbing.Reference,
	tags map[plumbing.Hash][]*plumbing.Reference,
	children map[plumbing.Hash]mapset.Set[plumbing.Hash],
) (string, error) {
	var buf bytes.Buffer
	canvas := svg.New(&buf)
	DrawRailway(canvas, commits, positions, heads, tags, children)
	return buf.String(), nil
}

// WriteHTML generates and writes HTML output
func WriteHTML(
	w io.Writer,
	svgContent string,
	commitData map[string]CommitData,
	title string,
) error {
	// Load HTML template
	template, err := getResource("html_template.html")
	if err != nil {
		return fmt.Errorf("failed to load HTML template: %w", err)
	}

	// Convert commit data to JSON
	commitDataJSON, err := json.Marshal(commitData)
	if err != nil {
		return fmt.Errorf("failed to marshal commit data: %w", err)
	}

	// Add id="railway_svg" to SVG element if not present
	if !strings.Contains(svgContent, `id="railway_svg"`) && !strings.Contains(svgContent, `id='railway_svg'`) {
		// Find the opening <svg> tag and add id attribute
		svgTagStart := strings.Index(svgContent, "<svg")
		if svgTagStart >= 0 {
			svgTagEnd := strings.Index(svgContent[svgTagStart:], ">")
			if svgTagEnd >= 0 {
				svgTagEnd += svgTagStart
				// Check if there's already an id attribute
				svgTag := svgContent[svgTagStart:svgTagEnd]
				if !strings.Contains(svgTag, "id=") {
					// Insert id attribute before the closing >
					svgContent = svgContent[:svgTagEnd] + ` id="railway_svg"` + svgContent[svgTagEnd:]
				}
			}
		}
	}

	// First replace resource references (CSS, JS) - this embeds the JavaScript
	// which contains placeholders that need to be replaced
	template, err = replaceReferences(template)
	if err != nil {
		return fmt.Errorf("failed to replace resource references: %w", err)
	}

	// Now replace all placeholders (including those in embedded JavaScript)
	placeholders := map[string]string{
		"title": html.EscapeString(title),
		"svg":   svgContent,
		"data":  string(commitDataJSON),
	}
	template = replacePlaceholders(template, placeholders)

	// Write final HTML
	_, err = w.Write([]byte(template))
	return err
}
