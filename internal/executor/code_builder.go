package executor

import (
	"fmt"
	"log/slog"
	"strings"
)

const (
	DefaultMaxCodeLength = 1000

	goCodePlaceHolder   = "// USER_CODE_HERE"
	goImportPlaceHolder = "// IMPORTS_HERE"

	pythonCodePlaceHolder = "# USER_CODE_HERE"

	jsCodePlaceHolder = "// USER_CODE_HERE"

	tempFileDirHolder  = "{{temp_file_dir}}"
	tempFileNameHolder = "{{temp_file_name}}"
)

type LanguagePlaceHolder struct {
	codePlaceHolder   string
	importPlaceHolder string
}

func getLanguagePlaceHolder(lang string) LanguagePlaceHolder {
	var result LanguagePlaceHolder
	switch lang {
	case "Golang":
		result = LanguagePlaceHolder{
			codePlaceHolder:   goCodePlaceHolder,
			importPlaceHolder: goImportPlaceHolder,
		}
	case "Python":
		result = LanguagePlaceHolder{
			codePlaceHolder:   pythonCodePlaceHolder,
			importPlaceHolder: "",
		}
	case "Javascript":
		result = LanguagePlaceHolder{
			codePlaceHolder:   jsCodePlaceHolder,
			importPlaceHolder: "",
		}
	}

	return result
}

type CodeBuilder interface {
	Build(lang, driverCode, userCode string) (string, error)
}

// ConcreteCodeBuilder is a struct for building complete and functionable code.
type ConcreteCodeBuilder struct {
	// store a map of package analyzers for future compiled languages integration
	pkgAnalyzers map[string]PackageAnalyzer
	logger       *slog.Logger
}

func NewCodeBuilder(pkgAnalyzer []PackageAnalyzer, logger *slog.Logger) CodeBuilder {
	b := ConcreteCodeBuilder{
		pkgAnalyzers: make(map[string]PackageAnalyzer),
		logger:       logger,
	}

	for _, analyzer := range pkgAnalyzer {
		b.pkgAnalyzers[analyzer.GetNormalizedName()] = analyzer
	}

	return &b
}

// Build will generate the code after sanitizing, dynamically imports, and combining driverCode, userCode.
func (c *ConcreteCodeBuilder) Build(lang, driverCode, userCode string) (string, error) {
	// Sanitize code
	err := Sanitize(userCode, lang, DefaultMaxCodeLength)
	if err != nil {
		return "", err
	}

	// only generate imports for compiled languages
	placeholder := getLanguagePlaceHolder(lang)

	finalCode := strings.Replace(driverCode, placeholder.codePlaceHolder, userCode, 1)
	c.logger.Info("User code added to Driver code", "final_code", finalCode)

	var imports string
	if analyzer := c.pkgAnalyzers[lang]; analyzer != nil {
		pkgs, err := analyzer.Analyze(finalCode)
		if err != nil && err == ErrParsed {
			c.logger.Error("Wrong syntax")
			return "", ErrParsed
		}

		imports = c.generateImports(pkgs)
		c.logger.Info("imports generated", "imports", imports)

		finalCode = strings.Replace(finalCode, placeholder.importPlaceHolder, imports, 1)
	}

	// combining altogether
	c.logger.Info("Code built", "final_code", finalCode)

	return finalCode, nil
}

func (c *ConcreteCodeBuilder) generateImports(pkgs map[string]bool) string {
	if len(pkgs) == 0 {
		return ""
	}

	var builder strings.Builder

	builder.WriteString("import (\n")

	for pkg := range pkgs {
		builder.WriteString(fmt.Sprintf(" \t\"%s\"\n ", pkg))
	}

	builder.WriteString(")")

	return builder.String()
}
