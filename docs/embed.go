// Package docs embeds the generated OpenAPI spec for the /swagger route
// (internal/routes/routes.go). swagger.json/swagger.yaml are generated, not
// hand-written — regenerate them after editing handler @-annotations with:
//
//	go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/api/main.go -o docs --parseDependency --parseInternal
//
// Deliberately just an embedded static file rather than importing
// github.com/swaggo/swag's runtime Register/ReadDoc API: that package's root
// import pulls in the full go/ast-based codegen toolchain (go/parser,
// golang.org/x/tools/go/loader, etc.) as a production dependency just to
// serve a JSON blob — disproportionate for what this needs. The swag CLI
// itself (invoked above via `go run`, not `go get`) never touches this
// module's own go.mod/go.sum.
package docs

import _ "embed"

//go:embed swagger.json
var JSON []byte
