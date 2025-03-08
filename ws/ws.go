package ws

import (
	"bufio"
	"io"
	"net/http"

	"github.com/gorilla/websocket"
)

func Gateway(h http.Handler) http.Handler {
	upgrader := websocket.Upgrader{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			h.ServeHTTP(w, r)
			return
		}

		// Upgrade connection to WebSocket
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, "could not upgrade connection", http.StatusInternalServerError)
			return
		}

		// WebSocket connection / HTTP request pipes for passing data around
		rReq, wReq := io.Pipe()
		rResp, wResp := io.Pipe()

		// Close request pipe when connection is closed
		conn.SetCloseHandler(func(code int, text string) error {
			return wReq.Close()
		})

		// Read from conn, write to wReq.
		// Close wReq when conn is closed.
		go func() {
			defer wReq.Close()
			for {
				t, p, err := conn.ReadMessage()
				if err != nil {
					return
				}
				switch t {
				case websocket.TextMessage:
					_, err = wReq.Write(p)
				case websocket.BinaryMessage:
					_, err = wReq.Write(p)
				case websocket.CloseMessage:
					return
				default:
					continue
				}
				if err != nil {
					return
				}
			}
		}()

		// Read from rResp, write to conn.
		// Close conn when rResp is closed.
		go func() {
			defer conn.Close()
			s := bufio.NewScanner(rResp)
			for s.Scan() {
				err := conn.WriteMessage(websocket.TextMessage, s.Bytes())
				if err != nil {
					break
				}
			}
			if err != nil {
				conn.WriteMessage(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseInternalServerErr, err.Error()),
				)
			} else if s.Err() != nil {
				conn.WriteMessage(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, err.Error()),
				)
			} else {
				conn.WriteMessage(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				)
			}
		}()

		// Replace request body with one that reads from rReq
		r, err = http.NewRequestWithContext(r.Context(), http.MethodPost, r.URL.String(), eofPipeReader{rReq})
		if err != nil {
			return
		}

		// Replace response writer with one that writes to wResp
		w = &responseForwarder{wResp, w.Header()}

		// Execute HTTP request with wrapped request and response
		h.ServeHTTP(w, r)

		// Close wResp to signal end of response
		wResp.Close()
	})
}

// Implementation of http.ResponseWriter interface that redirects all response
// data to embedded *io.PipeWriter.
type responseForwarder struct {
	*io.PipeWriter
	h http.Header
}

func newResponseForwarder(w *io.PipeWriter, h http.Header) http.ResponseWriter {
	return &responseForwarder{w, h}
}

func (rf *responseForwarder) Header() http.Header {
	return rf.h
}

func (rf *responseForwarder) WriteHeader(int) {
}

func (rf *responseForwarder) Flush() {
}

// Wrapper around *io.PipeReader that returns io.EOF when underlying
// *io.PipeReader is closed.
type eofPipeReader struct {
	*io.PipeReader
}

func (r eofPipeReader) Read(p []byte) (int, error) {
	n, err := r.PipeReader.Read(p)
	if err == io.ErrClosedPipe {
		err = io.EOF
	}
	return n, err
}
