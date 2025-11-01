package executor

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
)

var ErrParsed error = errors.New("Failed to analyze code.")

// PackageAnalyzer helps finding which packages are being imported in a piece of code
type PackageAnalyzer interface {
	GetNormalizedName() string
	Analyze(code string) (map[string]bool, error)
}

type GoPackageAnalyzer struct {
	lang string
}

func NewGoPackageAnalyzer() *GoPackageAnalyzer {
	lang, _ := NormalizeLanguage("golang")
	return &GoPackageAnalyzer{
		lang: lang,
	}
}

func (p *GoPackageAnalyzer) Analyze(code string) (map[string]bool, error) {
	// a map to store which pkgs to import
	pkgs := make(map[string]bool)
	fset := token.NewFileSet()

	// 0 means parse everything
	node, err := parser.ParseFile(fset, "code.go", code, 0)
	if err != nil {
		return nil, ErrParsed
	}

	// this find all the packages that are EXPLICITLY imported (import statement)
	for _, i := range node.Imports {
		// strip the double quote ("fmt", "strings")
		p := i.Path.Value[1 : len(i.Path.Value)-1]
		pkgs[p] = true
	}

	// this find all the packages that are IMPLICITLY imported (not appeard in import statement)
	ast.Inspect(node, func(n ast.Node) bool {
		selExpr, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true // continue traversing
		}

		ident, ok := selExpr.X.(*ast.Ident)
		if !ok {
			return true
		}

		pkgs[ident.Name] = true
		return true
	})

	return pkgs, nil
}

func (p *GoPackageAnalyzer) GetNormalizedName() string {
	return p.lang
}
