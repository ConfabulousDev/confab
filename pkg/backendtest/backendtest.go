// Package backendtest provides shared helpers for tests that stand up a
// mock Confab backend with net/http/httptest.
//
// It exists because pkg/http compresses request bodies over 1KB with zstd
// (see pkg/http/client.go). A mock handler that decodes r.Body as raw JSON
// therefore works only while payloads stay small, and silently mis-decodes
// larger ones into zero-valued structs — a false green, or (when the
// handler echoes a line count back) a spurious re-send. Read every mock
// backend's body through ReadRequestBody so body size never changes test
// behavior.
package backendtest

import (
	"io"
	"net/http"

	"github.com/klauspost/compress/zstd"
)

// zstdDecoder is stateless for DecodeAll and safe for concurrent use, so
// one package-level decoder serves every test.
var zstdDecoder, _ = zstd.NewReader(nil)

// ReadRequestBody reads r.Body, transparently decompressing it when the
// client sent Content-Encoding: zstd.
func ReadRequestBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	return Decompress(r.Header.Get("Content-Encoding"), body), nil
}

// Decompress returns body decoded per contentEncoding. Use it when the
// test needs the raw bytes too (e.g. to assert on compressed transfer
// size); otherwise prefer ReadRequestBody. Returns body unchanged for any
// encoding other than zstd, and on a decode failure — a mock backend
// should not panic on a malformed body.
func Decompress(contentEncoding string, body []byte) []byte {
	if contentEncoding != "zstd" {
		return body
	}
	out, err := zstdDecoder.DecodeAll(body, nil)
	if err != nil {
		return body
	}
	return out
}
