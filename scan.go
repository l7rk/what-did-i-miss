package main

import (
	"html"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

type node struct {
	tag      string
	attrs    map[string]string
	children []*node
	text     string
	parent   *node
}

var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "source": true,
	"track": true, "wbr": true, "param": true,
}

var (
	tagRe  = regexp.MustCompile(`(?s)<!--.*?-->|<(/?)([a-zA-Z][a-zA-Z0-9-]*)((?:[^>"']|"[^"]*"|'[^']*')*)>`)
	attrRe = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+)))?`)

	notDoneText = regexp.MustCompile(`(?i)\bnot\s+(yet\s+)?(completed?|started|done|viewed|passed)\b|\bincomplete\b|\bto\s*do:|\bnot\s+attempted\b`)

	notDoneCode = regexp.MustCompile(`(?i)completion[-_](auto|manual)?[-_]?n\b|completion-n\b|notcomplete|not-complete|incomplete|todo`)

	doneText = regexp.MustCompile(`(?i)^\s*(completed?|done)\b|completion[-_](auto|manual)?[-_]?y\b|completion-y\b|\bcomplete\b`)
)

var attrUnescape = strings.NewReplacer("&amp;", "&", "&quot;", `"`, "&#39;", "'", "&#039;", "'", "&lt;", "<", "&gt;", ">", "&nbsp;", " ")

func parse(src string) *node {
	root := &node{tag: "#root", attrs: map[string]string{}}
	cur := root
	last := 0
	addText := func(s string) {
		if strings.TrimSpace(s) != "" {
			cur.children = append(cur.children, &node{tag: "#text", text: html.UnescapeString(s), parent: cur})
		}
	}
	skipUntil := ""
	for _, m := range tagRe.FindAllStringSubmatchIndex(src, -1) {
		full := src[m[0]:m[1]]
		if strings.HasPrefix(full, "<!--") {
			if skipUntil == "" {
				addText(src[last:m[0]])
			}
			last = m[1]
			continue
		}
		closing := src[m[2]:m[3]] == "/"
		name := strings.ToLower(src[m[4]:m[5]])
		if skipUntil != "" {
			if closing && name == skipUntil {
				skipUntil = ""
				last = m[1]
			}
			continue
		}
		addText(src[last:m[0]])
		last = m[1]
		if closing {
			for n := cur; n != nil && n != root; n = n.parent {
				if n.tag == name {
					cur = n.parent
					break
				}
			}
			continue
		}
		attrs := map[string]string{}
		for _, a := range attrRe.FindAllStringSubmatch(src[m[6]:m[7]], -1) {
			v := a[2] + a[3] + a[4]
			attrs[strings.ToLower(a[1])] = attrUnescape.Replace(v)
		}
		n := &node{tag: name, attrs: attrs, parent: cur}
		cur.children = append(cur.children, n)
		selfClosing := strings.HasSuffix(strings.TrimSpace(src[m[6]:m[7]]), "/")
		if name == "script" || name == "style" {
			skipUntil = name
			continue
		}
		if !voidTags[name] && !selfClosing {
			cur = n
		}
	}
	return root
}

func textOf(n *node) string {
	if n.tag == "#text" {
		return n.text
	}
	var b strings.Builder
	for _, c := range n.children {
		b.WriteString(textOf(c))
		b.WriteString(" ")
	}
	return b.String()
}

func clean(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func signalText(n *node) (label string, code string) {
	label = n.attrs["alt"] + " " + n.attrs["title"] + " " + n.attrs["aria-label"] + " " + n.attrs["data-original-title"] + " " + n.attrs["data-bs-original-title"]
	if n.attrs["class"] != "" && strings.Contains(strings.ToLower(n.attrs["class"]), "badge") {
		label += " " + textOf(n)
	}
	code = n.attrs["class"] + " " + n.attrs["src"] + " " + n.attrs["data-region"]
	return
}

func itemFor(n *node) (*node, string, string) {
	row := n.parent
	for p := n.parent; p != nil && p.tag != "#root"; p = p.parent {
		cls := strings.ToLower(p.attrs["class"])
		if p.tag == "li" || p.tag == "tr" || strings.Contains(cls, "activity") || strings.Contains(cls, "section") {
			row = p
			break
		}
	}
	if row == nil {
		return nil, "", ""
	}
	var link *node
	var find func(*node)
	find = func(x *node) {
		if link != nil {
			return
		}
		if x.tag == "a" && clean(textOf(x)) != "" {
			link = x
			return
		}
		for _, c := range x.children {
			find(c)
		}
	}
	find(row)
	name := ""
	href := ""
	if link != nil {
		name = clean(textOf(link))
		href = link.attrs["href"]
	} else {
		name = clean(textOf(row))
	}

	name = clean(notDoneText.ReplaceAllString(name, ""))
	name = strings.Trim(name, " :-–")
	if len(name) > 90 {
		name = name[:90] + "…"
	}
	return row, name, href
}

type hit struct{ name, href string }

func scan(root *node) (missed []hit, doneCount int) {
	seenRow := map[*node]bool{}
	seenName := map[string]bool{}
	var walk func(*node)
	walk = func(n *node) {
		if n.tag != "#text" && n.tag != "#root" {
			label, code := signalText(n)
			isNotDone := notDoneText.MatchString(label) || notDoneCode.MatchString(code)
			if isNotDone {
				row, name, href := itemFor(n)
				if row != nil && !seenRow[row] && name != "" && !seenName[name] {
					seenRow[row] = true
					seenName[name] = true
					missed = append(missed, hit{name, href})
				}
			} else if doneText.MatchString(label) || doneText.MatchString(code) {
				doneCount++
			}
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(root)
	return
}

var (
	numberedRe = regexp.MustCompile(`^\d+(\.\d+)*\b`)

	introRe = regexp.MustCompile(`(?i)\bintroduction\b|^week\s*\d+\b`)
	skipRe  = regexp.MustCompile(`(?i)further reading|references|acknowledg|glossary|bibliography|where next|tell us what you think|survey|copyright`)
)

func allLinks(root *node) []hit {
	var out []hit
	seen := map[string]bool{}
	var walk func(*node)
	walk = func(n *node) {
		if n.tag == "a" {
			name := clean(textOf(n))
			href := n.attrs["href"]
			if name != "" && href != "" && !seen[href] {
				seen[href] = true
				out = append(out, hit{name, href})
			}
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(root)
	return out
}

func absURL(href string) string {
	href = strings.TrimSpace(href)
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	if strings.HasPrefix(href, "/") {
		return "https://www.open.edu" + href
	}
	return href
}

func isOpenLearn(href string) bool {
	u, err := url.Parse(href)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "open.edu" || strings.HasSuffix(h, ".open.edu") || h == "open.ac.uk" || strings.HasSuffix(h, ".open.ac.uk")
}

func isQuiz(href string) bool {
	l := strings.ToLower(href)
	return strings.Contains(l, "/mod/quiz/") || strings.Contains(l, "quiz")
}

func openURL(u string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	case "darwin":
		return exec.Command("open", u).Start()
	default:
		println("would open:", u)
		return nil
	}
}
