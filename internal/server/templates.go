package server

import (
	"embed"
	"fmt"
	"html/template"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// dict builds a map for passing multiple named values into a sub-template
// invocation (html/template pipelines only support a single argument).
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %v is not a string", pairs[i])
		}
		m[key] = pairs[i+1]
	}
	return m, nil
}

func parseTemplates() *template.Template {
	return template.Must(template.New("").Funcs(template.FuncMap{"dict": dict}).ParseFS(templateFS, "templates/*.html"))
}
