package tools

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// CodeDefinition represents a named code construct extracted from a source file.
type CodeDefinition struct {
	Name string
	Type string
	Line int
}

// ListCodeDefinitions extracts function, method, type, and constant names
// from a Go source file without reading the full content into context.
func ListCodeDefinitions(workspace, rawPath string) ([]CodeDefinition, error) {
	abs, err := ResolveAndValidatePath(workspace, rawPath)
	if err != nil {
		return nil, err
	}
	if filepath.Ext(abs) != ".go" {
		return nil, fmt.Errorf("list_code_definitions only supports Go files")
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Go file: %w", err)
	}

	var defs []CodeDefinition

	ast.Inspect(f, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.FuncDecl:
			name := decl.Name.Name
			typeStr := "function"
			if decl.Recv != nil {
				recvType := "unknown"
				if ident, ok := decl.Recv.List[0].Type.(*ast.Ident); ok {
					recvType = ident.Name
				} else if star, ok := decl.Recv.List[0].Type.(*ast.StarExpr); ok {
					if ident, ok := star.X.(*ast.Ident); ok {
						recvType = ident.Name
					}
				}
				typeStr = "method (" + recvType + ")"
			}
			defs = append(defs, CodeDefinition{
				Name: name,
				Type: typeStr,
				Line: fset.Position(decl.Pos()).Line,
			})
		case *ast.TypeSpec:
			typeStr := "type"
			switch decl.Type.(type) {
			case *ast.StructType:
				typeStr = "struct"
			case *ast.InterfaceType:
				typeStr = "interface"
			}
			defs = append(defs, CodeDefinition{
				Name: decl.Name.Name,
				Type: typeStr,
				Line: fset.Position(decl.Pos()).Line,
			})
		case *ast.ValueSpec:
			for _, name := range decl.Names {
				vtype := "var"
				if decl.Values == nil {
					vtype = "const"
				}
				defs = append(defs, CodeDefinition{
					Name: name.Name,
					Type: vtype,
					Line: fset.Position(name.Pos()).Line,
				})
			}
		}
		return true
	})

	if len(defs) == 0 {
		return nil, fmt.Errorf("no definitions found")
	}
	return defs, nil
}

// FormatCodeDefinitions formats code definitions as a string for tool output.
func FormatCodeDefinitions(defs []CodeDefinition) string {
	var sb strings.Builder
	for _, d := range defs {
		sb.WriteString(fmt.Sprintf("  %s  %s  (line %d)\n", d.Type, d.Name, d.Line))
	}
	return strings.TrimRight(sb.String(), "\n")
}
