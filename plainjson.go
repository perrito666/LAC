package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode"
)

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
