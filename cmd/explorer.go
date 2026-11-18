package main

import (
	"encoding/json"
	"errors"
	"io"
	"midnight-go-sdk/compile"
	"midnight-go-sdk/logger"
	"net/http"
)

func explorerHandler(c compile.ICompile) http.HandlerFunc {
	limits := handlerLimits(c)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			requestError(w, r, 405, "method_not_allowed", "Only POST is allowed")
			return
		}
		var request compile.ExplorerRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			requestError(w, r, 400, "invalid_request", "Invalid explorer request")
			return
		}
		var trailing interface{}
		if err := decoder.Decode(&trailing); err != io.EOF {
			requestError(w, r, 400, "invalid_request", "Unexpected trailing data")
			return
		}
		if err := request.Validate(); err != nil {
			requestError(w, r, 400, "invalid_request", err.Error())
			return
		}
		if !limits.StateRequests.TryAcquire() {
			respondBusy(w, r)
			return
		}
		defer limits.StateRequests.Release()
		active, err := resolveCompileByNetwork(r.Context(), c, request.NetworkID)
		if err != nil {
			requestError(w, r, 400, "invalid_network", "Invalid network")
			return
		}
		conf, ok := active.(*compile.Config)
		if !ok {
			requestError(w, r, 503, "unavailable", "Explorer operations unavailable")
			return
		}
		result, err := conf.Explore(r.Context(), request)
		if err != nil {
			var reader *compile.StateReadError
			if errors.As(err, &reader) {
				w.Header().Set("X-Midnight-Error-Code", reader.Code)
			}
			respondStateError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if _, err := w.Write(result); err != nil {
			logger.Logger().Error(err)
		}
	}
}
