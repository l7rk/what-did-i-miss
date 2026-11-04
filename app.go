package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui.html
var uiHTML string

type item struct {
	Name string `json:"name"`
	Href string `json:"href"`
}

type result struct {
	File   string `json:"file"`
	Done   int    `json:"done"`
	Missed int    `json:"missed"`

	MissedHrefs []string `json:"missedHrefs"`
	Pages       []item   `json:"pages"`
	Quizzes     []item   `json:"quizzes"`
	Skipped     []item   `json:"skipped"`
	Other       []item   `json:"other"`
}

func analyze(src string) result {
	root := parse(src)
	missed, done := scan(root)
	r := result{Done: done, Missed: len(missed), MissedHrefs: []string{}, Pages: []item{}, Quizzes: []item{}, Skipped: []item{}, Other: []item{}}
	for _, h := range missed {
		r.MissedHrefs = append(r.MissedHrefs, absURL(h.href))
	}
	if len(missed) == 0 {
		return r
	}
	seen := map[string]bool{}
	addPage := func(h hit) {
		if !seen[h.href] {
			seen[h.href] = true
			r.Pages = append(r.Pages, item{h.name, h.href})
		}
	}

	for _, h := range allLinks(root) {
		h.href = absURL(h.href)
		if isOpenLearn(h.href) && !isQuiz(h.href) && introRe.MatchString(h.name) && !skipRe.MatchString(h.name) {
			addPage(h)
		}
	}
	for _, h := range missed {
		h.href = absURL(h.href)
		switch {
		case !isOpenLearn(h.href):
			r.Other = append(r.Other, item{h.name, h.href})
		case isQuiz(h.href):
			r.Quizzes = append(r.Quizzes, item{h.name, h.href})
		case skipRe.MatchString(h.name) || !(numberedRe.MatchString(h.name) || introRe.MatchString(h.name)):
			r.Skipped = append(r.Skipped, item{h.name, h.href})
		default:
			addPage(h)
		}
	}
	return r
}

type app struct {
	mu       sync.Mutex
	token    string
	current  result
	preload  *result
	lastSeen time.Time
	byeAt    time.Time
}

func (a *app) authed(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Token") != a.token && r.URL.Query().Get("t") != a.token {
		http.Error(w, "nope", http.StatusForbidden)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (a *app) listFor(kind string) []item {
	switch kind {
	case "quiz":
		return a.current.Quizzes
	case "skipped":
		return a.current.Skipped
	default:
		return a.current.Pages
	}
}

func (a *app) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" || r.URL.Query().Get("t") != a.token {
			http.Error(w, "Open this from the What Did I Miss app.", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, strings.Replace(uiHTML, "__TOKEN__", a.token, 1))
	})

	mux.HandleFunc("/api/preload", func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(w, r) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.preload == nil {
			writeJSON(w, nil)
			return
		}
		a.current = *a.preload
		a.preload = nil
		writeJSON(w, a.current)
	})

	mux.HandleFunc("/api/scan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !a.authed(w, r) {
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 60<<20))
		if err != nil {
			http.Error(w, "couldn't read file", 400)
			return
		}
		res := analyze(string(body))
		res.File = r.URL.Query().Get("name")
		a.mu.Lock()
		a.current = res
		a.mu.Unlock()
		writeJSON(w, res)
	})

	mux.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !a.authed(w, r) {
			return
		}
		a.mu.Lock()
		list := a.listFor(r.URL.Query().Get("kind"))
		a.mu.Unlock()
		i, err := strconv.Atoi(r.URL.Query().Get("i"))
		if err != nil || i < 0 || i >= len(list) || !isOpenLearn(list[i].Href) {
			http.Error(w, "bad item", 400)
			return
		}
		openURL(list[i].Href)
		writeJSON(w, map[string]int{"opened": 1})
	})

	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(w, r) {
			return
		}
		a.mu.Lock()
		a.lastSeen = time.Now()
		a.byeAt = time.Time{}
		a.mu.Unlock()
		w.WriteHeader(204)
	})
	mux.HandleFunc("/api/bye", func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(w, r) {
			return
		}
		a.mu.Lock()
		a.byeAt = time.Now()
		a.mu.Unlock()
		w.WriteHeader(204)
	})
	return mux
}

func (a *app) watchdog() {
	for range time.Tick(2 * time.Second) {
		a.mu.Lock()
		bye := !a.byeAt.IsZero() && time.Since(a.byeAt) > 6*time.Second

		gone := !a.lastSeen.IsZero() && time.Since(a.lastSeen) > 4*time.Minute
		a.mu.Unlock()
		if bye || gone {
			os.Exit(0)
		}
	}
}

func main() {
	tok := make([]byte, 16)
	rand.Read(tok)
	a := &app{token: hex.EncodeToString(tok)}

	if len(os.Args) > 1 {
		if data, err := os.ReadFile(os.Args[1]); err == nil {
			res := analyze(string(data))
			res.File = filepath.Base(os.Args[1])
			a.preload = &res
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println("Couldn't start:", err)
		os.Exit(1)
	}
	addr := fmt.Sprintf("http://%s/?t=%s", ln.Addr().String(), a.token)
	go a.watchdog()
	go func() {
		time.Sleep(300 * time.Millisecond)
		openURL(addr)
	}()

	go func() {
		time.Sleep(3 * time.Minute)
		a.mu.Lock()
		never := a.lastSeen.IsZero()
		a.mu.Unlock()
		if never {
			os.Exit(0)
		}
	}()
	if os.Getenv("WDIM_PRINT_URL") != "" {
		fmt.Println(addr)
	}
	http.Serve(ln, a.routes())
}
