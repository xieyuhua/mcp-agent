package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// WebService 联网查询能力：网页搜索与网页正文抓取。
// 搜索使用 DuckDuckGo HTML 版（无需 API key，适合本地/内网部署）；
// 抓取使用标准库解析 HTML 并提取纯文本，去除脚本/样式/注释噪声。
type WebService struct {
	client    *http.Client
	userAgent string
}

// NewWebService 构造联网查询服务。
func NewWebService() *WebService {
	return &WebService{
		client: &http.Client{Timeout: 20 * time.Second},
		userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
			"(KHTML, like Gecko) Chrome/126.0 Safari/537.36",
	}
}

// SearchResult 一条搜索结果。
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// SearchResponse 联网搜索返回。
type SearchResponse struct {
	Query   string         `json:"query"`
	Count   int            `json:"count"`
	Results []SearchResult `json:"results"`
}

// Search 执行一次联网搜索，返回前 limit 条结果（默认 5，最大 10）。
func (w *WebService) Search(_ context.Context, query string, limit int, onProgress ProgressFunc) (*SearchResponse, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}
	if onProgress != nil {
		onProgress(0, "正在联网搜索: "+query)
	}

	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	body, err := w.getHTML(searchURL)
	if err != nil {
		return nil, fmt.Errorf("web search request failed: %w", err)
	}
	defer body.Close()

	results, err := parseDuckDuckGo(body, limit)
	if err != nil {
		return nil, fmt.Errorf("parse search results: %w", err)
	}
	if onProgress != nil {
		onProgress(len(results), fmt.Sprintf("搜索完成，获得 %d 条结果", len(results)))
	}
	return &SearchResponse{Query: query, Count: len(results), Results: results}, nil
}

// FetchResult 网页抓取返回。
type FetchResult struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	Length    int    `json:"length"`
	Truncated bool   `json:"truncated"`
}

// Fetch 抓取指定 URL 的网页并提取正文纯文本（截断到 maxChars，默认 8000，最大 40000）。
func (w *WebService) Fetch(_ context.Context, targetURL string, maxChars int, onProgress ProgressFunc) (*FetchResult, error) {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, fmt.Errorf("url is required")
	}
	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		return nil, fmt.Errorf("url must start with http:// or https://")
	}
	if maxChars <= 0 {
		maxChars = 8000
	}
	if maxChars > 40000 {
		maxChars = 40000
	}
	if onProgress != nil {
		onProgress(0, "正在抓取网页: "+targetURL)
	}

	body, err := w.getHTML(targetURL)
	if err != nil {
		return nil, fmt.Errorf("fetch web page failed: %w", err)
	}
	defer body.Close()

	title, text, err := extractText(body, maxChars)
	if err != nil {
		return nil, fmt.Errorf("extract text: %w", err)
	}
	truncated := len([]rune(text)) >= maxChars
	if onProgress != nil {
		onProgress(len([]rune(text)), "网页抓取完成")
	}
	return &FetchResult{
		URL:       targetURL,
		Title:     title,
		Text:      text,
		Length:    len([]rune(text)),
		Truncated: truncated,
	}, nil
}

// getHTML 发起 GET 请求并返回响应体（调用方负责关闭）。
func (w *WebService) getHTML(rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", w.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// parseDuckDuckGo 从 DuckDuckGo HTML 结果页解析搜索结果。
func parseDuckDuckGo(r io.Reader, limit int) ([]SearchResult, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var results []SearchResult
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(results) >= limit {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" && hasClass(n, "result__a") {
			href := attr(n, "href")
			title := textContent(n)
			if href != "" && title != "" {
				if real := decodeDuckDuckGoURL(href); real != "" {
					href = real
				}
				snippet := findSnippet(n)
				results = append(results, SearchResult{
					Title:   strings.TrimSpace(title),
					URL:     href,
					Snippet: strings.TrimSpace(snippet),
				})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results, nil
}

// findSnippet 从标题链接节点出发，在其祖先 result 容器内查找摘要文本。
func findSnippet(linkNode *html.Node) string {
	container := linkNode
	for container != nil && !hasClass(container, "result") {
		container = container.Parent
	}
	if container == nil {
		return ""
	}
	var snippet string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if snippet != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" && hasClass(n, "result__snippet") {
			snippet = textContent(n)
			return
		}
		for c := n.FirstChild; c != nil && snippet == ""; c = c.NextSibling {
			walk(c)
		}
	}
	walk(container)
	return snippet
}

// decodeDuckDuckGoURL 把 DuckDuckGo 的跳转链接还原为真实目标 URL。
func decodeDuckDuckGoURL(href string) string {
	if !strings.Contains(href, "duckduckgo.com/l/?") && !strings.Contains(href, "duckduckgo.com/l?") {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	q := u.Query().Get("uddg")
	if q == "" {
		return ""
	}
	if decoded, err := url.QueryUnescape(q); err == nil {
		return decoded
	}
	return q
}

// extractText 从 HTML 提取标题与正文纯文本，截断到 maxChars（按 rune 计）。
func extractText(r io.Reader, maxChars int) (string, string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", "", err
	}
	var title string
	var titleFound bool
	var findTitle func(*html.Node)
	findTitle = func(n *html.Node) {
		if titleFound {
			return
		}
		if n.Type == html.ElementNode && n.Data == "title" {
			title = strings.TrimSpace(textContent(n))
			titleFound = true
			return
		}
		for c := n.FirstChild; c != nil && !titleFound; c = c.NextSibling {
			findTitle(c)
		}
	}
	findTitle(doc)

	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "head", "meta", "link", "svg":
				return
			case "br":
				sb.WriteRune('\n')
			case "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote":
				if sb.Len() > 0 {
					sb.WriteRune('\n')
				}
			}
		}
		if n.Type == html.TextNode {
			t := strings.TrimSpace(n.Data)
			if t != "" {
				sb.WriteString(t)
				sb.WriteRune(' ')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	full := sb.String()
	runes := []rune(full)
	if len(runes) > maxChars {
		return title, string(runes[:maxChars]), nil
	}
	return title, full, nil
}

// hasClass 判断节点是否含有指定 class。
func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

// attr 取节点指定属性值。
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// textContent 递归提取节点下的全部文本（去掉 script/style）。
func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
		return ""
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(textContent(c))
	}
	return sb.String()
}
