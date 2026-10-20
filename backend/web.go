package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Web tools for agents: fetch a public page, and (when the operator configured a search service) search.
// Everything they return is text written by strangers; the step prompt already tells the model to treat
// tool results as data.

const (
	webLabel        = "Web search" // the name a step lists to get these tools
	maxFetchBytes   = 2 << 20
	maxFetchOutput  = 12000
	maxSearchOutput = 6000
)

// WebSearchConfig is the search service behind web_search. Without one, agents can still fetch pages.
type WebSearchConfig struct {
	Provider string // "", brave or searxng
	Key      string // brave
	URL      string // searxng base address; brave override
}

func (w WebSearchConfig) Enabled() bool { return w.Provider != "" }

func webTools(search WebSearchConfig) []ToolDef {
	defs := []ToolDef{{
		Name: "web_fetch",
		Description: "Read a public web page and get its text. Give a full https:// address. Use it for pages you were pointed to or found by searching. " +
			"It cannot reach private or internal addresses, log in, or run scripts.",
		InputSchema: objSchema(map[string]any{"url": map[string]any{"type": "string", "description": "Full address, for example https://example.com/page"}}, "url"),
	}}
	if search.Enabled() {
		defs = append(defs, ToolDef{
			Name:        "web_search",
			Description: "Search the web. Returns up to 5 results with title, address and a short snippet. Follow up with web_fetch to read a result.",
			InputSchema: objSchema(map[string]any{"query": map[string]any{"type": "string", "description": "What to search for"}}, "query"),
		})
	}
	return defs
}

func (s *Server) runWeb(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	switch name {
	case "web_fetch":
		var in struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", errors.New("the tool input was not valid")
		}
		return s.webFetch(ctx, in.URL)
	case "web_search":
		var in struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", errors.New("the tool input was not valid")
		}
		return s.webSearch(ctx, in.Query)
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

func (s *Server) webFetch(ctx context.Context, raw string) (string, error) {
	u, err := parseWebURL(raw, s.webGuard.anyPort)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", errors.New("that is not a valid address")
	}
	req.Header.Set("User-Agent", "AgentWorkflow/1 (+web_fetch)")
	req.Header.Set("Accept", "text/html, text/plain, application/json, application/xml;q=0.9, */*;q=0.1")
	resp, err := s.webGuard.client(20 * time.Second).Do(req)
	if err != nil {
		if errors.Is(err, errBlockedAddress) || strings.Contains(err.Error(), errBlockedAddress.Error()) {
			return "", errBlockedAddress
		}
		return "", errors.New("could not fetch that page")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("the page answered with status %d", resp.StatusCode)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	isHTML := strings.Contains(ct, "html")
	textual := isHTML || strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "xml") || ct == ""
	if !textual {
		return "", fmt.Errorf("that address is not a text page (%s)", strings.SplitN(ct, ";", 2)[0])
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return "", errors.New("the page stopped loading")
	}
	text := string(body)
	title := ""
	if isHTML {
		title, text = htmlToText(text)
	}
	text = cut(strings.TrimSpace(text), maxFetchOutput)
	if text == "" {
		return "", errors.New("the page has no readable text")
	}
	head := "Fetched " + resp.Request.URL.String()
	if title != "" {
		head += "\nTitle: " + title
	}
	return head + "\n\n" + text, nil
}

var (
	reBlocks   = regexp.MustCompile(`(?is)<(script|style|noscript|svg|template|head)[^>]*>.*?</(script|style|noscript|svg|template|head)>`)
	reComments = regexp.MustCompile(`(?s)<!--.*?-->`)
	reBreaks   = regexp.MustCompile(`(?i)</?(br|p|div|li|ul|ol|tr|table|h[1-6]|section|article|header|footer|blockquote|pre)[^>]*>`)
	reTags     = regexp.MustCompile(`(?s)<[^>]*>`)
	reTitle    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reSpaces   = regexp.MustCompile(`[ \t\f\r\x{00a0}]+`)
	reBlank    = regexp.MustCompile(`\n\s*\n+`)
)

// htmlToText gives the title and the readable text of an HTML page. It is a plain tag stripper, enough
// for a model to read an article; it does not run scripts or follow frames.
func htmlToText(src string) (title, text string) {
	if m := reTitle.FindStringSubmatch(src); m != nil {
		title = strings.TrimSpace(reSpaces.ReplaceAllString(html.UnescapeString(reTags.ReplaceAllString(m[1], "")), " "))
	}
	src = reComments.ReplaceAllString(src, "")
	src = reBlocks.ReplaceAllString(src, "")
	src = reBreaks.ReplaceAllString(src, "\n")
	src = reTags.ReplaceAllString(src, "")
	src = html.UnescapeString(src)
	src = reSpaces.ReplaceAllString(src, " ")
	lines := strings.Split(src, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	src = reBlank.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return cut(title, 200), strings.TrimSpace(src)
}

func (s *Server) webSearch(ctx context.Context, q string) (string, error) {
	cfg := s.cfg.WebSearch
	if !cfg.Enabled() {
		return "", errors.New("web search is not set up on this server")
	}
	q = strings.TrimSpace(q)
	if q == "" || len([]rune(q)) > 300 {
		return "", errors.New("give a search of up to 300 characters")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var endpoint string
	hdr := http.Header{"Accept": {"application/json"}, "User-Agent": {"AgentWorkflow/1 (+web_search)"}}
	switch cfg.Provider {
	case "brave":
		base := cfg.URL
		if base == "" {
			base = "https://api.search.brave.com/res/v1/web/search"
		}
		endpoint = base + "?count=5&q=" + url.QueryEscape(q)
		hdr.Set("X-Subscription-Token", cfg.Key)
	case "searxng":
		endpoint = strings.TrimRight(cfg.URL, "/") + "/search?format=json&q=" + url.QueryEscape(q)
	default:
		return "", errors.New("web search is not set up on this server")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", errors.New("web search is not set up correctly on this server")
	}
	req.Header = hdr
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", errors.New("the search service could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("the search service answered with status %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	type hit struct{ Title, URL, Snippet string }
	var hits []hit
	switch cfg.Provider {
	case "brave":
		var r struct {
			Web struct {
				Results []struct{ Title, URL, Description string } `json:"results"`
			} `json:"web"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return "", errors.New("the search service sent something unexpected")
		}
		for _, x := range r.Web.Results {
			hits = append(hits, hit{x.Title, x.URL, x.Description})
		}
	case "searxng":
		var r struct {
			Results []struct{ Title, URL, Content string } `json:"results"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return "", errors.New("the search service sent something unexpected")
		}
		for _, x := range r.Results {
			hits = append(hits, hit{x.Title, x.URL, x.Content})
		}
	}
	if len(hits) == 0 {
		return "No results.", nil
	}
	var b strings.Builder
	for i, h := range hits {
		if i == 5 {
			break
		}
		snippet := strings.TrimSpace(reTags.ReplaceAllString(html.UnescapeString(h.Snippet), ""))
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n", i+1, cut(strings.TrimSpace(h.Title), 200), strings.TrimSpace(h.URL), cut(snippet, 400))
	}
	return cut(b.String(), maxSearchOutput), nil
}
