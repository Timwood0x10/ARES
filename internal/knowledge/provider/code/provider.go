// Package code implements a GraphProvider that analyses Go source directories
// and emits functions, types, and interfaces as KnowledgeObjects.
//
// It uses the standard go/parser and go/ast packages — no external dependency.
package code

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// Object type constants used by CodeProvider.
const (
	typeStruct    knowledge.ObjectType = "struct"
	typeInterface knowledge.ObjectType = "interface"
	tagFunction                        = "function"
)

// CodeProvider scans a Go source directory and streams its symbols
// (functions, structs, interfaces) as KnowledgeObjects.
type CodeProvider struct {
	name    string
	rootDir string
}

// New creates a CodeProvider that scans rootDir for Go source files.
func New(name, rootDir string) (*CodeProvider, error) {
	if name == "" {
		return nil, fmt.Errorf("provider name is required")
	}
	if rootDir == "" {
		return nil, fmt.Errorf("root directory is required")
	}
	info, err := os.Stat(rootDir)
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", rootDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", rootDir)
	}
	return &CodeProvider{name: name, rootDir: rootDir}, nil
}

// Name returns the provider identifier.
func (p *CodeProvider) Name() string { return p.name }

// IntentMatch returns 0.7 for architecture/code intents, 0.2 otherwise.
func (p *CodeProvider) IntentMatch(intent knowledge.Intent) float64 {
	goal := strings.ToLower(intent.Goal)
	if strings.Contains(goal, "architecture") ||
		strings.Contains(goal, "code") ||
		strings.Contains(goal, "function") ||
		strings.Contains(goal, "api") ||
		strings.Contains(goal, "struct") ||
		strings.Contains(goal, "interface") ||
		strings.Contains(goal, "dependency") {
		return 0.7
	}
	return 0.2
}

// Stream parses all .go files under rootDir and emits one KnowledgeObject
// per top-level declaration (function, type, interface).
func (p *CodeProvider) Stream(ctx context.Context, intent knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
	objCh := make(chan *knowledge.KnowledgeObject, 128)
	errCh := make(chan error, 1)

	go func() {
		defer close(objCh)
		defer close(errCh)

		maxResults := intent.Scope.MaxObjects
		if maxResults <= 0 {
			maxResults = 100
		}

		fset := token.NewFileSet()
		count := 0

		err := filepath.WalkDir(p.rootDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if count >= maxResults {
				return filepath.SkipAll
			}

			// Skip vendor, node_modules, hidden dirs, and non-.go files.
			if d.IsDir() {
				name := d.Name()
				if name == "vendor" || name == "node_modules" || name == ".git" ||
					strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			src, rErr := os.ReadFile(path) //nolint:gosec // bounded by WalkDir, own source tree
			if rErr != nil {
				return nil
			}

			file, rErr := parser.ParseFile(fset, path, src, parser.ParseComments)
			if rErr != nil {
				return nil
			}

			relPath, _ := filepath.Rel(p.rootDir, path)

			for _, decl := range file.Decls {
				if count >= maxResults {
					return filepath.SkipAll
				}

				obj := p.declToObject(decl, file, relPath, fset)
				if obj == nil {
					continue
				}
				count++

				select {
				case objCh <- obj:
				case <-ctx.Done():
					return ctx.Err()
				}
			}

			return nil
		})

		if err != nil && err != context.Canceled && err != filepath.SkipAll {
			errCh <- fmt.Errorf("code provider: %w", err)
		}
	}()

	return objCh, errCh
}

// declToObject converts a Go AST declaration to a KnowledgeObject.
func (p *CodeProvider) declToObject(decl ast.Decl, file *ast.File, relPath string, fset *token.FileSet) *knowledge.KnowledgeObject {
	switch d := decl.(type) {
	case *ast.GenDecl:
		return p.genDeclToObject(d, file, relPath)
	case *ast.FuncDecl:
		return p.funcDeclToObject(d, file, relPath, fset)
	default:
		return nil
	}
}

func (p *CodeProvider) genDeclToObject(d *ast.GenDecl, file *ast.File, relPath string) *knowledge.KnowledgeObject {
	for _, spec := range d.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}

		typeName := ts.Name.Name
		summary := fmt.Sprintf("type %s defined in %s.%s", typeName, file.Name.Name, relPath)

		var objType knowledge.ObjectType
		switch ts.Type.(type) {
		case *ast.StructType:
			objType = typeStruct
		case *ast.InterfaceType:
			objType = typeInterface
		default:
			objType = knowledge.ObjectCode
		}

		return &knowledge.KnowledgeObject{
			ID:         fmt.Sprintf("%s:%s.%s", p.name, file.Name.Name, typeName),
			Type:       objType,
			Namespace:  p.name,
			Summary:    summary,
			Confidence: 0.9,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
			Tags:       []string{string(objType), file.Name.Name},
		}
	}
	return nil
}

func (p *CodeProvider) funcDeclToObject(d *ast.FuncDecl, file *ast.File, relPath string, fset *token.FileSet) *knowledge.KnowledgeObject {
	funcName := d.Name.Name
	pos := fset.Position(d.Pos())

	summary := fmt.Sprintf("func %s defined at %s:%d", funcName, relPath, pos.Line)
	if d.Doc != nil {
		doc := strings.TrimSpace(d.Doc.Text())
		if len(doc) > 0 {
			summary = doc
			if len(summary) > 200 {
				summary = summary[:200] + "..."
			}
		}
	}

	return &knowledge.KnowledgeObject{
		ID:         fmt.Sprintf("%s:%s.%s", p.name, file.Name.Name, funcName),
		Type:       knowledge.ObjectCode,
		Namespace:  p.name,
		Summary:    summary,
		Confidence: 0.9,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		Tags:       []string{tagFunction, file.Name.Name},
	}
}
