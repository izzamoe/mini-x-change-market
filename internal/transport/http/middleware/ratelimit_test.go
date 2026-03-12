package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/izzam/mini-exchange/internal/transport/http/middleware"
	"github.com/stretchr/testify/assert"
)

func TestRateLimiter_AllowsRequests(t *testing.T) {
	rl := middleware.NewRateLimiter(10, 10, nil)

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First 10 requests (burst capacity) should be allowed.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.168.1.1:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d should be allowed", i+1)
	}
}

func TestRateLimiter_BlocksAfterBurst(t *testing.T) {
	// 1 RPS, burst of 1 → second request from same IP must be throttled.
	rl := middleware.NewRateLimiter(1, 1, nil)

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	makeReq := func() int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:9999"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	// First request consumes the single token.
	assert.Equal(t, http.StatusOK, makeReq())
	// Second request immediately hits rate limit.
	assert.Equal(t, http.StatusTooManyRequests, makeReq())
}

func TestRateLimiter_DifferentIPs_Independent(t *testing.T) {
	// 1 RPS, burst of 1.
	rl := middleware.NewRateLimiter(1, 1, nil)

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	makeReqFrom := func(ip string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	// Each unique IP gets its own bucket, so both should succeed.
	assert.Equal(t, http.StatusOK, makeReqFrom("1.2.3.4"))
	assert.Equal(t, http.StatusOK, makeReqFrom("5.6.7.8"))
}

func TestRecovery_CatchesPanic(t *testing.T) {
	h := middleware.Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	// Must not panic the test itself.
	assert.NotPanics(t, func() { h.ServeHTTP(w, req) })
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCORS_SetsHeaders(t *testing.T) {
	h := middleware.CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_PreFlight(t *testing.T) {
	h := middleware.CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}
