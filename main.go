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
	swaggerFile   string
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
	flag.CommandLine.StringVar(&c.swaggerFile, "swaggerfile", "", "path to a file containing a swagger schema json.")
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
	var ts map[string]map[string]maybeType
	var tns map[string]string
	if len(c.swaggerFile) != 0 {
		ts, err = schemaIntoMap(c)
		if err != nil {
			return fmt.Errorf("reading swagger file into maps: %w", err)
		}
	} else {
		m, err := jsonIntoMap(c)
		if err != nil {
			return fmt.Errorf("reading files into maps: %w", err)
		}
		ts, tns, err = typesFromMap(c, m)
		if err != nil {
			return fmt.Errorf("crafting types: %w", err)
		}
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
	makeMeCode(c, ts, tns, out)
	return nil
}

// SwaggerType represents a schema type in swagger
type SwaggerType string

const (
	// STString represents a string property
	STString SwaggerType = "string"
	// STNumber represents a number (float) property
	STNumber SwaggerType = "number"
	// STInteger represents an integer property
	STInteger SwaggerType = "integer"
	// STBoolean represents a boolean property
	STBoolean SwaggerType = "boolean"
	// STArray represents an array of something else property
	STArray SwaggerType = "array"
	// STObject represents an object property
	STObject SwaggerType = "object"
)

// SwaggerXML represents the XML attribute in swagger specs
type SwaggerXML struct {
	Name      string `json:"name,omitempty"`
	Attribute string `json:"attribute,omitempty"`
}

type OnlyRef struct {
	Ref string `json:"$ref,omitempty"`
}

type MultiProperties struct {
	AllOf []OnlyRef `json:"allOf,omitempty"` // for now we only support Ref
	AnyOf []OnlyRef `json:"anyOf,omitempty"` // for now we only support Ref
	OneOf []OnlyRef `json:"oneOf,omitempty"` //for now we only support Ref
}

type MetaSwaggerProperty struct {
	Type            SwaggerType `json:"type,omitempty"`
	Ref             string      `json:"$ref,omitempty"`
	Required        bool        `json:"required,omitempty"`
	Description     string      `json:"description,omitempty"`
	Format          string      `json:"format,omitempty"`
	ReadOnly        bool        `json:"readOnly,omitempty"` // ill ignore this
	Enum            []string    `json:"enum,omitempty"`
	MultiProperties `json:",inline"`
}

// SwaggerItems represents the Item property of swagger schemas
type SwaggerItems struct {
	MetaSwaggerProperty `json:",inline"`
}

// SwaggerProperty represents the Property attribute of swagger schemas.
type SwaggerProperty struct {
	MetaSwaggerProperty  `json:",inline"`
	Items                SwaggerItems     `json:"items,omitempty"`
	AdditionalProperties *SwaggerProperty `json:"additionalProperties,omitempty"`
}

// SwaggerSchema represents the Schema attribute on swagger schemas
type SwaggerSchema struct {
	Type            SwaggerType                `json:"type,omitempty"`
	Properties      map[string]SwaggerProperty `json:"properties,omitempty"`
	MultiProperties `json:",inline"`
}

// SwaggerComponents represents the components attribute of swagger schemas.
type SwaggerComponents struct {
	Schemas map[string]SwaggerSchema `json:"schemas,omitempty"`
}

// SwaggerSimplification represents a subset of Swagger schemas
type SwaggerSimplification struct {
	Components SwaggerComponents `json:"components,omitempty"`
}

func typeFromRef(ref string) string {
	i := strings.LastIndex(ref, "/")
	if i < 0 {
		return ref
	}
	return ref[i+1:]
}

func processMultiple(multi []OnlyRef) maybeType {
	result := maybeType{
		multiType: make([]string, 0, len(multi)),
	}
	for _, m := range multi {
		result.multiType = append(result.multiType, typeFromRef(m.Ref))
	}
	return result
}

func resolveSwaggerType(prop SwaggerProperty) maybeType {
	switch prop.Type {
	case STArray:
		if prop.Items.Ref != "" {
			return maybeType{
				isArray:    true,
				nameOftype: typeFromRef(prop.Items.Ref),
			}
		}
		var fieldType maybeType
		if len(prop.Items.AllOf) > 0 {
			fieldType = processMultiple(prop.Items.AllOf)
		}
		if len(prop.Items.OneOf) > 0 {
			fieldType = processMultiple(prop.Items.OneOf)
		}
		if len(prop.Items.AnyOf) > 0 {
			fieldType = processMultiple(prop.Items.AnyOf)
		}
		if prop.Items.Type != "" {
			fieldType = resolveSwaggerType(SwaggerProperty{
				MetaSwaggerProperty: prop.Items.MetaSwaggerProperty,
			})
		}
		fieldType.isArray = true
		return fieldType
	case STBoolean:
		return maybeType{typeOf: reflect.TypeOf(bool(true))}
	case STInteger:
		return maybeType{typeOf: reflect.TypeOf(int64(1))}
	case STNumber:
		return maybeType{typeOf: reflect.TypeOf(float64(1.1))}
	case STString:
		return maybeType{typeOf: reflect.TypeOf("")}
	case STObject:
		if len(prop.AllOf) > 0 {
			fmt.Println("processing all of")
			return processMultiple(prop.AllOf)
		}
		if len(prop.OneOf) > 0 {
			fmt.Println("processing one of")
			return processMultiple(prop.OneOf)
		}
		if len(prop.AnyOf) > 0 {
			fmt.Println("processing any of")
			return processMultiple(prop.AnyOf)
		}
		if prop.AdditionalProperties != nil {
			return resolveSwaggerType(*prop.AdditionalProperties)
		}
		if prop.Ref != "" {
			return maybeType{
				nameOftype: typeFromRef(prop.Ref),
			}
		}
		return maybeType{}
	}
	return maybeType{}
}

func processProperty(ps map[string]SwaggerProperty) map[string]maybeType {
	t := map[string]maybeType{}
	for fieldName, prop := range ps {
		fmt.Printf("processing field %s\n", fieldName)
		t[fieldName] = resolveSwaggerType(prop)
		fmt.Printf("resulting in: %#v\n", t[fieldName])
	}
	return t
}

func schemaIntoMap(c *config) (map[string]map[string]maybeType, error) {

	result := map[string]map[string]maybeType{}

	var tgt SwaggerSimplification
	fp, err := os.Open(c.swaggerFile)
	if err != nil {
		return nil, fmt.Errorf("opening json file: %w", err)
	}
	if err := json.NewDecoder(fp).Decode(&tgt); err != nil {
		return nil, fmt.Errorf("decoding file contents: %w", err)
	}
	for compName, component := range tgt.Components.Schemas {
		newType := map[string]maybeType{}
		switch component.Type {
		case STObject:
			fmt.Printf("processing %s\n", compName)
			if len(component.AllOf) > 0 {
				fmt.Println("processing all of")
				result[compName] = map[string]maybeType{
					"": processMultiple(component.AllOf),
				}
				continue
			}
			if len(component.OneOf) > 0 {
				fmt.Println("processing one of")
				result[compName] = map[string]maybeType{
					"": processMultiple(component.OneOf),
				}
				continue
			}
			if len(component.AnyOf) > 0 {
				fmt.Println("processing any of")
				result[compName] = map[string]maybeType{
					"": processMultiple(component.AnyOf),
				}
				continue
			}
			newType = processProperty(component.Properties)
			result[compName] = newType
		default:
			fmt.Printf("%s is just a %s", compName, component.Type)
		}
	}
	return result, nil
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

func typesFromMap(c *config, m map[string][]interface{}) (map[string]map[string]maybeType, map[string]string, error) {
	types := map[string]map[string]maybeType{}
	outerTypes := map[string]string{}
	for tn, t := range m {
		for _, tf := range t {
			switch field := tf.(type) {
			case map[string]interface{}:
				fileName := filepath.Base(tn)
				parts := strings.Split(fileName, ".")
				name := parts[0]
				t, err := unWrapMap(c, field, name, types, outerTypes, tn)
				if err != nil {
					return nil, nil, fmt.Errorf("unwrapping json types: %w", err)
				}
				finalTname, _ := typeExists(name, "topLevel", c, t, types)
				outerTypes[finalTname] = tn
			default:
				// not sure what to do here
				fmt.Printf("type of field (%T) %v\n", tf, tf)
			}
		}
	}
	return types, outerTypes, nil
}

func unWrapMap(c *config, m map[string]interface{}, name string,
	typeMap map[string]map[string]maybeType,
	outerTypes map[string]string,
	fileName string) (map[string]maybeType, error) {
	aType := map[string]maybeType{}
	for fn, f := range m {
		var it = maybeType{
			originalFileName: fileName,
		}
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
				uit, err := unWrapMap(c, innerField, fn, typeMap, outerTypes, fileName)
				if err != nil {
					return nil, fmt.Errorf("unwrapping type %s: %w", fn, err)
				}

				tName, _ := typeExists(fn, name, c, uit, typeMap)
				outerTypes[tName] = fileName
				it.nameOftype = tName
			default:
				it.typeOf = reflect.TypeOf(innerField)
			}

		case map[string]interface{}:
			uit, err := unWrapMap(c, field, fn, typeMap, outerTypes, fileName)
			if err != nil {
				return nil, fmt.Errorf("unwrapping type %s: %w", fn, err)
			}
			tName, _ := typeExists(fn, name, c, uit, typeMap)
			outerTypes[tName] = fileName
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
	isArray          bool
	typeOf           reflect.Type
	nameOftype       string
	originalFileName string
	multiType        []string
	description      string
}

func (m *maybeType) Resolve() (string, string) {
	if len(m.multiType) > 0 {
		t := ""
		for _, mt := range m.multiType {
			t = t + `*` + mt + " `json:\",inline\"`\n"
		}
		return "", t
	}

	if m.typeOf == nil {
		n := capitalize(m.nameOftype)
		if n == "" {
			n = "interface{}"
		}
		if m.isArray {
			n = "[]" + n
		}
		return "", n
	}

	tname := m.typeOf.Name()
	if tname == "" {
		tname = "interface{}"
	}
	if m.isArray {
		tname = "[]" + tname
	}
	return m.typeOf.PkgPath(), tname
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

func makeMeCode(c *config, typeMap map[string]map[string]maybeType, outerTypeNames map[string]string, out io.Writer) {
	heading := &strings.Builder{}
	heading.WriteString(fmt.Sprintf("package %s\n", c.targetPackage))
	imports := map[string]bool{}
	code := &strings.Builder{}
	typeNames := make([]string, 0, len(typeMap))
	for tk := range typeMap {
		typeNames = append(typeNames, tk)
	}
	sort.Strings(typeNames)
	for typeToFiles, fname := range outerTypeNames {
		fmt.Printf("type %s is in file %s\n", typeToFiles, fname)
	}
	for _, tk := range typeNames {
		fileName, ok := outerTypeNames[tk]
		if !ok {
			fmt.Printf("could not find '%s' \n", tk)
			fileName = "unknown"
			if c.swaggerFile != "" {
				fileName = c.swaggerFile
			}
		}
		tvs := typeMap[tk]
		fieldNames := make([]string, 0, len(tvs))
		for tn := range tvs {
			fieldNames = append(fieldNames, tn)
		}
		sort.Strings(fieldNames)
		structName := capitalize(tk)

		code.WriteString(fmt.Sprintf("// %s is auto generated by github.com/perrito666/LAC from \"%s\" json file\n", structName, fileName))
		code.WriteString(fmt.Sprintf("type %s struct {\n", structName))
		for _, fn := range fieldNames {
			f := tvs[fn]
			pkg, tn := f.Resolve()
			if pkg != "" {
				imports[pkg] = true
			}
			if fn == "" {
				code.WriteString(tn)
				break
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
			if f.description != "" {
				code.WriteString(fmt.Sprintf("// %s\n", f.description))
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
