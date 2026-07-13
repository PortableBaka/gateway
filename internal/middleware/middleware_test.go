package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestChain_OrdersMiddlewareOutermostFirst pins the exact composition order
// Chain produces: the first middleware listed must be the outermost, so it
// runs before every other middleware on the way in and after all of them on
// the way out. Every earlier stage's ordering decisions (RequestID before
// Recover before Log, etc.) depend on this.
func TestChain_OrdersMiddlewareOutermostFirst(t *testing.T) {
	var order []string

	mark := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+":enter")
				next.ServeHTTP(w, r)
				order = append(order, name+":exit")
			})
		}
	}

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	})

	handler := Chain(final, mark("first"), mark("second"))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"first:enter", "second:enter", "handler", "second:exit", "first:exit"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q (full: %v)", i, order[i], want[i], order)
		}
	}
}

func TestChain_NoMiddlewareReturnsHandlerUnchanged(t *testing.T) {
	called := false
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	handler := Chain(final)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !called {
		t.Error("handler was not invoked")
	}
}
