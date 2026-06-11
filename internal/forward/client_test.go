package forward

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/forward-mcp/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestClient_SendChatRequest(t *testing.T) {
	tests := []struct {
		name           string
		request        *ChatRequest
		serverResponse *ChatResponse
		serverStatus   int
		expectError    bool
	}{
		{
			name: "successful request",
			request: &ChatRequest{
				Messages: []map[string]string{
					{"role": "user", "content": "Hello"},
				},
				Model: "test-model",
			},
			serverResponse: &ChatResponse{
				Response: "Hi there!",
				Model:    "test-model",
			},
			serverStatus: http.StatusOK,
			expectError:  false,
		},
		{
			name: "server error",
			request: &ChatRequest{
				Messages: []map[string]string{
					{"role": "user", "content": "Hello"},
				},
				Model: "test-model",
			},
			serverResponse: nil,
			serverStatus:   http.StatusInternalServerError,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request headers
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				auth := base64.StdEncoding.EncodeToString([]byte("test-api-key:test-api-secret"))
				assert.Equal(t, "Basic "+auth, r.Header.Get("Authorization"))

				// Verify request method
				assert.Equal(t, http.MethodPost, r.Method)

				// Set response
				w.WriteHeader(tt.serverStatus)
				if tt.serverResponse != nil {
					err := json.NewEncoder(w).Encode(tt.serverResponse)
					if err != nil {
						t.Errorf("Failed to encode server response: %v", err)
					}
				}
			}))
			defer server.Close()

			// Create client with test server URL
			client := NewClient(&config.ForwardConfig{
				APIKey:     "test-api-key",
				APISecret:  "test-api-secret",
				APIBaseURL: server.URL,
				Timeout:    5,
			})

			// Send request
			resp, err := client.SendChatRequest(tt.request)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, resp)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.serverResponse, resp)
			}
		})
	}
}

func TestClient_GetAvailableModels(t *testing.T) {
	tests := []struct {
		name           string
		serverResponse []string
		serverStatus   int
		expectError    bool
	}{
		{
			name:           "successful request",
			serverResponse: []string{"model-1", "model-2"},
			serverStatus:   http.StatusOK,
			expectError:    false,
		},
		{
			name:           "server error",
			serverResponse: nil,
			serverStatus:   http.StatusInternalServerError,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request headers
				auth := base64.StdEncoding.EncodeToString([]byte("test-api-key:test-api-secret"))
				assert.Equal(t, "Basic "+auth, r.Header.Get("Authorization"))

				// Verify request method
				assert.Equal(t, http.MethodGet, r.Method)

				// Set response
				w.WriteHeader(tt.serverStatus)
				if tt.serverResponse != nil {
					err := json.NewEncoder(w).Encode(tt.serverResponse)
					if err != nil {
						t.Errorf("Failed to encode server response: %v", err)
					}
				}
			}))
			defer server.Close()

			// Create client with test server URL
			client := NewClient(&config.ForwardConfig{
				APIKey:     "test-api-key",
				APISecret:  "test-api-secret",
				APIBaseURL: server.URL,
				Timeout:    5,
			})

			// Get models
			models, err := client.GetAvailableModels()

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, models)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.serverResponse, models)
			}
		})
	}
}

func TestExtractAPIErrorDetail(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected string
	}{
		{
			name:     "NQE runtime error with nested errors",
			body:     `{"apiUrl":"/nqe","httpMethod":"POST","message":"Error encountered while executing the NQE query","reason":"NQE_RUNTIME_ERROR","errors":[{"message":"Expected parameter, 'Operating_Systems', was not provided."}],"snapshotId":"1248212","completionType":"FINISHED"}`,
			expected: "Error encountered while executing the NQE query; Expected parameter, 'Operating_Systems', was not provided. (NQE_RUNTIME_ERROR)",
		},
		{
			name:     "message only",
			body:     `{"message":"Invalid network ID"}`,
			expected: "Invalid network ID",
		},
		{
			name:     "non-JSON body",
			body:     `<html>Bad Request</html>`,
			expected: "",
		},
		{
			name:     "JSON without recognized fields",
			body:     `{"status":400}`,
			expected: "",
		},
		{
			name:     "empty body",
			body:     ``,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, extractAPIErrorDetail([]byte(tt.body)))
		})
	}
}

func TestExtractAPIErrorDetailTruncation(t *testing.T) {
	long := strings.Repeat("x", 600)
	detail := extractAPIErrorDetail([]byte(`{"message":"` + long + `"}`))
	assert.Len(t, detail, 503) // 500 chars + "..."
	assert.True(t, strings.HasSuffix(detail, "..."))
}
