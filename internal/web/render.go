package web

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Renderer struct {
	templates map[string]*template.Template
}

// templateFuncs are available in every page template.
var templateFuncs = template.FuncMap{
	"sub": func(a, b int) int { return a - b },
}

func NewRenderer() (*Renderer, error) {
	pages := []string{"home", "import", "cards", "review", "stats", "card_edit", "settings", "chat"}
	r := &Renderer{templates: make(map[string]*template.Template)}
	for _, p := range pages {
		tmpl, err := template.New(p).Funcs(templateFuncs).ParseFS(
			templateFS, "templates/layout.html", "templates/"+p+".html")
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", p, err)
		}
		r.templates[p] = tmpl
	}
	return r, nil
}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) {
	t, ok := r.templates[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		_, _ = io.WriteString(w, "<p>template error: "+template.HTMLEscapeString(err.Error())+"</p>")
	}
}

// RenderPartial renders a named sub-template from a page (e.g. the "card-area"
// fragment inside "review"). Used for HTMX partial swaps.
func (r *Renderer) RenderPartial(w http.ResponseWriter, page, partial string, data any) {
	t, ok := r.templates[page]
	if !ok {
		http.Error(w, "template not found: "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, partial, data); err != nil {
		_, _ = io.WriteString(w, "<p>template error: "+template.HTMLEscapeString(err.Error())+"</p>")
	}
}

// StaticFS returns the embedded static filesystem rooted at static/.
func StaticFS() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
