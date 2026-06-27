// Package handler wires HTTP routes to the store and renders the UI.
package handler

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/bhoobalan/shortlink/internal/geo"
	"github.com/bhoobalan/shortlink/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// reserved slugs cannot be used as custom paths (they collide with real routes).
var reserved = map[string]bool{
	"track-urls":  true,
	"shorten":     true,
	"static":      true,
	"favicon.ico": true,
	"robots.txt":  true,
}

// slugPattern validates custom paths: 2–40 letters, digits, dash or underscore.
var slugPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{2,40}$`)

// Handler holds the dependencies shared by every route.
type Handler struct {
	store      *store.Store
	tmpl       *template.Template
	baseURL    string
	adminToken string
	css        []byte
}

// New builds a Handler. baseURL is the public origin used to render full short
// links. adminToken, when non-empty, gates the /track-urls dashboard.
func New(st *store.Store, baseURL, adminToken string) (*Handler, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	css, err := staticFS.ReadFile("static/style.css")
	if err != nil {
		return nil, err
	}
	return &Handler{
		store:      st,
		tmpl:       tmpl,
		baseURL:    strings.TrimRight(baseURL, "/"),
		adminToken: adminToken,
		css:        css,
	}, nil
}

// Routes returns the configured mux.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /static/style.css", h.styleCSS)
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("POST /shorten", h.shorten)
	mux.HandleFunc("GET /track-urls", h.guard(h.trackURLs))
	mux.HandleFunc("GET /track-urls/{slug}/logs", h.guard(h.trackLogs))
	mux.HandleFunc("GET /{slug}/qr", h.qr)
	mux.HandleFunc("GET /{slug}/stats", h.stats)
	mux.HandleFunc("GET /{slug}", h.redirect)
	return mux
}

// guard protects the dashboard. With no ADMIN_TOKEN set it's open (dev mode);
// otherwise it accepts ?token=… (and sets a cookie) or a matching cookie.
func (h *Handler) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.adminToken == "" {
			next(w, r)
			return
		}
		if t := r.URL.Query().Get("token"); t == h.adminToken {
			http.SetCookie(w, &http.Cookie{
				Name:     "bhoo_admin",
				Value:    t,
				Path:     "/track-urls",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
			next(w, r)
			return
		}
		if c, err := r.Cookie("bhoo_admin"); err == nil && c.Value == h.adminToken {
			next(w, r)
			return
		}
		http.Error(w, "unauthorized — append ?token=… to the URL", http.StatusUnauthorized)
	}
}

func (h *Handler) styleCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(h.css)
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	h.render(w, "index.html", nil)
}

// resultData is the model for the HTMX result partial.
type resultData struct {
	ShortURL string
	Slug     string
	LongURL  string
	Expires  string
	Error    string
}

// expiryLabel turns the form's expires value into a display label.
func expiryLabel(v string) string {
	switch v {
	case "1h":
		return "IN 1 HOUR"
	case "1d":
		return "IN 1 DAY"
	case "7d":
		return "IN 7 DAYS"
	case "30d":
		return "IN 30 DAYS"
	}
	return "NEVER"
}

func (h *Handler) shorten(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderResult(w, resultData{Error: "Bad request."})
		return
	}

	raw := strings.TrimSpace(r.FormValue("url"))
	if raw == "" {
		h.renderResult(w, resultData{Error: "Please enter a URL."})
		return
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		h.renderResult(w, resultData{Error: "That doesn't look like a valid URL."})
		return
	}

	custom := strings.TrimSpace(r.FormValue("custom"))
	if custom != "" {
		if !slugPattern.MatchString(custom) || reserved[strings.ToLower(custom)] {
			h.renderResult(w, resultData{Error: "Custom path must be 2–40 letters, numbers, - or _, and not a reserved word."})
			return
		}
	}

	var ttl time.Duration
	if d, ok := parseExpiry(r.FormValue("expires")); ok {
		ttl = d
	}

	link, err := h.store.Create(r.Context(), u.String(), custom, ttl)
	if errors.Is(err, store.ErrSlugTaken) {
		h.renderResult(w, resultData{Error: "That custom path is already taken. Try another."})
		return
	}
	if err != nil {
		h.renderResult(w, resultData{Error: "Something went wrong. Please try again."})
		return
	}

	h.renderResult(w, resultData{
		ShortURL: h.baseURL + "/" + link.Slug,
		Slug:     link.Slug,
		LongURL:  link.LongURL,
		Expires:  expiryLabel(r.FormValue("expires")),
	})
}

func (h *Handler) redirect(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	link, err := h.store.Get(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Capture + log the click. Done synchronously (Lambda freezes after the
	// response, so a background goroutine wouldn't reliably run) but bounded.
	ctx, cancel := context.WithTimeout(r.Context(), 2500*time.Millisecond)
	defer cancel()

	ip := clientIP(r)
	loc := geo.Lookup(ctx, ip)
	_ = h.store.LogClick(ctx, store.ClickEvent{
		Slug:      slug,
		IP:        ip,
		City:      loc.City,
		Region:    loc.Region,
		Country:   loc.Country,
		Lat:       loc.Lat,
		Lon:       loc.Lon,
		Resolved:  loc.Resolved,
		UserAgent: r.UserAgent(),
		TS:        time.Now().UnixMilli(),
	})
	_ = h.store.IncrementClicks(ctx, slug)

	http.Redirect(w, r, link.LongURL, http.StatusFound)
}

func (h *Handler) qr(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if _, err := h.store.Get(r.Context(), slug); err != nil {
		http.NotFound(w, r)
		return
	}
	png, err := qrcode.Encode(h.baseURL+"/"+slug, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "qr error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(png)
}

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	link, err := h.store.Get(r.Context(), slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.render(w, "stats.html", map[string]any{
		"Slug":     link.Slug,
		"LongURL":  link.LongURL,
		"Clicks":   link.Clicks,
		"ShortURL": h.baseURL + "/" + link.Slug,
		"Created":  time.Unix(link.CreatedAt, 0).UTC().Format("2 Jan 2006, 15:04 MST"),
	})
}

// linkView is a row in the dashboard list.
type linkView struct {
	Slug     string
	ShortURL string
	LongURL  string
	Clicks   int64
	Created  string
}

func (h *Handler) trackURLs(w http.ResponseWriter, r *http.Request) {
	links, err := h.store.ListLinks(r.Context(), 200)
	if err != nil {
		http.Error(w, "could not load links", http.StatusInternalServerError)
		return
	}
	views := make([]linkView, 0, len(links))
	for _, l := range links {
		views = append(views, linkView{
			Slug:     l.Slug,
			ShortURL: h.baseURL + "/" + l.Slug,
			LongURL:  l.LongURL,
			Clicks:   l.Clicks,
			Created:  time.Unix(l.CreatedAt, 0).UTC().Format("2 Jan 2006, 15:04"),
		})
	}
	h.render(w, "dashboard.html", map[string]any{"Links": views})
}

// clickView is a row in the logs fragment.
type clickView struct {
	Time     string
	IP       string
	City     string
	Region   string
	Country  string
	Lat      float64
	Lon      float64
	Resolved bool
	Agent    string
}

func (h *Handler) trackLogs(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if _, err := h.store.Get(r.Context(), slug); err != nil {
		http.NotFound(w, r)
		return
	}
	clicks, err := h.store.ListClicks(r.Context(), slug, 200)
	if err != nil {
		http.Error(w, "could not load logs", http.StatusInternalServerError)
		return
	}
	rows := make([]clickView, 0, len(clicks))
	for _, c := range clicks {
		rows = append(rows, clickView{
			Time:     time.UnixMilli(c.TS).UTC().Format("15:04:05 02-Jan"),
			IP:       c.IP,
			City:     c.City,
			Region:   c.Region,
			Country:  c.Country,
			Lat:      c.Lat,
			Lon:      c.Lon,
			Resolved: c.Resolved,
			Agent:    shortAgent(c.UserAgent),
		})
	}
	h.render(w, "logs.html", map[string]any{
		"Slug":     slug,
		"ShortURL": h.baseURL + "/" + slug,
		"Clicks":   rows,
	})
}

func (h *Handler) renderResult(w http.ResponseWriter, d resultData) {
	h.render(w, "result.html", d)
}

func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// clientIP extracts the visitor IP, honoring X-Forwarded-For (set by API
// Gateway / proxies) and falling back to the socket address.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// shortAgent reduces a User-Agent string to a compact "device/browser" label.
func shortAgent(ua string) string {
	if ua == "" {
		return "unknown"
	}
	device := "Desktop"
	if strings.Contains(ua, "Mobile") || strings.Contains(ua, "Android") || strings.Contains(ua, "iPhone") {
		device = "Mobile"
	}
	browser := "Unknown"
	switch {
	case strings.Contains(ua, "Edg"):
		browser = "Edge"
	case strings.Contains(ua, "Chrome"):
		browser = "Chrome"
	case strings.Contains(ua, "Firefox"):
		browser = "Firefox"
	case strings.Contains(ua, "Safari"):
		browser = "Safari"
	case strings.Contains(ua, "curl"):
		browser = "curl"
	}
	return device + "/" + browser
}

// parseExpiry maps the form's expiry option to a duration.
func parseExpiry(s string) (time.Duration, bool) {
	switch s {
	case "1h":
		return time.Hour, true
	case "1d":
		return 24 * time.Hour, true
	case "7d":
		return 7 * 24 * time.Hour, true
	case "30d":
		return 30 * 24 * time.Hour, true
	}
	return 0, false
}
