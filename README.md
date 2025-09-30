# SpecSmash

**Property-based testing for OpenAPI 3.0 specifications in Go**

Ever wanted to destroy your validator? Were you too lazy to write proper unit tests for your validation? 

Say 
No 
More

SpecSmash generates random test data that conforms to your OpenAPI 3.0 schemas. Inspired by [hypothesis-jsonschema](https://github.com/Zac-HD/hypothesis-jsonschema) but for Go.

## Features

- **Automatic Test Data Generation** - Generates realistic test data from OpenAPI 3.0 schemas
- **Schema Validation** - Ensures generated data validates against your spec
- **Property-Based Testing** - Leverages [pgregory.net/rapid](https://pkg.go.dev/pgregory.net/rapid) for exhaustive edge case discovery
- **Comprehensive Coverage** - Supports complex schemas including:
  - All primitive types (string, number, integer, boolean)
  - String formats (uuid, date-time, email, byte, etc.)
  - Objects with nested properties
  - Arrays with various item types
  - oneOf, anyOf, allOf compositions
  - Nullable fields
  - Min/max constraints, enums
  - Additional properties
- **Custom Pattern Generators** - Provide your own pattern matching functions for regex patterns and string formats

## Installation

```bash
go get github.com/djosh34/specsmash
```

## Quick Start

```go
package main

import (
    "testing"
    "github.com/djosh34/specsmash"
    "pgregory.net/rapid"
)

func TestMyAPI(t *testing.T) {
    // Load your OpenAPI spec
    spec, err := SpecSmash.ReadSpec("testdata/openapi.yaml")
    if err != nil {
        t.Fatal(err)
    }

    // Generate and validate test data
    rapid.Check(t, func(t *rapid.T) {
        // Get the schema you want to test
        schema := spec.Components.Schemas["User"].Value
        
        // Generate data conforming to the schema
        generator := SpecSmash.GenFromSchema(schema)
        data := generator.Draw(t, "user-data")
        
        // Use the generated data to test your API
        // ... your test logic here
    })
}
```

## Pattern Matching

SpecSmash requires you to provide a custom pattern function for schemas that use `pattern` constraints or certain string formats (email, hostname, ipv4, ipv6, uri, uri-reference). This is because ECMA regex patterns are not natively supported.

### Using Pattern Functions

```go
import (
    "testing"
    "github.com/djosh34/specsmash"
    "pgregory.net/rapid"
)

func TestWithPatterns(t *testing.T) {
    spec, err := SpecSmash.ReadSpec("testdata/openapi.yaml")
    if err != nil {
        t.Fatal(err)
    }

    // Create generation options with a custom pattern function
    opts := SpecSmash.NewGenerationOptions().WithPatternFunc(
        func(pattern string, format string, minLength int, maxLength int, t *rapid.T) string {
            // Use rapid.StringMatching or your own implementation
            // NOTE: rapid.StringMatching uses RE2 syntax, not ECMA
            return rapid.StringMatching(pattern).Draw(t, "pattern-"+format)
        },
    )

    rapid.Check(t, func(t *rapid.T) {
        schema := spec.Components.Schemas["User"].Value
        
        // Use the custom options
        generator := opts.GenFromSchema(schema)
        data := generator.Draw(t, "user-data")
        
        // ... your test logic here
    })
}
```

The pattern function receives:
- `pattern` - The regex pattern from the schema (or a suggested pattern for formats)
- `format` - The string format (e.g., "email", "ipv4", or empty string for custom patterns)
- `minLength` - Minimum string length constraint from the schema
- `maxLength` - Maximum string length constraint from the schema (-1 if not set)
- `t` - The rapid.T instance for drawing values

**Important**: If your schema contains patterns or certain string formats and you don't provide a `PatternFunc`, the generator will panic with a helpful error message.

## Limitations

- `multipleOf` is not fully supported due to implementation errors (in this version). It will generate valid multipleOfs most of the time, unless you have very small multipliers that will cause precision issues
- `anyOf` when both have a shared property, the generator will error (in this version)
- Pattern matching requires a user-provided function due to ECMA regex incompatibility


## How It Works

SpecSmash uses [kin-openapi](https://github.com/getkin/kin-openapi) to parse OpenAPI specifications and [rapid](https://pkg.go.dev/pgregory.net/rapid) for property-based test generation. It intelligently creates generators that respect all schema constraints including types, formats, ranges, patterns, and compositions.

## License

MIT
