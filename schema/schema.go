package schema

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
)

type Schema struct {
	Version    string
	Base       map[string]*Value     // toplevel values
	Top        map[string]*Namespace // toplevel namespaces
	Namespaces map[string]*Namespace // all namespaces with full name
	Values     map[string]*Value     // all values with full name in schema
}

type Namespace struct {
	Parent *Namespace

	Name        string
	FlatName    string
	Description string

	Children []*Namespace
	Values   []*Value
}

type Value struct {
	Parent      *Namespace
	Type        TypeInfo
	Name        string
	FlatName    string
	Description string
}

type TypeInfo struct {
	Package     string
	Name        string
	Constructor string
}

// Definition represent in yaml file field specifications.
type Definition struct {
	Name        string
	Type        string
	Description string
	Fields      map[string]Definition
}

var (
	boolType  = TypeInfo{Name: "bool", Constructor: "Bool"}
	strType   = TypeInfo{Name: "string", Constructor: "String"}
	intType   = TypeInfo{Name: "int", Constructor: "Int"}
	longType  = TypeInfo{Name: "int64", Constructor: "Int64"}
	floatType = TypeInfo{Name: "float64", Constructor: "Float64"}
	dateType  = TypeInfo{Package: "time", Name: "time.Time", Constructor: "Time"}
	durType   = TypeInfo{Package: "time", Name: "time.Duration", Constructor: "Dur"}
	objType   = TypeInfo{Name: "map[string]interface{}", Constructor: "Any"}
	ipType    = TypeInfo{Name: "string", Constructor: "String"}
	geoType   = TypeInfo{Name: "string", Constructor: "String"}
)

func LoadFromFiles(version string, paths []string, exclude map[string]bool) (*Schema, error) {
	defs, err := loadDefs(paths)
	if err != nil {
		return nil, err
	}

	schema := buildSchema(version, flattenDefs("", defs), exclude)
	copyDescriptions(schema, "", defs)
	return schema, nil
}

func loadDefs(paths []string) (map[string]Definition, error) {
	var files []string

	for _, path := range paths {
		stat, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("failed to access '%v': %+v", path, err)
		}

		if !stat.IsDir() {
			files = append(files, path)
			continue
		}

		local, err := filepath.Glob(filepath.Join(path, "*.yml"))
		if err != nil {
			return nil, fmt.Errorf("finding yml files in '%v' failed: %+v", path, err)
		}
		files = append(files, local...)
	}

	// load definitions
	defs := map[string]Definition{}
	for _, file := range files {
		contents, err := ioutil.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("error reading file %v: %+v", file, err)
		}

		var fileDefs map[string]Definition
		if err := yaml.Unmarshal(contents, &fileDefs); err != nil {
			return nil, fmt.Errorf("error parsing file %v: %+v", file, err)
		}

		for k, v := range fileDefs {
			defs[k] = v
		}
	}

	return defs, nil
}

func flattenDefs(path string, in map[string]Definition) map[string]TypeInfo {
	filtered := map[string]TypeInfo{}
	for fldPath, fld := range in {
		if path != "" {
			fldPath = fmt.Sprintf("%v.%v", path, fldPath)
		}

		if fld.Type != "group" {
			filtered[fldPath] = getType(fld.Type, fldPath)
		}

		for k, v := range flattenDefs(fldPath, fld.Fields) {
			filtered[k] = v
		}
	}
	return filtered
}

func buildSchema(version string, defs map[string]TypeInfo, exclude map[string]bool) *Schema {
	s := &Schema{
		Version:    version,
		Base:       map[string]*Value{},
		Top:        map[string]*Namespace{},
		Namespaces: map[string]*Namespace{},
		Values:     map[string]*Value{},
	}

	for fullName, ti := range defs {
		if exclude[fullName] {
			continue
		}

		fullName = normalizePath(fullName)
		name, path := splitPath(fullName)
		isBase := path == "base" || path == ""

		var current *Namespace
		val := &Value{
			Type:     ti,
			Name:     name,
			FlatName: fullName,
		}

		if isBase {
			if exclude[name] {
				continue
			}

			s.Base[name] = val
			s.Values[name] = val
		} else {
			s.Values[fullName] = val
		}

		// iterate backwards through fully qualified and build namespaces.
		// Namespaces and values get dynamically interlinked
		for path != "" {
			fullPath := path
			name, path = splitPath(path)

			ns := s.Namespaces[fullPath]
			newNS := ns == nil
			if newNS {
				ns = &Namespace{
					Name:     name,
					FlatName: fullPath,
				}
				s.Namespaces[fullPath] = ns
			}

			if val != nil {
				// first parent namespace. Let's add the value and reset, so it won't be added to another namespace
				val.Parent = ns
				ns.Values = append(ns.Values, val)
				val = nil
			}
			if current != nil && current.Parent == nil {
				// was new namespace, lets insert and link it
				current.Parent = ns
				ns.Children = append(ns.Children, current)
			}

			if !newNS { // we found a common ancestor in the tree, let's stop early
				current = nil
				break
			}

			current = ns // advance to parent namespace
		}

		if current != nil {
			// new top level namespace:
			s.Top[current.Name] = current
		}
	}

	return s
}

func copyDescriptions(schema *Schema, root string, defs map[string]Definition) {
	for fqName, def := range defs {
		if root != "" {
			fqName = fmt.Sprintf("%v.%v", root, fqName)
		}

		path := normalizePath(fqName)
		if path != "" && def.Description != "" {
			if def.Type == "group" {
				ns := schema.Namespaces[path]
				if ns == nil {
					panic(fmt.Sprintf("no namespace for: %v", path))
				}

				ns.Description = def.Description
			} else {
				val, ok := schema.Values[path]
				if !ok {
					continue
				}
				if val == nil {
					panic(fmt.Sprintf("no value for: %v", path))
				}

				val.Description = def.Description
			}
		}

		copyDescriptions(schema, fqName, def.Fields)
	}
}

func splitPath(in string) (name, parent string) {
	idx := strings.LastIndexByte(in, '.')
	if idx < 0 {
		return in, ""
	}

	return in[idx+1:], in[:idx]
}

func normalizePath(in string) string {
	var rootPaths = []string{"base"}

	for _, path := range rootPaths {
		if in == path {
			return ""
		}
		if strings.HasPrefix(in, path) && len(in) > len(path) && in[len(path)] == '.' {
			return in[len(path)+1:]
		}
	}
	return in
}

func getType(typ, name string) TypeInfo {
	switch typ {
	case "keyword", "text":
		return strType
	case "bool", "boolean":
		return boolType
	case "integer":
		return intType
	case "long":
		return longType
	case "float":
		return floatType
	case "date":
		return dateType
	case "duration":
		return durType
	case "object":
		return objType
	case "ip":
		return ipType
	case "geo_point":
		return geoType
	default:
		panic(fmt.Sprintf("unknown type '%v' in field '%v'", typ, name))
	}
}
