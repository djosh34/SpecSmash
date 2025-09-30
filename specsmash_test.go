package SpecSmash

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"

	"github.com/getkin/kin-openapi/openapi3"
)

// GenerateAndValidate loads an OpenAPI spec, finds all POST requestBody schemas with application/json,
// generates N random payloads per path using the generators above, and validates them using pb33f validator.
func GenerateAndValidate(t *testing.T, specPath string) error {

	kinDoc, err := ReadSpec(specPath)
	assert.NoError(t, err)

	generationOpts := NewGenerationOptions()

	// iterate paths, focus on POST and application/json requestBody only
	for p, item := range kinDoc.Paths.Map() {
		op := item.Post
		schema, ok := GetSchema(op)
		if !ok {
			continue
		}
		gen := generationOpts.GenFromSchema(schema.Value)
		nDraws := 0

		// template http.Request for validator: method POST, URL path p, body as bytes, header content-type
		rapid.Check(t, func(rapidT *rapid.T) {
			payload := gen.Draw(rapidT, "payload")
			nDraws++
			err = ValidatePayload(rapidT.Context(), payload, p, op)
			assert.NoError(t, err, "Validation failed for %s %s", p, string(payload))

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

	// value := float64(117981427092954.52)
	// v := float64(0.01)

	// valuaString := fmt.Sprintf("%g", value)
	// vString := fmt.Sprintf("%g", v)

	// numParsed, err := decimal128.Parse(valuaString)
	// assert.NoError(t, err)
	// denParsed, err := decimal128.Parse(vString)
	// assert.NoError(t, err)
	// _, remainder := numParsed.QuoRem(denParsed)
	// isZero := remainder.IsZero()
	// assert.True(t, isZero, "IsZero failed for %s %s", specPath, value)

	for i, tt := range testTable {
		name := fmt.Sprintf("%s-%s-%v", tt.opName, tt.path, i)
		t.Run(name, func(t *testing.T) {
			kinDoc, err := ReadSpec(specPath)
			assert.NoError(t, err)

			var op *openapi3.Operation
			switch tt.opName {
			case "Post":
				op = kinDoc.Paths.Value(tt.path).Post
			default:
				t.Fatalf("unknown operation %s", tt.opName)
			}

			err = ValidatePayload(t.Context(), tt.payload, tt.path, op)
			assert.NoError(t, err, "Validation failed for %s %s", tt.path, string(tt.payload))

		})

	}

}

func TestGenerateAndValidateUltraComprehensive(t *testing.T) {
	err := GenerateAndValidate(t, "testdata/openapi_ultra_comprehensive.yaml")
	if err != nil {
		t.Fatalf("GenerateAndValidate failed: %v", err)
	}
}
