package SpecSmash

import (
	"bytes"
	"fmt"
	"github.com/woodsbury/decimal128"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"

	kinopenapi "github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
)

func ReadSpec(t *testing.T, specPath string) (*kinopenapi.T, error) {
	t.Helper()

	// load spec
	b, err := os.ReadFile(specPath)
	if err != nil {
		return nil, err
	}

	// kin-openapi to reuse our schema generator
	loader := &kinopenapi.Loader{IsExternalRefsAllowed: true}
	kinDoc, err := loader.LoadFromData(b)
	if err != nil {
		return nil, err
	}
	if err := kinDoc.Validate(loader.Context); err != nil {
		return nil, fmt.Errorf("kin-openapi validate errors: %v", err)
	}

	return kinDoc, nil

}

func GetSchema(t *testing.T, op *kinopenapi.Operation) (*kinopenapi.SchemaRef, bool) {
	t.Helper()
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

func ValidatePayload(t *testing.T, payload []byte, p string, op *kinopenapi.Operation) {
	requestValidationInput := &openapi3filter.RequestValidationInput{
		Request: &http.Request{
			Method: "POST",
			URL:    &url.URL{Path: p},
			Body:   io.NopCloser(bytes.NewBuffer(payload)),
			Header: http.Header{"Content-Type": []string{"application/json"}},
		},
	}
	err := openapi3filter.ValidateRequestBody(t.Context(), requestValidationInput, op.RequestBody.Value)
	assert.NoError(t, err, "Validation failed for %s %s", p, string(payload))
}

// GenerateAndValidate loads an OpenAPI spec, finds all POST requestBody schemas with application/json,
// generates N random payloads per path using the generators above, and validates them using pb33f validator.
func GenerateAndValidate(t *testing.T, specPath string) error {

	kinDoc, err := ReadSpec(t, specPath)
	assert.NoError(t, err)

	generationOpts := NewGenerationOptions()

	// iterate paths, focus on POST and application/json requestBody only
	for p, item := range kinDoc.Paths.Map() {
		op := item.Post
		schema, ok := GetSchema(t, op)
		if !ok {
			continue
		}
		gen := generationOpts.GenFromSchema(schema.Value)
		nDraws := 0

		// template http.Request for validator: method POST, URL path p, body as bytes, header content-type
		rapid.Check(t, func(rapidT *rapid.T) {
			payload := gen.Draw(rapidT, "payload")
			nDraws++
			ValidatePayload(t, payload, p, op)

			if nDraws%10000 == 0 {
				fmt.Printf("Generated %d draws\n", nDraws)
			}
		})
	}

	return nil
}

func TestGenerateAndValidateSimple(t *testing.T) {
	err := GenerateAndValidate(t, "testdata/openapi_simple.yaml")
	if err != nil {
		t.Fatalf("GenerateAndValidate failed: %v", err)
	}
}

func TestGenerateAndValidateComprehensive(t *testing.T) {
	err := GenerateAndValidate(t, "testdata/openapi_comprehensive.yaml")
	if err != nil {
		t.Fatalf("GenerateAndValidate failed: %v", err)
	}
}

func TestCheck(t *testing.T) {
	specPath := "testdata/openapi_comprehensive.yaml"

	var testTable = []struct {
		path    string
		opName  string
		payload []byte
	}{
		{
			"/events",
			"Post",
			[]byte(`
			     	  {
						"amount": {
						  "amount": 0.02,
						  "currency": "USD"
						},
						"ts": "2025-09-30T11:57:43Z",
						"type": "purchase"
					  }
			`),
		},
		{
			"/events",
			"Post",
			[]byte(`
			     	  {
						"amount": {
						  "amount": 92954.52,
						  "currency": "USD"
						},
						"ts": "2025-09-30T11:57:43Z",
						"type": "purchase"
					  }
			`),
		},
		// Just too high
		//{
		//	"/events",
		//	"Post",
		//	[]byte(`
		//	     	  {
		//				"amount": {
		//				  "amount": 117981427092954.52,
		//				  "currency": "USD"
		//				},
		//				"ts": "2025-09-30T11:57:43Z",
		//				"type": "purchase"
		//			  }
		//	`),
		//},
	}

	value := float64(117981427092954.52)
	v := float64(0.01)

	valuaString := fmt.Sprintf("%g", value)
	vString := fmt.Sprintf("%g", v)

	numParsed, err := decimal128.Parse(valuaString)
	assert.NoError(t, err)
	denParsed, err := decimal128.Parse(vString)
	assert.NoError(t, err)
	_, remainder := numParsed.QuoRem(denParsed)
	isZero := remainder.IsZero()
	assert.True(t, isZero, "IsZero failed for %s %s", specPath, value)

	for i, tt := range testTable {
		name := fmt.Sprintf("%s-%s-%v", tt.opName, tt.path, i)
		t.Run(name, func(t *testing.T) {
			kinDoc, err := ReadSpec(t, specPath)
			assert.NoError(t, err)

			var op *kinopenapi.Operation
			switch tt.opName {
			case "Post":
				op = kinDoc.Paths.Value(tt.path).Post
			default:
				t.Fatalf("unknown operation %s", tt.opName)
			}

			ValidatePayload(t, tt.payload, tt.path, op)

		})

	}

}

func TestGenerateAndValidateUltraComprehensive(t *testing.T) {
	err := GenerateAndValidate(t, "testdata/openapi_ultra_comprehensive.yaml")
	if err != nil {
		t.Fatalf("GenerateAndValidate failed: %v", err)
	}
}
