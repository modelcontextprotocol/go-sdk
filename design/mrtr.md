## Context

A proposal for implementing Multi Round-Trip Requests
(MRTR) as defined in [SEP-2322](https://github.com/CaitieM20/modelcontextprotocol/blob/de6d76fba3078eda957dadb3cec51ca8ab851b5c/seps/2322-MRTR.md).

In the new protocol version servers can't initiate requests to clients, but when a server requires additional input for completing `tools/call`, `prompts/get`, or `resources/read` it can return an incomplete result along with a set of `inputRequests`. The client fulfills them locally and retries the same call with `inputResponses` attached.

## Goals

**Must have:**

* Backward compatibility.
* Correct representation on the wire.

**Nice to have:**

* Minimal changes to the exported API surface.
* Hard for server implementers to construct an invalid payload.
* Simple input request handling for clients.
* Protocol-version-independent code.
* Consistency with the rest of the SDK.

## Proposal

`ServerSession` methods return an error for new-version protocol connections.

`InputRequest`/`InputResponse` is introduced as a sealed-interface:
```go
// Implemented by *ElicitParams, *CreateMessageParams, *ListRootsParams
type InputRequest interface{ isInputRequest() }

type InputRequestMap map[string]InputRequest
// MarshalJSON encodes as map[string]struct{ Method string; Params InputRequest }
func (m InputRequestMap) MarshalJSON() ([]byte, error) { ... }
// UnmarshalJSON decodes from map[string]struct{ Method string; Params InputRequest }
func (m *InputRequestMap) UnmarshalJSON(data []byte) error { ... }

// Implemented by *ElicitResult, *CreateMessageResult, *ListRootsResult.
type InputResponse interface{ isInputResult() }

type InputResponseMap map[string]InputResponse
// MarshalJSON encodes as map[string]struct{ Method string; Result InputResponse }
func (m InputResponseMap) MarshalJSON() ([]byte, error) { ... }
// UnmarshalJSON decodes from map[string]struct{ Method string; Result InputResponse }
func (m *InputResponseMap) UnmarshalJSON(data []byte) error { ... }
```

All affected methods' `*Params` are extended with `InputResponseMap` and `RequestState` fields:
```go
type CallToolParams struct {
  ...
  InputResponses InputResponseMap `json:"inputResponses,omitempty"`
  RequestState  string            `json:"requestState,omitempty"`
}
// Same for GetPromptParams, ReadResourceParams
```

`InputRequests` and `RequestState` fields are added directly to `CallToolResult`, `GetPromptResult`, and `ReadResourceResult` as exported.
Result type discriminator (completed, input_required) is unexported so that SDK users don't need to set it to the correct constant in addition to setting either `Content` or `InputRequests`. Handler execution result is validated and augmented before marshaling:
```go
type CallToolResult struct {
  ...
  InputRequests InputRequestMap `json:"inputRequests,omitempty"`
  RequestState  string          `json:"requestState,omitempty"`
  resultType    string          // set by the SDK and used in MarshalJSON()
}
// Same for GetPromptResult, ReadResourceResult.
```
Alternatively, the field could only exist on `wire struct`, but this would make us return `complete` to older clients or empty string to newer clients, because there's no access to negotiated protocol version in `MarshalJSON`.

Servers request additional input by constructing a correct struct literal:
```go
mcp.AddTool(s, tool, func(ctx context.Context, req *mcp.CallToolRequest, in MyIn) (*mcp.CallToolResult, MyOut, error) {
  if !hasConfirmation(in) {
    return &mcp.CallToolResult{
      InputRequests: InputRequestMap{"confirm": &mcp.ElicitParams{Message: "Sure?"}},
      RequestState:  "state-token",
    }, zero, nil
  }
  return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, myOut, nil
})
```
An incomplete result should be converted to an error response when communicating with an older client. This is a reasonable default for server implementers to not branch by protocol version in their code.

An unexported middleware is installed in the client, which similarly to `urlElicitationMiddleware` will automatically invoke handlers for the corresponding methods on incomplete results and retry the original request. `ClientOptions` will be extended with configuration knobs:
```go
type MRTROptions struct {
  ...
  MaxRetries int
  Disabled   bool
}
client := mcp.NewClient(impl, &mcp.ClientOptions{
  MRTR: &mcp.MRTROptions{MaxRetries: 3},
})
```

Alternatively, clients have an option to disable it and write a retry loop manually:
```go
client := mcp.NewClient(impl, &mcp.ClientOptions{MRTR: &mcp.MRTROptions{Disabled: true}})
result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "my-tool"})
if result.InputRequests != nil { ... }
```

This is error-prone because according to the SEP an empty map can be returned for load-shedding purposes. `NeedsInput` function can be provided, but nothing would force users to use it. 

**Pros**

This is arguably the simplest and the most transparent approach which is also closest to the spec.
What gets explicitly set on the server can be observed on the wire and on the client. 
The opt-out middleware follows the principle of the least surprise for app developers. If client method handlers were provided they will continue to be invoked regardless of the protocol version in use. The `Disabled` option lets "power-users" build any custom handling logic. 

**Cons**

The biggest downside of the proposal is that server developers can construct incorrect responses and this will only be validated at runtime.

## Alternatives considered

### Unexported fields

MRTR fields can be unexported, accessible only through getters, constructible only through constructor functions, and handled explicitly in custom `(Unm|M)arshalJSON`. This will make it impossible for developers to construct incorrect responses and for clients to perform an erroneous `len(result.InputRequests) > 0` check in the load-shedding case.
```go
type CallToolResult struct {
  ...
  inputRequests InputRequestMap
  requestState  string          
  resultType    string
}

func (r *CallToolResult) InputRequests() (InputRequestMap, bool) { ... }

// InputRequiredResult struct exists for backward-compatibility in case of new fields being needed for input request results.
type InputRequiredResult struct {
  InputRequests InputRequestMap
  RequestState  string
}

// RequireInput constructs a tool call, prompt or resource result with input requests set.
// mrtrResult provides methods for setting private fields on these types.
func RequireInput[T any, TP interface { *T; mrtrResult }](r InputRequiredResult) TP { ... }
```

On the server:
```go
mcp.AddTool(s, tool, func(ctx context.Context, req *mcp.CallToolRequest, in MyIn) (*mcp.CallToolResult, MyOut, error) {
  if !hasConfirmation(in) {
    return mcp.RequireInput[mcp.CallToolResult](mcp.InputRequiredResult{
      InputRequests: mcp.InputRequestMap{"confirm": &mcp.ElicitParams{Message: "Deploy to production?"}},
      RequestState:  "deployment-123",
    }), nil, nil
  }
  return &mcp.CallToolResult{ Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, myOut, nil
})
```

On the client:
```go
result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "my-tool"})
if requests, ok := result.InputRequests(); ok { ... }
```

The biggest downside of this approach is the obscure data model with hidden fields. An incomplete `mcp.CallToolResult` looks like an uninitialized struct until `InputRequests` method result is examined. 
In addition to this, the verbose `RequireInput` syntax (no auto type inference from assignment target) does not look idiomatic and fits poorly into the existing SDK APIs.

---

### `InputRequiredError` type

We could explore a different data channel - `error` return value. This would give us the natural "happy path is when all inputs are provided" flow on the server side, and good result interpretability on the client side (impossible to confuse with a successful response).
The new error could be converted to the correct wire representation at the marshaling stage.
```go
type InputRequiredError struct {
  InputRequests InputRequestMap
  RequestState  string
}

func (e *InputRequiredError) Error() string {
  return fmt.Sprintf("input required: %d request(s)", len(e.InputRequests))
}
```

On the server:
```go
mcp.AddTool(s, tool, func(ctx context.Context, req *mcp.CallToolRequest, in MyIn) (*mcp.CallToolResult, MyOut, error) {
  if !hasConfirmation(in) {
    return nil, zero, &mcp.InputRequiredError{
      InputRequests: mcp.InputRequestMap{"confirm": &mcp.ElicitParams{Message: "Sure?"}}, 
      RequestState:  "state-token",
    }
  }
  return &mcp.CallToolResult{ Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, myOut, nil
})
```

On the client:
```go
result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "my-tool"})
var inputReqErr *mcp.InputRequiredError
if errors.As(err, &inputReqErr) { ... }
```

The downsides of this approach are:
* The drift from the protocol, where MRTR is not an error flow.
* Obscure "customError -> non-error protocol type on wirte -> customError" data lifecycle. 
* Things get confusing for error-processing middleware.

---

### New functions

We could introduce new functions with a different handler signature where the return type is a sealed interface. This would give us compiler-enforced correctness for values constructed by tool handlers and clients would be forced to unpack `mcp.RoundTripCallToolResult` and make a concious decision for how to handle it.
```go
type RoundTripToolHandler func(context.Context, *CallToolRequest) (RoundTripCallToolResult, error)
type RoundTripToolHandlerFor[In, Out any] func(context.Context, *CallToolRequest, In) (RoundTripCallToolResult, Out, error)

// RoundTripCallToolResult is implemented by CallToolResult and IncompleteResult
type RoundTripCallToolResult interface { isMRTRResult() }

type IncompleteResult struct {
  ...
  InputRequests InputRequestMap `json:"inputRequests,omitempty"`
  RequestState  string          `json:"requestState,omitempty"`
}

func (s *Server) AddRoundTripTool(t *Tool, h RoundTripToolHandler)
func AddRoundTripTool[In, Out any](s *Server, t *Tool, h RoundTripToolHandlerFor[In, Out])
```

`Server.AddTool` wraps the old `ToolHandler` into a `RoundTripToolHandler` to update its function signature:
```go
mcp.AddRoundTripTool(s, tool, func(ctx context.Context, req *mcp.CallToolRequest, in MyIn) (mcp.RoundTripCallToolResult, MyOut, error) {
  if needsInput(in) {
    return &mcp.IncompleteResult{
      ResultType: mcp.ResultTypeInputRequired,
      InputRequests: InputRequestMap{"confirm": &mcp.ElicitParams{Message: "Sure?"}},
    }, zero, nil
  }
  return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, myOut, nil
})
```

The downsides of this approach are:
* SEP suggests `ResultType` will potentially be extended with new values, `RoundTrip` in new function names will not allow us to cleanly extend the sealed interface with new types. But an overly generic name for new functions will make the API use-case less clear.
* Different code 
* SDK takes the same action (puts it on the wire) regardless of the returned type, it exists only for enforcing correctness of the user code. 
* Exported API surface bloat: +7 exported functions.

---

### Exported Middleware

We could flip "unexported MRTR middleware with opt-out option" to "exported middleware with opt-in requirement".
```go
func AutoMRTR(opts *MRTROptions) Middleware { ... }
type MRTROptions struct {
  MaxRetries int
}
client := mcp.NewClient(impl, nil)
client.AddSendingMiddleware(mcp.AutoMRTR(&mcp.MRTROptions{
  MaxRetries: 5,
}))
```
This would change semantics of `*Handler` fields - depending on the protocol version in use, an extra initialization step will be required for them to "take effect".

---

### Protocol version bridging

When a handler returns an input-required result to an old client the SDK could bridge by invoking `ServerSession.Elicit`/`CreateMessage`/`ListRoots` on the `ServerSession` and re-invoking the handler with the collected `inputResponses`:
```go
func mrtrBridgeMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
  return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
    res, err := next(ctx, method, req)
    if err != nil || !needsInput(res) || isNewProtocol(req) {
      return res, err
    }
    // Fulfill input requests via server-initiated calls, inject inputResponses into the request, 
    // re-invoke the handler.
  }
}
```

Or even more radically: instead of making `ServerSession.Elicit`, `CreateMessage`, and `ListRoots` return errors for newer clients, we could convert these invocations into MRTR wire format transparently: suspend the handler, return `input_required`, and resume when the client retries with `inputResponses`:
```go
mcp.AddTool(s, tool, func(ctx context.Context, req *mcp.CallToolRequest, in MyIn) (*mcp.CallToolResult, MyOut, error) {
  result, err := req.Session.Elicit(ctx, &mcp.ElicitParams{Message: "Deploy to production?"})
  if err != nil {
    return nil, zero, err
  }
  if result.Action != "accept" {
    return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "cancelled"}}}, zero, nil
  }
  return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "deployed"}}}, myOut, nil
})
```

Such bridging would contradict the design goal of MRTR — servers shouldn't hold resources between round trips, and it should be possible for a retry to arrive on any server instance in a multi-server deployment.

