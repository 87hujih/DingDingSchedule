package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"schedule_server/internal/agent/tools"
)

func TestLLMClientChatStructuredSendsJSONObjectOptions(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeStructuredTestCompletion(t, w)
	}))
	defer server.Close()

	client := NewLLMClient(server.URL, "test-key", "test-model")
	result, err := client.ChatStructured(context.Background(), []tools.Message{
		{Role: "user", Content: "hello"},
	}, StructuredOutputSpec{
		Mode:                 "json_object",
		Temperature:          0,
		MaxTokens:            512,
		TransportMaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("ChatStructured() error = %v", err)
	}
	if result.Attempts != 1 {
		t.Fatalf("Attempts = %d, want 1", result.Attempts)
	}
	format, ok := captured["response_format"].(map[string]any)
	if !ok || format["type"] != "json_object" {
		t.Fatalf("response_format = %#v, want json_object", captured["response_format"])
	}
	if temperature, ok := captured["temperature"].(float64); !ok || temperature != 0 {
		t.Fatalf("temperature = %#v, want explicit zero", captured["temperature"])
	}
	if maxTokens, ok := captured["max_tokens"].(float64); !ok || maxTokens != 512 {
		t.Fatalf("max_tokens = %#v, want 512", captured["max_tokens"])
	}
}

func TestLLMClientChatStructuredRetries503Once(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		writeStructuredTestCompletion(t, w)
	}))
	defer server.Close()

	client := NewLLMClient(server.URL, "test-key", "test-model")
	result, err := client.ChatStructured(context.Background(), []tools.Message{{Role: "user", Content: "hello"}}, StructuredOutputSpec{
		Mode:                 "json_object",
		MaxTokens:            512,
		TransportMaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("ChatStructured() error = %v", err)
	}
	if result.Attempts != 2 || calls.Load() != 2 {
		t.Fatalf("result attempts/calls = %d/%d, want 2/2", result.Attempts, calls.Load())
	}
}

func TestLLMClientChatStructuredDoesNotRetry500(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "not retryable for structured compiler", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewLLMClient(server.URL, "test-key", "test-model")
	result, err := client.ChatStructured(context.Background(), []tools.Message{{Role: "user", Content: "hello"}}, StructuredOutputSpec{
		Mode:                 "json_object",
		MaxTokens:            512,
		TransportMaxAttempts: 2,
	})
	if err == nil {
		t.Fatal("ChatStructured() error = nil, want HTTP 500 error")
	}
	if result.Attempts != 1 || calls.Load() != 1 {
		t.Fatalf("result attempts/calls = %d/%d, want 1/1", result.Attempts, calls.Load())
	}
}

func TestLLMClientLegacyChatDoesNotSendStructuredFields(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeStructuredTestCompletion(t, w)
	}))
	defer server.Close()

	client := NewLLMClient(server.URL, "test-key", "test-model")
	if _, err := client.Chat(context.Background(), []tools.Message{{Role: "user", Content: "hello"}}, nil); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	for _, field := range []string{"response_format", "temperature", "max_tokens"} {
		if _, exists := captured[field]; exists {
			t.Fatalf("legacy Chat request unexpectedly contains %q: %#v", field, captured)
		}
	}
}

func writeStructuredTestCompletion(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{}","tool_calls":[]},"finish_reason":"stop"}]}`)); err != nil {
		t.Errorf("write response: %v", err)
	}
}
