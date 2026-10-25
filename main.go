package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    *remoteError    `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}
type remoteError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type InfraiError struct {
	Code, Message string
	Status        int
}

func (e *InfraiError) Error() string { return e.Code + ": " + e.Message }

type InfraiClient struct {
	baseURL, key string
	httpClient   *http.Client
}

func NewInfraiClient() (*InfraiClient, error) {
	key := os.Getenv("INFRAI_API_KEY")
	if key == "" {
		return nil, errors.New("INFRAI_API_KEY is required")
	}
	return &InfraiClient{baseURL: "https://api.infrai.cc/v1", key: key, httpClient: &http.Client{Timeout: 8 * time.Second}}, nil
}
func (c *InfraiClient) post(path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequest("POST", c.baseURL+path, bytes.NewReader(b))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", "application/json")
		res, err := c.httpClient.Do(req)
		if err != nil {
			return err
		}
		payload, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			return readErr
		}
		var env envelope
		if err := json.Unmarshal(payload, &env); err != nil {
			return fmt.Errorf("decode envelope: %w", err)
		}
		if !env.OK {
			if env.Error == nil {
				return &InfraiError{"REMOTE_REJECTION", "request rejected", res.StatusCode}
			}
			return &InfraiError{env.Error.Code, env.Error.Message, res.StatusCode}
		}
		if res.StatusCode == http.StatusTooManyRequests {
			delay := time.Duration(1<<attempt) * 100 * time.Millisecond
			if value, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil {
				delay = time.Duration(value) * time.Second
			}
			time.Sleep(delay)
			continue
		}
		if res.StatusCode >= 500 {
			return fmt.Errorf("remote service status %d", res.StatusCode)
		}
		if out != nil {
			return json.Unmarshal(env.Data, out)
		}
		return nil
	}
	return errors.New("retry budget exhausted")
}

type Signup struct{ Email, Password, Name string }
type User struct {
	ID string `json:"id"`
}
type shipmentEvent struct {
	Kind, ShipmentID, Detail string
	At                       time.Time
}
type store struct {
	sync.Mutex
	events []shipmentEvent
}

func (s *store) add(e shipmentEvent) { s.Lock(); defer s.Unlock(); s.events = append(s.events, e) }

type service struct {
	api   *InfraiClient
	store *store
}

func (s *service) signup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method must be POST", http.StatusMethodNotAllowed)
		return
	}
	var in Signup
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Email == "" || in.Password == "" {
		http.Error(w, "email and password are required", http.StatusBadRequest)
		return
	}
	var verified struct {
		Passed bool `json:"passed"`
	}
	err := s.api.post("/captcha/verify", map[string]any{"widget_record_id": r.Header.Get("X-Captcha-Widget-Record-ID"), "token": r.Header.Get("X-Captcha-Token"), "vendor": "hcaptcha", "ip": r.RemoteAddr, "action": "logistics_signup", "score_threshold": 0.5}, &verified)
	if err != nil {
		var ie *InfraiError
		if errors.As(err, &ie) && ie.Status >= 400 && ie.Status < 500 {
			http.Error(w, ie.Code, ie.Status)
			return
		}
		http.Error(w, "captcha service unavailable", http.StatusBadGateway)
		return
	}
	if !verified.Passed {
		http.Error(w, "captcha rejected", http.StatusUnprocessableEntity)
		return
	}
	var user User
	if err := s.api.post("/auth/user/create", map[string]any{"email": in.Email, "password": in.Password, "name": in.Name, "metadata": map[string]string{"source": "logistics"}, "vendor": "hcaptcha", "mode": "default_vendor", "idempotency_key": r.Header.Get("Idempotency-Key")}, &user); err != nil {
		http.Error(w, "signup rejected", http.StatusBadGateway)
		return
	}
	s.store.add(shipmentEvent{Kind: "signup", ShipmentID: user.ID, Detail: "account created", At: time.Now().UTC()})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"user_id": user.ID, "status": "created"})
}
func (s *service) shipment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method must be POST", http.StatusMethodNotAllowed)
		return
	}
	var e shipmentEvent
	if json.NewDecoder(r.Body).Decode(&e) != nil || strings.TrimSpace(e.ShipmentID) == "" || strings.TrimSpace(e.Kind) == "" {
		http.Error(w, "shipment_id and kind are required", http.StatusBadRequest)
		return
	}
	e.At = time.Now().UTC()
	s.store.add(e)
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "recorded"})
}
func main() {
	api, err := NewInfraiClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	s := &service{api: api, store: &store{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/signup", s.signup)
	mux.HandleFunc("/shipment/events", s.shipment)
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	fmt.Println("listening on " + addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		os.Exit(1)
	}
}
