package builder

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestParamToString_AllOfReference(t *testing.T) {
	param := &openapi3.Parameter{
		Schema: &openapi3.SchemaRef{
			Value: &openapi3.Schema{
				AllOf: []*openapi3.SchemaRef{
					{Ref: "#/components/schemas/ResourceType"},
				},
			},
		},
	}

	got := paramToString("*p.ResourceParentType", param)

	if got != "string(*p.ResourceParentType)" {
		t.Fatalf("expected conversion for referenced schema inside allOf, got %q", got)
	}
}
