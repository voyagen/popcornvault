package api

import _ "embed"

// OpenAPISpec holds the raw OpenAPI 3.0 specification YAML.
//
//go:embed openapi.yaml
var OpenAPISpec []byte
