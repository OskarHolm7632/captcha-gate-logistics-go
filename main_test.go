package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignupRejectsWithoutCaptchaBeforeCreatingUser(t *testing.T) {
	called := ""
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"ok":false,"data":null,"error":{"code":"CAPTCHA_REJECTED","message":"rejected"},"metadata":{}}`))
	}))
	defer remote.Close()
	s := &service{api: &InfraiClient{baseURL: remote.URL, key: "test", httpClient: remote.Client()}, store: &store{}}
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(`{"email":"a@example.com","password":"pass","name":"A"}`))
	rec := httptest.NewRecorder()
	s.signup(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || called != "/captcha/verify" {
		t.Fatalf("status=%d path=%s", rec.Code, called)
	}
}
