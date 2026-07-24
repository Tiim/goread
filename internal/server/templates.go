package server

import (
	"embed"
	"html/template"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

func parseTemplates() *template.Template {
	return template.Must(template.ParseFS(templateFS, "templates/*.html"))
}
