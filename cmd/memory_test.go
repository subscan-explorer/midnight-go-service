package main

import (
	"context"
	"github.com/stretchr/testify/mock"
	"midnight-go-sdk/compile"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type unreadBody struct{ t *testing.T }

func (b unreadBody) Read([]byte) (int, error) {
	b.t.Fatal("rejected request body was decoded")
	return 0, nil
}
func (b unreadBody) Close() error { return nil }

func TestAdmissionRejectsBeforeDecodeAndReleasesAfterErrors(t *testing.T) {
	rt := compile.NewRuntime()
	for rt.VerifyRequests.TryAcquire() {
	}
	conf := &compile.Config{Runtime: rt}
	handler := compileHandler(conf)
	req := httptest.NewRequest("POST", "/verify", nil)
	req.Body = unreadBody{t}
	response := httptest.NewRecorder()
	handler(response, req)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") == "" {
		t.Fatalf("got %d", response.Code)
	}
	rt.VerifyRequests.Release()
	for i := 0; i < 2; i++ {
		response = httptest.NewRecorder()
		handler(response, httptest.NewRequest("POST", "/verify", strings.NewReader("bad json")))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("admission leaked on error: %d", response.Code)
		}
	}
}

func TestVerifyBodyLimit(t *testing.T) {
	rt := compile.NewRuntime()
	rt.VerifyBodyLimit = 64
	handler := compileHandler(&compile.Config{Runtime: rt})
	for _, knownLength := range []bool{false, true} {
		req := httptest.NewRequest("POST", "/verify", strings.NewReader(`{"source_code":"`+strings.Repeat("x", 100)+`"}`))
		if !knownLength {
			req.ContentLength = -1
		}
		response := httptest.NewRecorder()
		handler(response, req)
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("got %d: %s", response.Code, response.Body.String())
		}
	}
}

func TestStateAdmissionAndCMAView(t *testing.T) {
	rt := compile.NewRuntime()
	for rt.StateRequests.TryAcquire() {
	}
	response := httptest.NewRecorder()
	stateHandler(&compile.Config{Runtime: rt})(response, httptest.NewRequest("GET", "/contract/state?contract_addr=addr", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", response.Code)
	}
	m := new(MockICompile)
	m.On("IsContractVerified", mock.Anything, "addr").Return(true)
	m.On("ReadContractCMA", mock.Anything, "addr", "").Return(&compile.ContractStateResponse{}, nil).Once()
	response = httptest.NewRecorder()
	stateHandler(m)(response, httptest.NewRequest("GET", "/contract/state?contract_addr=addr&view=cma", nil).WithContext(context.Background()))
	if response.Code != http.StatusOK {
		t.Fatalf("got %d", response.Code)
	}
	m.AssertExpectations(t)
}
