package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode"

	flag "github.com/spf13/pflag"
)

type config struct {
	targetFile    string
	sourceFiles   []string
	targetPackage string
	fileTypeMap   map[string]string
	imports       []string
	replaceTypes  map[string]string
	typesForItems map[string]string
}

// ErrBadUsage should be raised when flags were improperly ivoked
type ErrBadUsage struct {
	err error
}

func (err *ErrBadUsage) Error() string {
	return err.Error()
}

func (err *ErrBadUsage) Unwrap() error {
	return err.err
}

var _ error = &ErrBadUsage{}

func parseFlags() (*config, error) {
	c := &config{}

	flag.CommandLine.StringVar(&c.targetFile, "target", "", "path to the go file where structs will be created. If none provided stdout will be used.")
	flag.CommandLine.StringVar(&c.targetPackage, "package", "main", "the package of the module where the structs will live.")
	flag.CommandLine.StringSliceVar(&c.sourceFiles, "source", []string{}, "list of files to use as source, wildcards are valid (such as *.json) but need to be quote wrapped.")
	flag.CommandLine.StringToStringVar(&c.fileTypeMap, "structnames", map[string]string{}, "alternative struct names for types, only full matches will be replaced use either comma separated match=replacement or pass this flag multiple times, the names before capitalization are considered for the match. ie `issuetype=someotherstructname`")
	flag.CommandLine.StringSliceVar(&c.imports, "imports", []string{}, "imports to be added")
	flag.CommandLine.StringToStringVar(&c.replaceTypes, "replacetypes", map[string]string{}, "replace basic types with your own, only full matching with the type name is done, remember to add them to imports if they depend on external packages. ie `float64=float32`")
	flag.CommandLine.StringToStringVar(&c.typesForItems, "typesforitems", map[string]string{}, "replace types of struct members specifying the path. ie `StructName.Member=package.CustomType` ")

	if err := flag.CommandLine.Parse(os.Args); err != nil {
		return nil, &ErrBadUsage{err: err}
	}
	return c, nil
}

func main() {
	if err := realMain(); err != nil {
		fmt.Printf("FAILED: %v\n", err)
		var badUsage *ErrBadUsage
		if errors.As(err, &badUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func realMain() error {
	c, err := parseFlags()
	if err != nil {
		return fmt.Errorf("flags step: %w", err)
	}
	m, err := jsonIntoMap(c)
	if err != nil {
		return fmt.Errorf("reading files into maps: %w", err)
	}
	ts, err := typesFromMap(c, m)
	if err != nil {
		return fmt.Errorf("crafting types: %w", err)
	}
	var out io.Writer
	if c.targetFile != "" {
		f, err := os.Create(c.targetFile)
		if err != nil {
			return fmt.Errorf("creating output file: %w", err)
		}
		out = f
	} else {
		out = os.Stdout
	}
	makeMeCode(c, ts, out)
	return nil
}

func jsonIntoMap(c *config) (map[string][]interface{}, error) {
	expanded := make([]string, 0, len(c.sourceFiles))
	for _, sf := range c.sourceFiles {
		g, err := filepath.Glob(sf)
		if err != nil {
			expanded = append(expanded, sf)
			continue
		}
		expanded = append(expanded, g...)
		for _, e := range g {
			fmt.Printf("Found file: %s\n", e)
		}
	}

	result := map[string][]interface{}{}
	for _, f := range expanded {
		var tgt interface{}
		fp, err := os.Open(f)
		if err != nil {
			return nil, fmt.Errorf("opening json file: %w", err)
		}
		if err := json.NewDecoder(fp).Decode(&tgt); err != nil {
			return nil, fmt.Errorf("decoding file contents: %w", err)
		}
		switch t := tgt.(type) {
		case map[string]interface{}:
			result[f] = []interface{}{t}
		case []interface{}:
			result[f] = t
		case string: // yeah, valid but cmoon
			result[f] = []interface{}{t}
		default:
			return nil, fmt.Errorf("the json is %T and I have no clue what to do with it", t)
		}
	}
	return result, nil
}

func typesFromMap(c *config, m map[string][]interface{}) (map[string]map[string]maybeType, error) {
	types := map[string]map[string]maybeType{}
	for tn, t := range m {
		for _, tf := range t {
			switch field := tf.(type) {
			case map[string]interface{}:
				fileName := filepath.Base(tn)
				t, err := unWrapMap(c, field, fileName, types)
				if err != nil {
					return nil, fmt.Errorf("unwrapping json types: %w", err)
				}
				parts := strings.Split(fileName, ".")
				name := parts[0]
				typeExists(name, "topLevel", c, t, types)
			default:
				// not sure what to do here
				fmt.Printf("type of field (%T) %v\n", tf, tf)
			}
		}
	}
	return types, nil
}

func unWrapMap(c *config, m map[string]interface{}, name string, typeMap map[string]map[string]maybeType) (map[string]maybeType, error) {
	aType := map[string]maybeType{}
	for fn, f := range m {
		var it = maybeType{}
		switch field := f.(type) {
		case map[string][]interface{}:
			// TODO handle this type (it is rather uncommon)
			continue
		case []interface{}:
			// Have no clue what this is
			it.isArray = true
			if len(field) == 0 {
				it.nameOftype = "interface{}"
				break
			}
			switch innerField := field[0].(type) {
			case map[string]interface{}:
				uit, err := unWrapMap(c, innerField, fn, typeMap)
				if err != nil {
					return nil, fmt.Errorf("unwrapping type %s: %w", fn, err)
				}

				tName, _ := typeExists(fn, name, c, uit, typeMap)
				it.nameOftype = tName
			default:
				it.typeOf = reflect.TypeOf(innerField)
			}

		case map[string]interface{}:
			uit, err := unWrapMap(c, field, fn, typeMap)
			if err != nil {
				return nil, fmt.Errorf("unwrapping type %s: %w", fn, err)
			}
			tName, _ := typeExists(fn, name, c, uit, typeMap)
			it.nameOftype = tName
		default:
			it.typeOf = reflect.TypeOf(f)
		}
		aType[fn] = it
	}
	return aType, nil
}

func normalizeNames(name, pkgName string) string {
	newName := make([]rune, 0, len(name)*2) // worse case scenario there are all capitals
	for i, r := range name {
		rr := rune(r)
		if unicode.IsUpper(rr) {
			rr = unicode.ToLower(rr)
			if i > 0 { // first can be safely lowercased without prepending _
				newName = append(newName, '_')
			}
		}
		newName = append(newName, rr)

	}
	normalized := string(newName)
	// prevent go lint stuttering type name warning
	if strings.HasPrefix(strings.ToLower(name), strings.ToLower(pkgName)) && len(name) != len(pkgName) {
		normalized = normalized[len(pkgName):]
	}
	return normalized
}

func typeExists(name, parent string, c *config, ours map[string]maybeType, typeMap map[string]map[string]maybeType) (string, bool) {
	foundName := name
	fmt.Printf("looking for type: %s\n", foundName)
	newName, ok := c.fileTypeMap[foundName]
	if ok {
		foundName = newName
		fmt.Printf("renamed to: %s\n", foundName)
	}
	foundName = normalizeNames(foundName, c.targetPackage)
	fmt.Printf("normalized to: %s\n", foundName)
	existing, exists := typeMap[foundName]
	if !exists {
		for k := range typeMap {
			parts := strings.Split(k, ".")
			if parts[len(parts)-1] == foundName {
				existing = typeMap[k]
				foundName = k
				fmt.Printf("it exists parented: %s\n", foundName)
				exists = true
				break
			}
		}
		if !exists {
			fmt.Println("it's new")
			typeMap[foundName] = ours
			return foundName, false
		}
	}

	missing := map[string]maybeType{}
	for k, v := range existing {
		vo, ok := ours[k]
		if !ok {
			continue
		}
		if !v.Equals(&vo) {
			newName := fmt.Sprintf("%s.%s", parent, foundName)
			typeMap[newName] = ours
			return newName, false
		}
	}

	for k, v := range ours {
		vo, ok := existing[k]
		if !ok {
			missing[k] = ours[k]
			continue
		}
		if !v.Equals(&vo) {
			newName := fmt.Sprintf("%s.%s", parent, foundName)
			typeMap[newName] = ours
			return newName, false
		}
	}
	for k := range missing {
		existing[k] = missing[k]
	}
	typeMap[foundName] = existing
	return foundName, true
}

type maybeType struct {
	isArray    bool
	typeOf     reflect.Type
	nameOftype string
}

func (m *maybeType) Resolve() (string, string) {
	if m.typeOf == nil {
		n := capitalize(m.nameOftype)
		if m.isArray {
			n = "[]" + n
		}
		return "", n
	}
	return m.typeOf.PkgPath(), m.typeOf.Name()
}

func (m *maybeType) Equals(mt *maybeType) bool {
	if m.typeOf != nil && mt.typeOf != nil {
		return m.typeOf.Name() == mt.typeOf.Name()
	}
	if m.nameOftype != "" && mt.nameOftype != "" {
		return m.nameOftype == mt.nameOftype
	}
	return false
}

func capitalize(s string) string {
	if s == "interface{}" {
		return s
	}
	// . is likely a parented type
	s = strings.Replace(s, ".", "_", -1)
	s = strings.Replace(s, "-", "_", -1)
	s = strings.Replace(s, "\\", "_", -1)
	parts := strings.Split(s, "_")
	for i, p := range parts {
		pl := strings.ToLower(p)
		switch pl {
		case "url":
			p = "URL"
		case "id":
			p = "ID"
		case "json":
			p = "JSON"
		case "html":
			p = "HTML"
		}

		for _, s := range []string{"url", "id", "html"} {
			if strings.HasSuffix(pl, s) {
				p = p[:len(p)-len(s)] + strings.ToUpper(s)
			}
			if strings.HasPrefix(pl, s) {
				p = strings.ToUpper(s) + p[len(s):]
			}
		}

		parts[i] = strings.Title(p)
	}
	return strings.Join(parts, "")
}

func makeMeCode(c *config, typeMap map[string]map[string]maybeType, out io.Writer) {
	heading := &strings.Builder{}
	heading.WriteString(fmt.Sprintf("package %s\n", c.targetPackage))
	imports := map[string]bool{}
	code := &strings.Builder{}
	typeNames := make([]string, 0, len(typeMap))
	for tk := range typeMap {
		typeNames = append(typeNames, tk)
	}
	sort.Strings(typeNames)
	for _, tk := range typeNames {
		tvs := typeMap[tk]
		fieldNames := make([]string, 0, len(tvs))
		for tn := range tvs {
			fieldNames = append(fieldNames, tn)
		}
		sort.Strings(fieldNames)
		structName := capitalize(tk)
		code.WriteString(fmt.Sprintf("// %s is auto generated by github.com/perrito666/LAC from a json file\n", structName))
		code.WriteString(fmt.Sprintf("type %s struct {\n", structName))
		for _, fn := range fieldNames {
			f := tvs[fn]
			pkg, tn := f.Resolve()
			if pkg != "" {
				imports[pkg] = true
			}
			capitalizedFN := capitalize(fn)
			if unicode.IsDigit(rune(capitalizedFN[0])) {
				capitalizedFN = "N" + capitalizedFN
			}

			// is this type a type we want replaced?
			replacementType, ok := c.replaceTypes[tn]
			if ok {
				tn = replacementType
			}
			// is this one of the paths for which we specified a type?
			typeForPath, ok := c.typesForItems[fmt.Sprintf("%s.%s", structName, capitalizedFN)]
			if ok {
				tn = typeForPath
			}
			if tn == "" {
				tn = "interface{}"
			}
			if tn == structName {
				tn = "*" + tn // otherwise we get an illegal cycle
			}
			code.WriteString(fmt.Sprintf("\t%s %s `json:\"%s\"`\n", capitalizedFN, tn, fn))
		}
		code.WriteString(fmt.Sprintf("}\n\n"))
	}
	for i := range imports {
		c.imports = append(c.imports, i)
	}
	sort.Strings(c.imports)
	if len(c.imports) > 0 {
		heading.WriteString("import (\n")
		for _, i := range c.imports {
			heading.WriteString(fmt.Sprintf(`\t"%s"\n`, i))
		}
		heading.WriteString(")\n")
	}
	heading.WriteString("\n")
	out.Write([]byte(heading.String()))
	out.Write([]byte(code.String()))
}
