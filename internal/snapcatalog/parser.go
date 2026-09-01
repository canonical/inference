package snapcatalog

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
)

var expectedHeaders = []string{"Snap", "Model", "Repository"}
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
var snapNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type Entry struct {
	SnapName   string `json:"snap_name"`
	Model      string `json:"model"`
	Repository string `json:"repository"`
}

func Parse(data []byte) ([]Entry, error) {
	root, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	table := findCatalogTable(root)
	if table == nil {
		return nil, fmt.Errorf("snap catalog table not found")
	}

	var entries []Entry
	seenNames := make(map[string]struct{})
	seenRepos := make(map[string]struct{})
	for _, row := range tableRows(table)[1:] {
		cells := childElements(row, "td")
		if len(cells) != len(expectedHeaders) {
			return nil, fmt.Errorf("snap catalog row must have three cells")
		}

		name := nodeText(cells[0])
		if err := ValidateSnapName(name); err != nil {
			return nil, err
		}
		model := nodeText(cells[1])
		if model == "" {
			return nil, fmt.Errorf("snap catalog entry %q has an empty model name", name)
		}
		repository, err := parseRepositoryCell(cells[2])
		if err != nil {
			return nil, fmt.Errorf("snap catalog entry %q: %v", name, err)
		}
		if _, found := seenNames[name]; found {
			return nil, fmt.Errorf("duplicate snap name %q", name)
		}
		if _, found := seenRepos[repository]; found {
			return nil, fmt.Errorf("duplicate repository %q", repository)
		}
		seenNames[name] = struct{}{}
		seenRepos[repository] = struct{}{}
		entries = append(entries, Entry{
			SnapName:   name,
			Model:      model,
			Repository: repository,
		})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("snap catalog table is empty")
	}
	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.SnapName, b.SnapName) })
	return entries, nil
}

func findCatalogTable(node *html.Node) *html.Node {
	if node.Type == html.ElementNode && node.Data == "table" {
		rows := tableRows(node)
		if len(rows) > 0 {
			headers := childElements(rows[0], "th")
			if len(headers) == len(expectedHeaders) &&
				slices.EqualFunc(headers, expectedHeaders, func(header *html.Node, expected string) bool {
					return nodeText(header) == expected
				}) {
				return node
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if table := findCatalogTable(child); table != nil {
			return table
		}
	}
	return nil
}

func tableRows(table *html.Node) []*html.Node {
	return childElements(table, "tr")
}

func childElements(node *html.Node, tag string) (nodes []*html.Node) {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == tag {
			nodes = append(nodes, child)
		} else {
			nodes = append(nodes, childElements(child, tag)...)
		}
	}
	return nodes
}

func nodeText(node *html.Node) string {
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(text.String()), " ")
}

func parseRepositoryCell(cell *html.Node) (string, error) {
	links := childElements(cell, "a")
	if len(links) != 1 {
		return "", fmt.Errorf("expected one repository link")
	}

	var href string
	for _, attr := range links[0].Attr {
		if attr.Key == "href" {
			href = attr.Val
			break
		}
	}
	parsed, err := url.Parse(href)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid public GitHub URL %q", href)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || !repositoryPattern.MatchString(strings.Join(parts, "/")) {
		return "", fmt.Errorf("invalid GitHub repository path %q", parsed.Path)
	}
	repository := strings.Join(parts, "/")
	if displayed := nodeText(links[0]); displayed != repository {
		return "", fmt.Errorf("link text %q does not match %q", displayed, repository)
	}
	if nodeText(cell) != repository {
		return "", fmt.Errorf("repository cell contains unexpected text")
	}
	return repository, nil
}

func ValidateSnapName(name string) error {
	if len(name) > 40 || !snapNamePattern.MatchString(name) {
		return fmt.Errorf("%q is not a valid snap name", name)
	}
	return nil
}
