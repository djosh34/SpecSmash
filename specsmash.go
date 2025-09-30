package SpecSmash

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3filter"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"pgregory.net/rapid"
)

// GenerationOptions holds configuration for schema generation
type GenerationOptions struct {
	depth                   int
	MaxDepth                int
	AdditionalPropertiesMax int
}

// ---------------- Core Utilities ----------------
func getType(t string) *openapi3.Types {
	typesSlice := openapi3.Types([]string{t})
	return &typesSlice
}

// genNull returns a RawMessage "null"
func genNull() *rapid.Generator[json.RawMessage] {
	return rapid.Just(json.RawMessage("null"))
}

// wrapNullable wraps a generator with nullable=true semantics.
func wrapNullable(schema *openapi3.Schema, g *rapid.Generator[json.RawMessage]) *rapid.Generator[json.RawMessage] {
	return rapid.Custom(func(t *rapid.T) json.RawMessage {
		if schema.Nullable {
			return rapid.OneOf(g, genNull()).Draw(t, "either-null-or-gen")
		}
		return g.Draw(t, "not-nullable-passthrough")
	})
}

// marshal wraps arbitrary Go into RawMessage
func marshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// ---------------- Primitive Generators ----------------

func (opts *GenerationOptions) genString(schema *openapi3.Schema) *rapid.Generator[json.RawMessage] {
	// Custom string generator with early returns using draw
	stringGen := rapid.Custom(func(t *rapid.T) string {
		// Special formats with early returns
		switch schema.Format {
		case "uuid":
			return rapid.Just(uuid.NewString()).Draw(t, "uuid")
		case "date-time":
			return rapid.Just(time.Now().UTC().Format(time.RFC3339)).Draw(t, "date-time")
		case "date":
			return rapid.Just(time.Now().UTC().Format("2006-01-02")).Draw(t, "date")
		case "email":
			return rapid.StringMatching(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`).Draw(t, "email")
		case "hostname":
			return rapid.StringMatching(`[a-zA-Z0-9\-\.]{1,253}`).Draw(t, "hostname")
		case "ipv4":
			return rapid.StringMatching(`\d{1,3}(\.\d{1,3}){3}`).Draw(t, "ipv4")
		case "ipv6":
			// loose IPv6 matcher
			return rapid.StringMatching(`([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}`).Draw(t, "ipv6")
		case "uri":
			return rapid.StringMatching(`https?://[^\s]+`).Draw(t, "uri")
		case "uri-reference":
			return rapid.StringMatching(`[-A-Za-z0-9._~:/?#@!$&'()*+,;=%]+`).Draw(t, "uri-reference")
		case "byte":
			// base64-encoded bytes
			b := rapid.SliceOfN(rapid.Byte(), 0, -1).Draw(t, "bytes")
			return base64.StdEncoding.EncodeToString(b)
		case "binary":
			// any octet sequence – represent as base64 to keep valid JSON
			b := rapid.SliceOfN(rapid.Byte(), 0, -1).Draw(t, "bytes")
			return base64.StdEncoding.EncodeToString(b)
		}

		// Handle pattern
		if schema.Pattern != "" {
			// TODO: support actual ecma regex syntax
			return rapid.StringMatching(schema.Pattern).Draw(t, "pattern")
		}

		// Default string with length bounds
		minLength := int(schema.MinLength)
		maxLength := -1
		if schema.MaxLength != nil {
			maxLength = int(*schema.MaxLength)
		}
		return rapid.StringN(minLength, maxLength, -1).Draw(t, "string")
	})

	// Second custom generator that draws from stringGen and returns json.RawMessage
	return rapid.Custom(func(t *rapid.T) json.RawMessage {
		if len(schema.Enum) > 0 {
			choices := make([]json.RawMessage, len(schema.Enum))
			for i, e := range schema.Enum {
				choices[i] = marshal(e)
			}
			return rapid.SampledFrom(choices).Draw(t, "String-Enum")
		}

		str := stringGen.Draw(t, "string-value")
		gen := rapid.Just(marshal(str))
		return wrapNullable(schema, gen).Draw(t, "String-Value")
	})
}

func (opts *GenerationOptions) genInteger(schema *openapi3.Schema) *rapid.Generator[json.RawMessage] {
	return rapid.Custom(func(t *rapid.T) json.RawMessage {
		minLength := int64(math.MinInt64)
		maxLength := int64(math.MaxInt64)
		if schema.Min != nil {
			m := int64(*schema.Min)
			if schema.ExclusiveMin {
				m++
			}
			minLength = m
		}
		if schema.Max != nil {
			m := int64(*schema.Max)
			if schema.ExclusiveMax {
				m--
			}
			maxLength = m
		}

		// clamp by integer format if provided
		switch schema.Format {
		case "int32":
			if minLength < math.MinInt32 {
				minLength = math.MinInt32
			}
			if maxLength > math.MaxInt32 {
				maxLength = math.MaxInt32
			}
		}

		base := rapid.Int64Range(minLength, maxLength)

		// multipleOf
		if schema.MultipleOf != nil && *schema.MultipleOf != 0 {
			mult := int64(*schema.MultipleOf)

			highestMultiplePossible := maxLength / mult
			lowestMultiplePossible := minLength / mult
			if lowestMultiplePossible > highestMultiplePossible {
				panic("multipleOf is too large for the given range")
			}
			base = rapid.Map(rapid.Int64Range(lowestMultiplePossible, highestMultiplePossible), func(v int64) int64 {
				return v * mult
			})
		}

		gen := rapid.Map(base, func(v int64) json.RawMessage { return marshal(v) })

		if len(schema.Enum) > 0 {
			opts := make([]json.RawMessage, len(schema.Enum))
			for i, e := range schema.Enum {
				opts[i] = marshal(e)
			}
			return rapid.SampledFrom(opts).Draw(t, "Integer-Enum")
		}

		return wrapNullable(schema, gen).Draw(t, "Integer-Value")
	})
}

func (opts *GenerationOptions) genNumber(schema *openapi3.Schema) *rapid.Generator[json.RawMessage] {
	return rapid.Custom(func(t *rapid.T) json.RawMessage {
		minimum := -math.MaxFloat64
		maximum := math.MaxFloat64
		if schema.Min != nil {
			m := *schema.Min
			if schema.ExclusiveMin {
				m = math.Nextafter(m, math.Inf(1))
			}
			minimum = m
		}
		if schema.Max != nil {
			m := *schema.Max
			if schema.ExclusiveMax {
				m = math.Nextafter(m, -math.Inf(1))
			}
			maximum = m
		}

		if schema.MultipleOf != nil && *schema.MultipleOf != 0 {
			mult := *schema.MultipleOf
			// kin-openapi doesn't validate multipleofs correctly
			// TODO too much work to fix this in the broken libs
			// not important anyways
			// this is safe probably
			minimum = math.Max(minimum, -2000000)
			maximum = math.Min(maximum, 20000000)

			if math.Abs(mult) > 1.0 {
				minimum /= mult
				maximum /= mult
			}

			multiplier := rapid.IntRange(int(minimum), int(maximum)).Draw(t, "Number-Multiplier")
			multiplication := float64(multiplier) * mult
			//
			//valuaString := fmt.Sprintf("%.10f", multiplication)
			//valuaStringG := fmt.Sprintf("%g", multiplication)
			//fmt.Println(valuaString)
			//fmt.Println(valuaStringG)
			//
			return marshal(multiplication)

			//base = rapid.Map(rapid.IntRange(int(minimum), int(maximum)), func(v int) float64 {
			//	return float64(v) * mult
			//})
		}

		base := rapid.Float64Range(minimum, maximum)
		gen := rapid.Map(base, func(v float64) json.RawMessage { return marshal(v) })

		if len(schema.Enum) > 0 {
			opts := make([]json.RawMessage, len(schema.Enum))
			for i, e := range schema.Enum {
				opts[i] = marshal(e)
			}
			return rapid.SampledFrom(opts).Draw(t, "Number-Enum")
		}

		return wrapNullable(schema, gen).Draw(t, "Number-Value")
	})
}

func (opts *GenerationOptions) genBoolean(schema *openapi3.Schema) *rapid.Generator[json.RawMessage] {
	return rapid.Custom(func(t *rapid.T) json.RawMessage {
		gen := rapid.Map(rapid.Bool(), func(b bool) json.RawMessage { return marshal(b) })
		if len(schema.Enum) > 0 {
			choices := make([]json.RawMessage, len(schema.Enum))
			for i, e := range schema.Enum {
				choices[i] = marshal(e)
			}
			return rapid.SampledFrom(choices).Draw(t, "Boolean-Enum")
		}
		return wrapNullable(schema, gen).Draw(t, "Boolean-Value")
	})
}

// ---------------- Array Generator ----------------

func (opts *GenerationOptions) genArray(schema *openapi3.Schema) *rapid.Generator[json.RawMessage] {
	return rapid.Custom(func(t *rapid.T) json.RawMessage {
		var itemGen *rapid.Generator[json.RawMessage]
		if schema.Items != nil {
			// Increase depth for recursive calls
			childOpts := &GenerationOptions{depth: opts.depth + 1}
			itemGen = childOpts.GenFromSchema(schema.Items.Value)
		} else {
			childOpts := &GenerationOptions{depth: opts.depth + 1}
			itemGen = childOpts.GenFromSchema(nil)
		}

		minLength := int(schema.MinItems)
		maxLength := -1
		if schema.MaxItems != nil {
			maxLength = int(*schema.MaxItems)
		}

		var arrGen *rapid.Generator[[]json.RawMessage]
		if schema.UniqueItems {
			arrGen = rapid.SliceOfNDistinct(itemGen, minLength, maxLength, func(e json.RawMessage) string { return string(e) })
		} else {
			arrGen = rapid.SliceOfN(itemGen, minLength, maxLength)
		}

		g := rapid.Map(arrGen, func(arr []json.RawMessage) json.RawMessage { return marshal(arr) })

		return wrapNullable(schema, g).Draw(t, "Array-Value")
	})
}

// ---------------- Object Generator ----------------

func (opts *GenerationOptions) genObject(schema *openapi3.Schema) *rapid.Generator[json.RawMessage] {
	// Build lists of required and optional properties
	var requiredPropsStrings []string
	var optionalPropStrings []string

	for propName := range schema.Properties {
		if contains(schema.Required, propName) {
			requiredPropsStrings = append(requiredPropsStrings, propName)
		} else {
			optionalPropStrings = append(optionalPropStrings, propName)
		}
	}

	return rapid.Custom(func(t *rapid.T) json.RawMessage {
		obj := make(map[string]json.RawMessage)
		allProps := make(map[string]*openapi3.SchemaRef)

		// Add additional properties
		isAllowedAdditionalProperties := true
		if schema.AdditionalProperties.Has != nil && !*schema.AdditionalProperties.Has {
			isAllowedAdditionalProperties = false
		}

		if isAllowedAdditionalProperties {
			numExtras := rapid.IntRange(0, opts.AdditionalPropertiesMax).Draw(t, "numExtras") // limit to 5 for performance
			for i := 0; i < numExtras; i++ {
				// even though the later code will replace if the key is already in the map, do note that the extraKey could be an allowed property
				extraKey := rapid.StringN(20, 30, -1).Draw(t, fmt.Sprintf("addKey-%d", i))
				extraSchema := schema.AdditionalProperties.Schema
				allProps[extraKey] = extraSchema
			}
		}

		// Add or override optional properties
		if len(optionalPropStrings) > 0 {
			optionalPropsGen := rapid.SliceOfNDistinct(
				rapid.SampledFrom(optionalPropStrings),
				0, len(optionalPropStrings),
				func(s string) string { return s },
			)
			optionalSampledKeys := optionalPropsGen.Draw(t, "optionalSampledKeys")

			for _, propName := range optionalSampledKeys {
				prop := schema.Properties[propName]
				allProps[propName] = prop
			}

		}

		// Add required properties
		for _, propName := range requiredPropsStrings {
			prop := schema.Properties[propName]
			allProps[propName] = prop
		}

		if len(allProps) == 0 {
			// When there are no properties, we still have to tell rapid that that is so
			return rapid.Just([]byte("{}")).Draw(t, "No props")
		}

		for propName, prop := range allProps {
			childOpts := &GenerationOptions{depth: opts.depth + 1}
			var propSchema *openapi3.Schema
			if prop != nil {
				propSchema = prop.Value
			}
			generatedValue := childOpts.GenFromSchema(propSchema).Draw(t, "prop-"+propName)
			obj[propName] = generatedValue
		}

		return marshal(obj)
	})
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---------------- Any Generator ----------------

func (opts *GenerationOptions) genAny() *rapid.Generator[json.RawMessage] {
	return rapid.Custom(func(t *rapid.T) json.RawMessage {

		// Check depth limit to prevent infinite recursion
		if opts.depth >= opts.MaxDepth {
			return opts.genString(&openapi3.Schema{Type: getType("string")}).Draw(t, "Any-MaxDepth-string")
		}

		return rapid.OneOf(
			opts.genString(&openapi3.Schema{Type: getType("string")}),
			opts.genInteger(&openapi3.Schema{Type: getType("integer")}),
			opts.genNumber(&openapi3.Schema{Type: getType("number")}),
			opts.genBoolean(&openapi3.Schema{Type: getType("boolean")}),
			opts.genArray(&openapi3.Schema{Type: getType("array")}),
			opts.genObject(&openapi3.Schema{Type: getType("object")}),
			genNull(),
		).Draw(t, "Any-OneOf")
	})
}

// ---------------- Compositions ----------------

func (opts *GenerationOptions) handleAllOf(schema *openapi3.Schema) *rapid.Generator[json.RawMessage] {
	return rapid.Custom(func(t *rapid.T) json.RawMessage {
		var mergedSchema openapi3.Schema

		for _, sub := range schema.AllOf {
			mergedSchema = mergeSchema(mergedSchema, sub)
		}

		return opts.genObject(&mergedSchema).Draw(t, "Object-Value")
	})
}

func mergeSchema(schema openapi3.Schema, sub *openapi3.SchemaRef) openapi3.Schema {
	if sub == nil || sub.Value == nil {
		return schema
	}

	subSchema := sub.Value

	// Both schemas must be objects
	if schema.Type != nil && len(*schema.Type) > 0 {
		types := []string(*schema.Type)
		if types[0] != "object" {
			panic(fmt.Sprintf("mergeSchema requires object type, got %s in base schema", types[0]))
		}
	}
	if subSchema.Type != nil && len(*subSchema.Type) > 0 {
		types := []string(*subSchema.Type)
		if types[0] != "object" {
			panic(fmt.Sprintf("mergeSchema requires object type, got %s in sub schema", types[0]))
		}
	}

	// Combine required fields
	requiredMap := make(map[string]bool)
	for _, r := range schema.Required {
		requiredMap[r] = true
	}
	for _, r := range subSchema.Required {
		requiredMap[r] = true
	}

	var mergedRequired []string
	for r := range requiredMap {
		mergedRequired = append(mergedRequired, r)
	}
	schema.Required = mergedRequired

	// Combine properties - fail if duplicates exist
	if schema.Properties == nil {
		schema.Properties = make(openapi3.Schemas)
	}

	for propName, propSchema := range subSchema.Properties {
		if _, exists := schema.Properties[propName]; exists {
			panic(fmt.Sprintf("duplicate property '%s' found during schema merge, unsupported yet", propName))
		}
		schema.Properties[propName] = propSchema
	}

	// Handle additionalProperties
	baseHas := schema.AdditionalProperties.Has
	baseAdditionalSchema := schema.AdditionalProperties.Schema
	subHas := subSchema.AdditionalProperties.Has
	subAdditionalSchema := subSchema.AdditionalProperties.Schema

	// If one of the schemas does not allow additionalProperties, the whole doesn't
	if (baseHas != nil && !*baseHas) || (subHas != nil && !*subHas) {
		falseVal := false
		schema.AdditionalProperties.Has = &falseVal
		schema.AdditionalProperties.Schema = nil
	} else if baseHas != nil && *baseHas && subHas != nil && *subHas {
		// Both are true -> true
		trueVal := true
		schema.AdditionalProperties.Has = &trueVal
		schema.AdditionalProperties.Schema = nil
	} else if (baseHas != nil && *baseHas) && subAdditionalSchema != nil {
		// Base is true, sub has schema -> schema with Has = true
		trueVal := true
		schema.AdditionalProperties.Has = &trueVal
		schema.AdditionalProperties.Schema = subAdditionalSchema
	} else if (subHas != nil && *subHas) && baseAdditionalSchema != nil {
		// Sub is true, base has schema -> schema with Has = true (keep base)
		trueVal := true
		schema.AdditionalProperties.Has = &trueVal
		schema.AdditionalProperties.Schema = baseAdditionalSchema
	} else if baseAdditionalSchema != nil && subAdditionalSchema != nil {
		// Both have schemas -> merge recursively with Has = true
		mergedAdditional := mergeSchema(*baseAdditionalSchema.Value, subAdditionalSchema)
		trueVal := true
		schema.AdditionalProperties.Has = &trueVal
		schema.AdditionalProperties.Schema = &openapi3.SchemaRef{Value: &mergedAdditional}
	} else if baseAdditionalSchema != nil {
		// Only base has schema - set Has = true
		trueVal := true
		schema.AdditionalProperties.Has = &trueVal
		schema.AdditionalProperties.Schema = baseAdditionalSchema
	} else if subAdditionalSchema != nil {
		// Only sub has schema - set Has = true
		trueVal := true
		schema.AdditionalProperties.Has = &trueVal
		schema.AdditionalProperties.Schema = subAdditionalSchema
	}

	return schema
}

func (opts *GenerationOptions) handleAnyOf(schema *openapi3.Schema) *rapid.Generator[json.RawMessage] {
	return rapid.Custom(func(t *rapid.T) json.RawMessage {
		// anyOf means the data must be valid against AT LEAST ONE schema (can be more than one)
		// We'll pick a random non-empty subset of schemas and try to merge them

		numSchemas := len(schema.AnyOf)
		// Pick how many schemas to satisfy (at least 1)
		numToSatisfy := rapid.IntRange(1, numSchemas).Draw(t, "anyOf-count")

		// Pick which schemas to satisfy
		indices := make([]int, numSchemas)
		for i := range indices {
			indices[i] = i
		}
		selectedIndices := rapid.SliceOfNDistinct(
			rapid.SampledFrom(indices),
			numToSatisfy,
			numToSatisfy,
			func(i int) int { return i },
		).Draw(t, "anyOf-indices")

		// If only one schema selected, just generate from it
		if len(selectedIndices) == 1 {
			childOpts := &GenerationOptions{depth: opts.depth + 1}
			return childOpts.GenFromSchema(schema.AnyOf[selectedIndices[0]].Value).Draw(t, "anyOf-single")
		}

		// Multiple schemas selected - try to merge them like allOf
		merged := make(map[string]json.RawMessage)
		for _, idx := range selectedIndices {
			childOpts := &GenerationOptions{depth: opts.depth + 1}
			val := childOpts.GenFromSchema(schema.AnyOf[idx].Value).Draw(t, fmt.Sprintf("anyOf-%d", idx))
			var submap map[string]json.RawMessage
			if err := json.Unmarshal(val, &submap); err == nil {
				// It's an object, merge it
				for k, v := range submap {
					merged[k] = v
				}
			} else {
				// Not an object (primitive type), just return this value
				// (can't easily merge primitives)
				return val
			}
		}
		return marshal(merged)
	})
}

func (opts *GenerationOptions) handleOneOf(schema *openapi3.Schema) *rapid.Generator[json.RawMessage] {
	return rapid.Custom(func(t *rapid.T) json.RawMessage {
		// choose exactly one branch
		var gens []*rapid.Generator[json.RawMessage]
		for _, sub := range schema.OneOf {
			// Increase depth for recursive calls
			childOpts := &GenerationOptions{depth: opts.depth + 1}
			gens = append(gens, childOpts.GenFromSchema(sub.Value))
		}
		return rapid.OneOf(gens...).Draw(t, "OneOf-Choice")
	})
}

// ---------------- Main Dispatcher ----------------

func (opts *GenerationOptions) GenFromSchema(schema *openapi3.Schema) *rapid.Generator[json.RawMessage] {
	return rapid.Custom(func(t *rapid.T) json.RawMessage {
		//fmt.Printf("Generating schema for %v\n", opts.depth)
		if schema == nil {
			return opts.genAny().Draw(t, "any")
		}

		// Compositions first
		if len(schema.AllOf) > 0 {
			return opts.handleAllOf(schema).Draw(t, "AllOf")
		}
		if len(schema.AnyOf) > 0 {
			return opts.handleAnyOf(schema).Draw(t, "AnyOf")
		}
		if len(schema.OneOf) > 0 {
			return opts.handleOneOf(schema).Draw(t, "OneOf")
		}

		if schema.Type == nil {
			return opts.genAny().Draw(t, "Any")
		}

		if len(*schema.Type) > 1 {
			panic("multiple types not supported in this implementation")
		}

		// Direct type
		typesSlice := []string(*schema.Type)
		switch typesSlice[0] {
		case "string":
			return opts.genString(schema).Draw(t, "String")
		case "integer":
			return opts.genInteger(schema).Draw(t, "Integer")
		case "number":
			return opts.genNumber(schema).Draw(t, "Number")
		case "boolean":
			return opts.genBoolean(schema).Draw(t, "Boolean")
		case "array":
			return opts.genArray(schema).Draw(t, "Array")
		case "object":
			return opts.genObject(schema).Draw(t, "Object")
		default:
			return opts.genAny().Draw(t, "Any")
		}
	})
}

// NewGenerationOptions creates a new GenerationOptions instance with default values
func NewGenerationOptions() *GenerationOptions {
	return &GenerationOptions{
		depth:                   0,
		MaxDepth:                10,
		AdditionalPropertiesMax: 10,
	}
}

func ReadSpec(specPath string) (*openapi3.T, error) {
	// load spec
	b, err := os.Open(specPath)
	if err != nil {
		return nil, err
	}

	return ReadSpecFromReader(b)
}

func ReadSpecFromReader(b io.Reader) (*openapi3.T, error) {
	// kin-openapi to reuse our schema generator
	loader := &openapi3.Loader{IsExternalRefsAllowed: true}
	kinDoc, err := loader.LoadFromIoReader(b)
	if err != nil {
		return nil, err
	}
	if err := kinDoc.Validate(loader.Context); err != nil {
		return nil, fmt.Errorf("kin-openapi validate errors: %v", err)
	}

	return kinDoc, nil

}

// GenFromSchema is a public wrapper that creates default options and generates from schema
func GenFromSchema(schema *openapi3.Schema) *rapid.Generator[json.RawMessage] {
	opts := NewGenerationOptions()
	return opts.GenFromSchema(schema)
}

func ValidatePayload(ctx context.Context, payload []byte, p string, op *openapi3.Operation) error {
	requestValidationInput := &openapi3filter.RequestValidationInput{
		Request: &http.Request{
			Method: "POST",
			URL:    &url.URL{Path: p},
			Body:   io.NopCloser(bytes.NewBuffer(payload)),
			Header: http.Header{"Content-Type": []string{"application/json"}},
		},
	}
	err := openapi3filter.ValidateRequestBody(ctx, requestValidationInput, op.RequestBody.Value)
	return err
}

func GetSchema(op *openapi3.Operation) (*openapi3.SchemaRef, bool) {
	if op == nil || op.RequestBody == nil {
		return nil, false
	}
	media, ok := op.RequestBody.Value.Content["application/json"]
	if !ok {
		return nil, false
	}
	schema := media.Schema

	return schema, true
}
