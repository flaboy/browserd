package types

import (
	"encoding/json"
	"net/http"
)

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Response[T any] struct {
	Data  *T         `json:"data"`
	Error *ErrorBody `json:"error"`
}

func WriteOK[T any](w http.ResponseWriter, status int, data T) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Response[T]{Data: &data, Error: nil})
}

func WriteErr(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Response[map[string]any]{
		Data: nil,
		Error: &ErrorBody{
			Code:    code,
			Message: message,
		},
	})
}
