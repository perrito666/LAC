package main

import (
	"errors"
	"fmt"
	"io"
	"os"

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
	// the type structure
	var ts map[string]map[string]maybeType
	// the outer type names
	var tns = map[string]string{}
	// extra comments to be added to the type definitions
	var extraComments = map[string]string{}

	if len(c.swaggerFile) != 0 {
		// swagger files, at least the ones I tried, return types with sane names to avoid needing
		// outer name correction but also return comments from their types description.
		// Schemas can be converted straight into the rendereable map since there is no guessing
		// happening so no intermediat format needed.
		ts, extraComments, err = schemaIntoMap(c)
		if err != nil {
			return fmt.Errorf("reading swagger file into maps: %w", err)
		}
	} else {
		// JSON will need the extra tns map that contains outer names, these are used to name
		// the outer most types basede on input file names.
		// jsonIntoMap creates an intermediat format from the .json files so we can then
		// resolve the types from it.
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
	makeMeCode(c, ts, tns, extraComments, out)
	return nil
}
