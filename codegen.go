package main

import (
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"unicode"
)

type maybeType struct {
	isArray          bool
	typeOf           reflect.Type
	nameOftype       string
	originalFileName string
	multiType        []string
	description      string
}

func (m *maybeType) IsMultiple() bool {
	return len(m.multiType) > 0
}

// Resolve tries to return a reasonable type based on the metadata we collected when analizing the
// original input.
func (m *maybeType) Resolve() (string, string) {
	// it is either anyOf, oneOf or allOf so inline types
	if len(m.multiType) > 0 {
		t := ""
		for _, mt := range m.multiType {
			t = t + `*` + capitalize(mt) + " `json:\",inline\"`\n"
		}
		return "", t
	}

	// it is not a reflected type (so no a primitive) if we can't guess what it is, we make it
	// empty interface, which will work for json parsers anyway.
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

	// This is a go primitive or not but we slipped through the other cracks.
	tname := m.typeOf.Name()
	if tname == "" {
		tname = "interface{}"
	}
	if m.isArray {
		tname = "[]" + tname
	}
	return m.typeOf.PkgPath(), tname
}

// Equals roughly compares type metadatas, it is incomplete
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
	if strings.HasPrefix(s, "map[") {
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

// makeMeCode will get our common structure and make it into go, we do not use AST or anything
// else as it seems this is a more reasonable way.
func makeMeCode(c *config, typeMap map[string]map[string]maybeType,
	outerTypeNames map[string]string,
	extraComments map[string]string,
	out io.Writer) {
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
		// file used to generate this type, might be useful to trace back generation errors.
		fileName, ok := outerTypeNames[tk]
		if !ok {
			fmt.Printf("could not find '%s' \n", tk)
			fileName = "unknown"
			if c.swaggerFile != "" {
				fileName = c.swaggerFile
			}
		}
		tvs := typeMap[tk]
		// Ensure the same JSON will always yield the same output (there are a few exceptions) for
		// too repetitive JSONs with the parsing order causing one type to be named in one way and
		// the next repeated name will have a prefix, swagger and reasonable JSON does not have this
		// problem
		fieldNames := make([]string, 0, len(tvs))
		for tn := range tvs {
			fieldNames = append(fieldNames, tn)
		}
		sort.Strings(fieldNames)
		structName := capitalize(tk)

		// Add a comment that Go likes, if possible also add extra comments if source provides.
		code.WriteString(fmt.Sprintf("// %s is auto generated by github.com/perrito666/LAC from \"%s\" json file\n", structName, fileName))
		ec, ok := extraComments[tk]
		if ok {
			code.WriteString(fmt.Sprintf("// %s \n", strings.Replace(ec, "\n", "\n// ", -1)))
		}

		// type definition
		code.WriteString(fmt.Sprintf("type %s struct {\n", structName))
		for _, fn := range fieldNames {
			f := tvs[fn]
			pkg, tn := f.Resolve()
			// this comes from an external package, so we add an import.
			if pkg != "" {
				imports[pkg] = true
			}

			// this is an embeddable type, happens to anyOf, oneOf, allOf definitions.
			if fn == "" {
				code.WriteString(tn)
				break
			}

			// Make sure the name is as Go lint compliant as possible.
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

			// if somehow this got all the way through empty, it becomes empty interface.
			if tn == "" {
				tn = "interface{}"
			}

			// this kind of recursion is not allowed in Go without pointers
			if tn == structName {
				tn = "*" + tn // otherwise we get an illegal cycle
			}

			// We have a description for the field, we add it formatting for go linter to be happy.
			if f.description != "" {
				code.WriteString(fmt.Sprintf("// %s is the %s\n", capitalizedFN, strings.Replace(f.description, "\n", "\n// ", -1)))
			}

			// this is either anyOf, oneOf or allOf so we embed the components into an anonymous
			// struct and hope for the best.
			// TODO make this a more complex struct and gemerate marshaling functions.
			if f.IsMultiple() {
				code.WriteString(fmt.Sprintf("\t%s  struct {\n", capitalizedFN))
				code.WriteString(fmt.Sprintf("\t%s \n", tn))
				code.WriteString(fmt.Sprintf("\t} `json:\"%s\"`\n", fn))
				continue
			}

			// Add a tag
			code.WriteString(fmt.Sprintf("\t%s %s `json:\"%s\"`\n", capitalizedFN, tn, fn))
		}
		code.WriteString(fmt.Sprintf("}\n\n"))
	}

	// add the imports
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
