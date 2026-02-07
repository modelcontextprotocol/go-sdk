module auth-middleware-example

go 1.23.0

require (
	github.com/golang-jwt/jwt/v5 v5.2.2
	github.com/modelcontextprotocol/go-sdk v0.3.0
)

require (
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.30.0 // indirect
)

replace github.com/modelcontextprotocol/go-sdk => ../../../
