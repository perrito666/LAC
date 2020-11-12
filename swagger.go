package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
)

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

// OnlyRef represents a simple object that only contains a ref to another component.
type OnlyRef struct {
	Ref string `json:"$ref,omitempty"`
}

// MultiProperties holds the bulk of multiple option properties.
type MultiProperties struct {
	AllOf []OnlyRef `json:"allOf,omitempty"` // for now we only support Ref
	AnyOf []OnlyRef `json:"anyOf,omitempty"` // for now we only support Ref
	OneOf []OnlyRef `json:"oneOf,omitempty"` //for now we only support Ref
}

// MetaSwaggerProperty holds the set of common fields to several properties.
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
	Description     string                     `json:"description,omitempty"`
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

func processMultiple(multi []OnlyRef, description string) maybeType {
	result := maybeType{
		description: description,
		multiType:   make([]string, 0, len(multi)),
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
				isArray:     true,
				description: prop.Description,
				nameOftype:  typeFromRef(prop.Items.Ref),
			}
		}
		var fieldType maybeType
		if len(prop.Items.AllOf) > 0 {
			fieldType = processMultiple(prop.Items.AllOf, prop.Description)
		}
		if len(prop.Items.OneOf) > 0 {
			fieldType = processMultiple(prop.Items.OneOf, prop.Description)
		}
		if len(prop.Items.AnyOf) > 0 {
			fieldType = processMultiple(prop.Items.AnyOf, prop.Description)
		}
		if prop.Items.Type != "" {
			fieldType = resolveSwaggerType(SwaggerProperty{
				MetaSwaggerProperty: prop.Items.MetaSwaggerProperty,
			})
		}
		fieldType.isArray = true
		return fieldType
	case STBoolean:
		return maybeType{
			description: prop.Description,
			typeOf:      reflect.TypeOf(bool(true)),
		}
	case STInteger:
		return maybeType{
			description: prop.Description,
			typeOf:      reflect.TypeOf(int64(1)),
		}
	case STNumber:
		return maybeType{
			description: prop.Description,
			typeOf:      reflect.TypeOf(float64(1.1)),
		}
	case STString:
		return maybeType{
			description: prop.Description,
			typeOf:      reflect.TypeOf(""),
		}
	case STObject:
		if len(prop.AllOf) > 0 {
			fmt.Println("processing all of")
			return processMultiple(prop.AllOf, prop.Description)
		}
		if len(prop.OneOf) > 0 {
			fmt.Println("processing one of")
			return processMultiple(prop.OneOf, prop.Description)
		}
		if len(prop.AnyOf) > 0 {
			fmt.Println("processing any of")
			return processMultiple(prop.AnyOf, prop.Description)
		}
		if prop.AdditionalProperties != nil {
			aps := resolveSwaggerType(*prop.AdditionalProperties)
			if aps.nameOftype != "" {
				aps.nameOftype = "map[string]" + aps.nameOftype
			} else if aps.typeOf == nil {
				aps.nameOftype = "map[string]interface{}"
			}
			return aps
		}
		if prop.Ref != "" {
			return maybeType{
				description: prop.Description,
				nameOftype:  typeFromRef(prop.Ref),
			}
		}
		return maybeType{
			description: prop.Description,
		}
	default:
		// No type can happen for multi items
		if len(prop.AllOf) > 0 {
			fmt.Println("processing all of")
			return processMultiple(prop.AllOf, prop.Description)
		}
		if len(prop.OneOf) > 0 {
			fmt.Println("processing one of")
			return processMultiple(prop.OneOf, prop.Description)
		}
		if len(prop.AnyOf) > 0 {
			fmt.Println("processing any of")
			return processMultiple(prop.AnyOf, prop.Description)
		}
		if prop.Ref != "" {
			return maybeType{
				description: prop.Description,
				nameOftype:  typeFromRef(prop.Ref),
			}
		}
	}
	return maybeType{description: prop.Description}
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

func schemaIntoMap(c *config) (map[string]map[string]maybeType, map[string]string, error) {

	result := map[string]map[string]maybeType{}
	extraComments := map[string]string{}

	var tgt SwaggerSimplification
	fp, err := os.Open(c.swaggerFile)
	if err != nil {
		return nil, nil, fmt.Errorf("opening json file: %w", err)
	}
	if err := json.NewDecoder(fp).Decode(&tgt); err != nil {
		return nil, nil, fmt.Errorf("decoding file contents: %w", err)
	}
	for compName, component := range tgt.Components.Schemas {
		newType := map[string]maybeType{}
		extraComments[compName] = component.Description
		switch component.Type {
		case STObject:
			fmt.Printf("processing %s\n", compName)
			if len(component.AllOf) > 0 {
				fmt.Println("processing all of")
				result[compName] = map[string]maybeType{
					"": processMultiple(component.AllOf, component.Description),
				}
				continue
			}
			if len(component.OneOf) > 0 {
				fmt.Println("processing one of")
				result[compName] = map[string]maybeType{
					"": processMultiple(component.OneOf, component.Description),
				}
				continue
			}
			if len(component.AnyOf) > 0 {
				fmt.Println("processing any of")
				result[compName] = map[string]maybeType{
					"": processMultiple(component.AnyOf, component.Description),
				}
				continue
			}
			newType = processProperty(component.Properties)
			result[compName] = newType
		default:
			fmt.Printf("%s is just a %s", compName, component.Type)
		}
	}
	return result, extraComments, nil
}
