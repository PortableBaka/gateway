package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type ctxKey int

const requestIdKey ctxKey = iota

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")

		if requestID == "" {
			requestID = uuid.New().String()
		}

		ctx := context.WithValue(r.Context(), requestIdKey, requestID)

		w.Header().Set("X-Request-ID", requestID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func GetRequestId(ctx context.Context) string {
	id, _ := ctx.Value(requestIdKey).(string)

	return id
}
