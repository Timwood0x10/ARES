package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"text/template"

	"goagentx/internal/errors"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// TemplateEngine handles prompt templates.
type TemplateEngine struct {
	funcs map[string]interface{}
	mu    sync.RWMutex
}

// NewTemplateEngine creates a new TemplateEngine.
func NewTemplateEngine() *TemplateEngine {
	titleCase := cases.Title(language.English)
	return &TemplateEngine{
		funcs: map[string]interface{}{
			"upper":   strings.ToUpper,
			"lower":   strings.ToLower,
			"title":   func(s string) string { return titleCase.String(s) },
			"trim":    strings.TrimSpace,
			"join":    strings.Join,
			"json":    toJSON,
			"to_yaml": toYAML,
		},
	}
}

// Render renders a template string with given data.
func (e *TemplateEngine) Render(tmpl string, data interface{}) (string, error) {
	e.mu.RLock()
	funcs := make(map[string]interface{}, len(e.funcs))
	for k, v := range e.funcs {
		funcs[k] = v
	}
	e.mu.RUnlock()

	t, err := template.New("prompt").Funcs(template.FuncMap(funcs)).Parse(tmpl)
	if err != nil {
		return "", errors.Wrap(err, "parse template")
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", errors.Wrap(err, "execute template")
	}

	return buf.String(), nil
}

// RenderFile renders a template file with given data.
func (e *TemplateEngine) RenderFile(path string, data interface{}) (string, error) {
	e.mu.RLock()
	funcs := make(map[string]interface{}, len(e.funcs))
	for k, v := range e.funcs {
		funcs[k] = v
	}
	e.mu.RUnlock()

	tmpl, err := template.New(path).Funcs(template.FuncMap(funcs)).ParseFiles(path)
	if err != nil {
		return "", errors.Wrap(err, "parse template file")
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", errors.Wrap(err, "execute template")
	}

	return buf.String(), nil
}

// RegisterFunc registers a custom function.
func (e *TemplateEngine) RegisterFunc(name string, fn interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.funcs[name] = fn
}

// Common templates.
var (
	// RecommendationPrompt is the prompt for generating recommendations.
	RecommendationPrompt = `You are a knowledgeable assistant. Analyze the user's preferences and provide personalized recommendations.

User Profile:
- Gender: {{.Gender}}
- Age: {{.Age}}
- Preferences: {{.StylePreferences}}
- Budget Range: ${{.BudgetMin}} - ${{.BudgetMax}}
- Favorite Categories: {{.FavoriteColors}}
- Favorite Options: {{.FavoriteBrands}}
- Context: {{.Occasion}}
- Season: {{.Season}}

Provide a JSON response with the following schema:
{{.Schema}}

Focus on:
1. Matching user preferences
2. Considering the context
3. Staying within budget
4. Providing diverse options`

	// ProfileExtractionPrompt extracts user profile from natural language.
	ProfileExtractionPrompt = `Extract user profile information from the following text:

{{.Input}}

Extract and return a JSON object with these fields:
- user_id (optional)
- gender (male/female/other)
- age (number)
- style_preferences (array of strings)
- budget_min (number)
- budget_max (number)
- favorite_colors (array)
- favorite_brands (array)
- body_type (string)
- occupation (string)
- location (string)

If a field is not mentioned, omit it from the JSON.`

	// StyleAnalysisPrompt analyzes style preferences.
	StyleAnalysisPrompt = `Analyze the following style description and identify key characteristics:

{{.Description}}

Return a JSON object with:
- primary_style (e.g., casual, formal, sporty, bohemian)
- secondary_styles (array)
- color_palette (array of hex colors)
- silhouette_preference (e.g., slim, loose, fitted)
- key_elements (array of strings)`
)

// RenderRecommendation renders the recommendation prompt.
func (e *TemplateEngine) RenderRecommendation(data map[string]interface{}) (string, error) {
	schema, err := GetRecommendResultSchema().ToJSONString()
	if err != nil {
		return "", fmt.Errorf("failed to serialize schema: %w", err)
	}
	data["Schema"] = schema
	return e.Render(RecommendationPrompt, data)
}

// RenderProfileExtraction renders the profile extraction prompt.
func (e *TemplateEngine) RenderProfileExtraction(input string) (string, error) {
	return e.Render(ProfileExtractionPrompt, map[string]string{
		"Input": input,
	})
}

// RenderStyleAnalysis renders the style analysis prompt.
func (e *TemplateEngine) RenderStyleAnalysis(description string) (string, error) {
	return e.Render(StyleAnalysisPrompt, map[string]string{
		"Description": description,
	})
}

// PromptTemplate defines a named, reusable prompt template with metadata.
type PromptTemplate struct {
	Name        string   // Unique template name used as a registry key.
	Description string   // Human-readable description of the template purpose.
	Template    string   // Go text/template source string.
	Variables   []string // Expected variable names for documentation and validation.
}

// TemplateRegistry stores and retrieves PromptTemplate instances by name.
type TemplateRegistry struct {
	templates map[string]*PromptTemplate
	mu        sync.RWMutex
}

// NewTemplateRegistry creates an empty TemplateRegistry.
func NewTemplateRegistry() *TemplateRegistry {
	return &TemplateRegistry{
		templates: make(map[string]*PromptTemplate),
	}
}

// Register adds a template to the registry. Returns an error if the name is
// empty, the template source is empty, or a template with the same name
// already exists.
func (r *TemplateRegistry) Register(tmpl *PromptTemplate) error {
	if tmpl == nil {
		return fmt.Errorf("template must not be nil")
	}
	if tmpl.Name == "" {
		return fmt.Errorf("template name must not be empty")
	}
	if tmpl.Template == "" {
		return fmt.Errorf("template source must not be empty for %q", tmpl.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.templates[tmpl.Name]; exists {
		return fmt.Errorf("template %q already registered", tmpl.Name)
	}

	// Store a copy to prevent external mutation.
	cp := *tmpl
	r.templates[tmpl.Name] = &cp
	return nil
}

// Get retrieves a template by name. Returns nil and false if not found.
func (r *TemplateRegistry) Get(name string) (*PromptTemplate, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tmpl, ok := r.templates[name]
	return tmpl, ok
}

// List returns all registered templates, sorted by insertion order is not
// guaranteed. The returned slice is a copy; callers may modify it freely.
func (r *TemplateRegistry) List() []*PromptTemplate {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*PromptTemplate, 0, len(r.templates))
	for _, tmpl := range r.templates {
		result = append(result, tmpl)
	}
	return result
}

// Render looks up a template by name, parses and executes it with the given
// variables, and returns the result. Returns an error if the template is not
// found or if execution fails.
func (r *TemplateRegistry) Render(name string, vars map[string]string) (string, error) {
	r.mu.RLock()
	tmpl, ok := r.templates[name]
	r.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("template %q not found", name)
	}

	engine := NewTemplateEngine()
	return engine.Render(tmpl.Template, vars)
}

// Helper functions.
func toJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func toYAML(v interface{}) string {
	// TODO: implement proper YAML serialization (expected by 2026-07-01).
	return fmt.Sprintf("%v", v)
}

// Global default template engine.
var defaultEngine = NewTemplateEngine()

// RenderWithDefault renders using the default engine.
func RenderWithDefault(tmpl string, data interface{}) (string, error) {
	return defaultEngine.Render(tmpl, data)
}

// RenderRecommendationWithDefault renders recommendation prompt.
func RenderRecommendationWithDefault(data map[string]interface{}) (string, error) {
	return defaultEngine.RenderRecommendation(data)
}

// RenderProfileExtractionWithDefault renders profile extraction prompt.
func RenderProfileExtractionWithDefault(input string) (string, error) {
	return defaultEngine.RenderProfileExtraction(input)
}

// RenderStyleAnalysisWithDefault renders style analysis prompt.
func RenderStyleAnalysisWithDefault(description string) (string, error) {
	return defaultEngine.RenderStyleAnalysis(description)
}
