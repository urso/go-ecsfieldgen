package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io/ioutil"
	"log"
	"strings"
	"text/template"

	wordwrap "github.com/mitchellh/go-wordwrap"
	"github.com/urso/go-ecsfieldgen/schema"
)

type config struct {
	PackageName   string
	TemplateFile  string
	OutputFile    string
	Version       string
	FormatCode    bool
	ExcludeFields []string
}

type stringsFlag []string

func (f *stringsFlag) String() string {
	return strings.Join(([]string)(*f), ",")
}

func (f *stringsFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func main() {
	log.SetFlags(0)

	cfg := config{}
	cfg.registerFlags(flag.CommandLine)
	flag.Parse()
	files := flag.Args()

	if len(files) == 0 {
		log.Fatal("No schema files given")
	}

	checkFlag("version", cfg.Version)
	if err := run(cfg, files); err != nil {
		log.Fatalf("Failed to run script: %+v", err)
	}
}

func run(cfg config, files []string) error {
	if cfg.TemplateFile == "" {
		return errors.New("no template file configured")
	}

	ignoreNames := map[string]bool{}
	for _, name := range cfg.ExcludeFields {
		ignoreNames[name] = true
	}

	schema, err := schema.LoadFromFiles(cfg.Version, files, ignoreNames)
	if err != nil {
		return fmt.Errorf("failed to load schema: %+v", err)
	}

	codeTmpl, err := ioutil.ReadFile(cfg.TemplateFile)
	if err != nil {
		return fmt.Errorf("failed to read template file '%v': %+v", cfg.TemplateFile, err)
	}

	contents, err := execTemplate(string(codeTmpl), cfg.PackageName, schema)
	if err != nil {
		return fmt.Errorf("failed to apply the code template: %+v", err)
	}

	if cfg.FormatCode {
		contents, err = format.Source(contents)
		if err != nil {
			return fmt.Errorf("failed to format code: %v", err)
		}
	}

	if cfg.OutputFile != "" {
		err := ioutil.WriteFile(cfg.OutputFile, contents, 0600)
		if err != nil {
			return fmt.Errorf("failed to write file '%v': %v", cfg.OutputFile, err)
		}
	} else {
		fmt.Printf("%s\n", contents)
	}
	return nil
}

func (c *config) registerFlags(fs *flag.FlagSet) {
	if fs == nil {
		fs = flag.CommandLine
	}

	flag.StringVar(&c.TemplateFile, "template", "", "Template file used to generate the code")
	flag.StringVar(&c.PackageName, "pkg", "ecs", "Target package name")
	flag.StringVar(&c.OutputFile, "out", "", "Output directory (required)")
	flag.StringVar(&c.Version, "version", "", "ECS version (required)")
	flag.BoolVar(&c.FormatCode, "fmt", false, "Format output")
	flag.Var((*stringsFlag)(&c.ExcludeFields), "e", "exclude fields")
}

func execTemplate(tmpl, pkgName string, schema *schema.Schema) ([]byte, error) {
	funcs := template.FuncMap{
		"goName":    goTypeName,
		"goComment": goCommentify,
	}

	// collect packages to be imported
	packages := map[string]string{}
	for _, val := range schema.Values {
		pkg := val.Type.Package
		if pkg != "" {
			packages[pkg] = pkg
		}
	}

	var buf bytes.Buffer
	t := template.Must(template.New("").Funcs(funcs).Parse(tmpl))
	err := t.Execute(&buf, map[string]interface{}{
		"packageName": pkgName,
		"packages":    packages,
		"schema":      schema,
	})
	if err != nil {
		return nil, fmt.Errorf("executing code template failed: %+v", err)
	}

	return buf.Bytes(), nil
}

func checkFlag(name, s string) {
	if s == "" {
		log.Fatalf("Error: -%v required", name)
	}
}

func goCommentify(s string) string {
	s = strings.Join(strings.Split(s, "\n"), " ")
	textLength := 75 - len(strings.Replace("", "\t", "    ", 4)+" // ")
	lines := strings.Split(wordwrap.WrapString(s, uint(textLength)), "\n")

	if len(lines) > 0 {
		// Remove empty first line.
		if strings.TrimSpace(lines[0]) == "" {
			lines = lines[1:]
		}
	}
	if len(lines) > 0 {
		// Remove empty last line.
		if strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
	}

	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}

	// remove empty lines
	for i := len(lines) - 1; i >= 0; i-- {
		if len(lines[i]) == 0 {
			lines = lines[:i]
		}
		break
	}

	for i := range lines {
		lines[i] = "// " + lines[i]
	}

	return strings.Join(lines, "\n")
}

func goTypeName(name string) string {
	var b strings.Builder
	for _, w := range strings.FieldsFunc(name, isSeparator) {
		b.WriteString(strings.Title(abbreviations(w)))
	}
	return b.String()
}

// abbreviations capitalizes common abbreviations.
func abbreviations(abv string) string {
	switch strings.ToLower(abv) {
	case "id", "ppid", "pid", "mac", "ip", "iana", "uid", "ecs", "url", "os",
		"http", "dns", "ssl", "tls", "ttl", "uuid":
		return strings.ToUpper(abv)
	default:
		return abv
	}
}

// isSeparate returns true if the character is a field name separator. This is
// used to detect the separators in fields like ephemeral_id or instance.name.
func isSeparator(c rune) bool {
	switch c {
	case '.', '_':
		return true
	case '@':
		// This effectively filters @ from field names.
		return true
	default:
		return false
	}
}
