module example-websocket-server

go 1.25.3

require (
	github.com/gorilla/websocket v1.5.3
	github.com/modelcontextprotocol/go-sdk v1.1.0
)

require (
	github.com/google/jsonschema-go v0.3.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.30.0 // indirect
)

replace github.com/modelcontextprotocol/go-sdk => ../../..
