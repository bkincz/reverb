package collections

import (
	"fmt"
	"html"
	"strings"
)

func RenderProseMirror(doc map[string]any) string {
	var b strings.Builder
	renderNode(&b, doc)
	return b.String()
}

func renderNode(b *strings.Builder, node map[string]any) {
	typ, _ := node["type"].(string)
	content, _ := node["content"].([]any)
	attrs, _ := node["attrs"].(map[string]any)

	switch typ {
	case "doc":
		renderChildren(b, content)

	case "paragraph":
		b.WriteString("<p>")
		renderChildren(b, content)
		b.WriteString("</p>")

	case "text":
		text, _ := node["text"].(string)
		marks, _ := node["marks"].([]any)
		rendered := html.EscapeString(text)
		rendered = applyMarks(rendered, marks)
		b.WriteString(rendered)

	case "heading":
		level := 1
		if l, ok := attrs["level"].(float64); ok {
			level = int(l)
		}
		if level < 1 || level > 6 {
			level = 1
		}
		tag := fmt.Sprintf("h%d", level)
		b.WriteString("<" + tag + ">")
		renderChildren(b, content)
		b.WriteString("</" + tag + ">")

	case "bulletList":
		b.WriteString("<ul>")
		renderChildren(b, content)
		b.WriteString("</ul>")

	case "orderedList":
		b.WriteString("<ol>")
		renderChildren(b, content)
		b.WriteString("</ol>")

	case "listItem":
		b.WriteString("<li>")
		renderChildren(b, content)
		b.WriteString("</li>")

	case "blockquote":
		b.WriteString("<blockquote>")
		renderChildren(b, content)
		b.WriteString("</blockquote>")

	case "codeBlock":
		lang, _ := attrs["language"].(string)
		if lang != "" {
			b.WriteString(fmt.Sprintf(`<pre><code class="language-%s">`, html.EscapeString(lang)))
		} else {
			b.WriteString("<pre><code>")
		}
		renderChildren(b, content)
		b.WriteString("</code></pre>")

	case "horizontalRule":
		b.WriteString("<hr>")

	case "hardBreak":
		b.WriteString("<br>")

	case "image":
		src, _ := attrs["src"].(string)
		alt, _ := attrs["alt"].(string)
		title, _ := attrs["title"].(string)

		if src != "" && isSafeImageURL(src) {
			b.WriteString(fmt.Sprintf(`<img src="%s" alt="%s"`,
				html.EscapeString(src), html.EscapeString(alt)))
			if title != "" {
				b.WriteString(fmt.Sprintf(` title="%s"`, html.EscapeString(title)))
			}
			b.WriteString(">")
		}
	}
}

func renderChildren(b *strings.Builder, content []any) {
	for _, child := range content {
		if m, ok := child.(map[string]any); ok {
			renderNode(b, m)
		}
	}
}

// ---------------------------------------------------------------------------
// URL safety
// ---------------------------------------------------------------------------

func isSafeLinkURL(u string) bool {
	l := strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(l, "http://") ||
		strings.HasPrefix(l, "https://") ||
		strings.HasPrefix(l, "mailto:") ||
		strings.HasPrefix(l, "/") ||
		strings.HasPrefix(l, "#")
}

func isSafeImageURL(u string) bool {
	l := strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(l, "http://") ||
		strings.HasPrefix(l, "https://") ||
		strings.HasPrefix(l, "data:image/") ||
		strings.HasPrefix(l, "/")
}

// ---------------------------------------------------------------------------
// Marks
// ---------------------------------------------------------------------------

func applyMarks(text string, marks []any) string {
	for _, raw := range marks {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		attrs, _ := m["attrs"].(map[string]any)
		switch typ {
		case "bold":
			text = "<strong>" + text + "</strong>"
		case "italic":
			text = "<em>" + text + "</em>"
		case "underline":
			text = "<u>" + text + "</u>"
		case "strike":
			text = "<s>" + text + "</s>"
		case "code":
			text = "<code>" + text + "</code>"
		case "link":
			href, _ := attrs["href"].(string)
			target, _ := attrs["target"].(string)

			if href != "" && isSafeLinkURL(href) {
				if target != "" {
					text = fmt.Sprintf(`<a href="%s" target="%s">%s</a>`,
						html.EscapeString(href), html.EscapeString(target), text)
				} else {
					text = fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(href), text)
				}
			}
		}
	}
	return text
}
