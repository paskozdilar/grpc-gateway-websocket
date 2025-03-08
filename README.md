# grpc-gateway-websocket

WebSocket gateway compatible with gRPC Gateway.

## Usage

To enable WebSocket communication, simply wrap your HTTP handler:

```go
import "github.com/paskozdilar/grpc-gateway-websocket/ws"

func run(h http.Handler) {
    h = ws.Gateway(h)
    http.ListenAndServe(":8080", h)
}
```

## Notes

- Server-streaming and bidirectional methods return response message wrapped in
  a `{"result": ...}` JSON object.
- Currently, gRPC Gateway has issues with WebSocket on methods that don't
  define `option (google.api.http) = { body: "*" }` annotation:
  https://github.com/grpc-ecosystem/grpc-gateway/issues/5326
