package auth

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLogger_DoesNotLogClientIPOrAuthorization(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handler := RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "198.51.100.20:4321"
	request.Header.Set("Authorization", "Bearer highly-sensitive-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	output := logs.String()
	for _, sensitive := range []string{"198.51.100.20", "highly-sensitive-token", "Authorization"} {
		if strings.Contains(output, sensitive) {
			t.Fatalf("request log contains sensitive value %q: %s", sensitive, output)
		}
	}
}
