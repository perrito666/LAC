# LAC -- Lazy API Coder

This tool is intended to generate go types from JSON it is inspired in @mholt 's wonderful [JSON to Go](https://github.com/mholt/json-to-go) which I used extensively in a recent integration project and left me wishing I had something I could run to generate dumb types from a set of API sample responses as part of my Make process.


# Usage

```
Usage of ./LAC:
      --imports strings                                      imports to be added
      --package string                                       the package of the module where the structs will live. (default "main")
      --replacetypes float64=float32                         replace basic types with your own, only full matching with the type name is done, remember to add them to imports if they depend on external packages. ie float64=float32 (default [])
      --source strings                                       list of files to use as source, wildcards are valid (such as *.json) but need to be quote wrapped.
      --structnames issuetype=someotherstructname            alternative struct names for types, only full matches will be replaced use either comma separated match=replacement or pass this flag multiple times, the names before capitalization are considered for the match. ie issuetype=someotherstructname (default [])
      --swaggerfile string                                   path to a file containing a swagger schema json.
      --target string                                        path to the go file where structs will be created. If none provided stdout will be used.
      --typesforitems StructName.Member=package.CustomType   replace types of struct members specifying the path. ie StructName.Member=package.CustomType  (default [])<F24><F25>
```

All types are exported.

For the outer types, the file names (without extension) are used, it is recommended that you either name the file as you want the outer struct to be called or provide a replacement in `--structnames`

# TODO:

* A ton of tests, I currently use the [api examples of JIRA](https://developer.atlassian.com/cloud/jira/platform/rest/v3) as a test but I am not sure I am free to distribute these as tests so ill leave you to get them.
* Accept stdin as input.
* Add input from a struct comment and add the fields to said struct
* Suport inline structs
