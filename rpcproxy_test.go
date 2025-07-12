package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Mock HTTP server for testing
type mockServer struct {
	server *httptest.Server
	responses map[string]string
	statusCodes map[string]int
	delays map[string]time.Duration
}

func newMockServer() *mockServer {
	ms := &mockServer{
		responses: make(map[string]string),
		statusCodes: make(map[string]int),
		delays: make(map[string]time.Duration),
	}
	
	ms.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		
		// Parse the request to determine response
		var req JSONRPCRequest
		json.Unmarshal(body, &req)
		
		// Add delay if configured
		if delay, exists := ms.delays[req.Method]; exists {
			time.Sleep(delay)
		}
		
		// Set status code
		statusCode := http.StatusOK
		if code, exists := ms.statusCodes[req.Method]; exists {
			statusCode = code
		}
		w.WriteHeader(statusCode)
		
		// Return response
		if response, exists := ms.responses[req.Method]; exists {
			w.Write([]byte(response))
		} else {
			// Default response for eth_blockNumber
			if req.Method == "eth_blockNumber" {
				w.Write([]byte(`{"jsonrpc":"2.0","result":"0x1234","id":0}`))
			} else {
				w.Write([]byte(`{"jsonrpc":"2.0","result":"test","id":0}`))
			}
		}
	}))
	
	return ms
}

func (ms *mockServer) URL() string {
	return ms.server.URL
}

func (ms *mockServer) Close() {
	ms.server.Close()
}

func (ms *mockServer) SetResponse(method, response string) {
	ms.responses[method] = response
}

func (ms *mockServer) SetStatusCode(method string, code int) {
	ms.statusCodes[method] = code
}

func (ms *mockServer) SetDelay(method string, delay time.Duration) {
	ms.delays[method] = delay
}

// Test Logger
func TestLogger(t *testing.T) {
	tests := []struct {
		name     string
		verbose  bool
		expected bool
	}{
		{"verbose enabled", true, true},
		{"verbose disabled", false, false},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := NewLogger(tt.verbose)
			if logger.verbose != tt.expected {
				t.Errorf("NewLogger(%v) = %v, want %v", tt.verbose, logger.verbose, tt.expected)
			}
		})
	}
}

// Test HTTPClient
func TestHTTPClient(t *testing.T) {
	timeout := 5 * time.Second
	client := NewHTTPClient(timeout)
	
	if client.client.Timeout != timeout {
		t.Errorf("HTTPClient timeout = %v, want %v", client.client.Timeout, timeout)
	}
	
	// Test that the client can make requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("test"))
	}))
	defer server.Close()
	
	resp, err := client.Post(server.URL, "text/plain", strings.NewReader("test"))
	if err != nil {
		t.Errorf("HTTPClient.Post() error = %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HTTPClient.Post() status = %v, want %v", resp.StatusCode, http.StatusOK)
	}
}

// Test JSON-RPC request/response marshaling
func TestJSONRPCStructs(t *testing.T) {
	// Test request marshaling
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "eth_blockNumber",
		Params:  json.RawMessage("[]"),
		ID:      1,
	}
	
	data, err := json.Marshal(req)
	if err != nil {
		t.Errorf("Failed to marshal JSONRPCRequest: %v", err)
	}
	
	var unmarshaled JSONRPCRequest
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Errorf("Failed to unmarshal JSONRPCRequest: %v", err)
	}
	
	if unmarshaled.Method != req.Method {
		t.Errorf("Unmarshaled method = %v, want %v", unmarshaled.Method, req.Method)
	}
	
	// Test response marshaling
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  json.RawMessage(`"0x1234"`),
		ID:      1,
	}
	
	data, err = json.Marshal(resp)
	if err != nil {
		t.Errorf("Failed to marshal JSONRPCResponse: %v", err)
	}
	
	var unmarshaledResp JSONRPCResponse
	err = json.Unmarshal(data, &unmarshaledResp)
	if err != nil {
		t.Errorf("Failed to unmarshal JSONRPCResponse: %v", err)
	}
	
	if string(unmarshaledResp.Result) != string(resp.Result) {
		t.Errorf("Unmarshaled result = %v, want %v", string(unmarshaledResp.Result), string(resp.Result))
	}
}

// Test getBlockHeight
func TestGetBlockHeight(t *testing.T) {
	server := newMockServer()
	defer server.Close()
	
	client := NewHTTPClient(5 * time.Second)
	
	tests := []struct {
		name           string
		response       string
		statusCode     int
		expectedHeight int64
		expectError    bool
	}{
		{
			name:           "valid response",
			response:       `{"jsonrpc":"2.0","result":"0x1234","id":0}`,
			statusCode:     http.StatusOK,
			expectedHeight: 0x1234,
			expectError:    false,
		},
		{
			name:           "invalid hex",
			response:       `{"jsonrpc":"2.0","result":"invalid","id":0}`,
			statusCode:     http.StatusOK,
			expectedHeight: 0,
			expectError:    true,
		},
		{
			name:           "server error",
			response:       "",
			statusCode:     http.StatusInternalServerError,
			expectedHeight: 0,
			expectError:    true,
		},
		{
			name:           "invalid json",
			response:       `{"invalid": "json"`,
			statusCode:     http.StatusOK,
			expectedHeight: 0,
			expectError:    true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server.SetResponse("eth_blockNumber", tt.response)
			server.SetStatusCode("eth_blockNumber", tt.statusCode)
			
			height, err := getBlockHeight(server.URL, client)
			
			if tt.expectError && err == nil {
				t.Errorf("getBlockHeight() expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("getBlockHeight() unexpected error: %v", err)
			}
			if !tt.expectError && height != tt.expectedHeight {
				t.Errorf("getBlockHeight() = %v, want %v", height, tt.expectedHeight)
			}
		})
	}
}

// Test getNodeHeights
func TestGetNodeHeights(t *testing.T) {
	server1 := newMockServer()
	defer server1.Close()
	server2 := newMockServer()
	defer server2.Close()
	server3 := newMockServer()
	defer server3.Close()
	
	// Configure servers
	server1.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1000","id":0}`)
	server2.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x2000","id":0}`)
	server3.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1500","id":0}`)
	
	nodes := []string{server1.URL, server2.URL, server3.URL}
	logger := NewLogger(false)
	client := NewHTTPClient(5 * time.Second)
	
	heights, healthy := getNodeHeights(nodes, logger, client)
	
	if len(heights) != 3 {
		t.Errorf("getNodeHeights() returned %d heights, want 3", len(heights))
	}
	if len(healthy) != 3 {
		t.Errorf("getNodeHeights() returned %d health statuses, want 3", len(healthy))
	}
	
	// Check that all nodes are healthy
	for i, h := range healthy {
		if !h {
			t.Errorf("Node %d should be healthy", i)
		}
	}
	
	// Check heights
	expectedHeights := []int64{0x1000, 0x2000, 0x1500}
	for i, height := range heights {
		if height != expectedHeights[i] {
			t.Errorf("Node %d height = %v, want %v", i, height, expectedHeights[i])
		}
	}
}

// Test getNodeHeights with unhealthy nodes
func TestGetNodeHeightsWithUnhealthyNodes(t *testing.T) {
	server1 := newMockServer()
	defer server1.Close()
	server2 := newMockServer()
	defer server2.Close()
	
	// Configure servers - one healthy, one unhealthy
	server1.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1000","id":0}`)
	server2.SetStatusCode("eth_blockNumber", http.StatusInternalServerError)
	
	nodes := []string{server1.URL, server2.URL}
	logger := NewLogger(false)
	client := NewHTTPClient(5 * time.Second)
	
	heights, healthy := getNodeHeights(nodes, logger, client)
	
	// First node should be healthy
	if !healthy[0] {
		t.Error("First node should be healthy")
	}
	
	// Second node should be unhealthy
	if healthy[1] {
		t.Error("Second node should be unhealthy")
	}
	
	// First node should have height
	if heights[0] != 0x1000 {
		t.Errorf("First node height = %v, want 0x1000", heights[0])
	}
	
	// Second node should have height 0 (default)
	if heights[1] != 0 {
		t.Errorf("Second node height = %v, want 0", heights[1])
	}
}

// Test forwardRequest
func TestForwardRequest(t *testing.T) {
	server := newMockServer()
	defer server.Close()
	
	server.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"test result","id":1}`)
	
	client := NewHTTPClient(5 * time.Second)
	requestBody := []byte(`{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`)
	
	response, err := forwardRequest(server.URL, requestBody, client)
	
	if err != nil {
		t.Errorf("forwardRequest() error = %v", err)
	}
	
	expected := `{"jsonrpc":"2.0","result":"test result","id":1}`
	if string(response) != expected {
		t.Errorf("forwardRequest() = %v, want %v", string(response), expected)
	}
}

// Test forwardRequest with error
func TestForwardRequestError(t *testing.T) {
	server := newMockServer()
	defer server.Close()
	
	server.SetStatusCode("eth_blockNumber", http.StatusInternalServerError)
	
	client := NewHTTPClient(5 * time.Second)
	requestBody := []byte(`{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`)
	
	_, err := forwardRequest(server.URL, requestBody, client)
	
	if err == nil {
		t.Error("forwardRequest() expected error but got none")
	}
}

// Test handleRequest with primary node healthy
func TestHandleRequestPrimaryHealthy(t *testing.T) {
	primaryServer := newMockServer()
	defer primaryServer.Close()
	backupServer := newMockServer()
	defer backupServer.Close()
	
	// Configure servers
	primaryServer.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1000","id":0}`)
	backupServer.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1001","id":0}`)
	primaryServer.SetResponse("eth_getBalance", `{"jsonrpc":"2.0","result":"0x1234","id":1}`)
	
	config := Config{
		PrimaryNode:   primaryServer.URL,
		BackupNodes:   []string{backupServer.URL},
		Timeout:       5 * time.Second,
		BlockLagLimit: 32,
	}
	
	logger := NewLogger(false)
	client := NewHTTPClient(config.Timeout)
	
	// Create test request
	requestBody := `{"jsonrpc":"2.0","method":"eth_getBalance","params":["0x123"],"id":1}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(requestBody))
	w := httptest.NewRecorder()
	
	handleRequest(w, req, config, logger, client)
	
	if w.Code != http.StatusOK {
		t.Errorf("handleRequest() status = %v, want %v", w.Code, http.StatusOK)
	}
	
	expected := `{"jsonrpc":"2.0","result":"0x1234","id":1}`
	if strings.TrimSpace(w.Body.String()) != expected {
		t.Errorf("handleRequest() response = %v, want %v", w.Body.String(), expected)
	}
}

// Test handleRequest with primary node unhealthy
func TestHandleRequestPrimaryUnhealthy(t *testing.T) {
	primaryServer := newMockServer()
	defer primaryServer.Close()
	backupServer := newMockServer()
	defer backupServer.Close()
	
	// Configure servers - primary unhealthy, backup healthy
	primaryServer.SetStatusCode("eth_blockNumber", http.StatusInternalServerError)
	backupServer.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1000","id":0}`)
	backupServer.SetResponse("eth_getBalance", `{"jsonrpc":"2.0","result":"0x5678","id":1}`)
	
	config := Config{
		PrimaryNode:   primaryServer.URL,
		BackupNodes:   []string{backupServer.URL},
		Timeout:       5 * time.Second,
		BlockLagLimit: 32,
	}
	
	logger := NewLogger(false)
	client := NewHTTPClient(config.Timeout)
	
	// Create test request
	requestBody := `{"jsonrpc":"2.0","method":"eth_getBalance","params":["0x123"],"id":1}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(requestBody))
	w := httptest.NewRecorder()
	
	handleRequest(w, req, config, logger, client)
	
	if w.Code != http.StatusOK {
		t.Errorf("handleRequest() status = %v, want %v", w.Code, http.StatusOK)
	}
	
	expected := `{"jsonrpc":"2.0","result":"0x5678","id":1}`
	if strings.TrimSpace(w.Body.String()) != expected {
		t.Errorf("handleRequest() response = %v, want %v", w.Body.String(), expected)
	}
}

// Test handleRequest with all nodes unhealthy
func TestHandleRequestAllUnhealthy(t *testing.T) {
	primaryServer := newMockServer()
	defer primaryServer.Close()
	backupServer := newMockServer()
	defer backupServer.Close()
	
	// Configure servers - both unhealthy
	primaryServer.SetStatusCode("eth_blockNumber", http.StatusInternalServerError)
	backupServer.SetStatusCode("eth_blockNumber", http.StatusInternalServerError)
	
	config := Config{
		PrimaryNode:   primaryServer.URL,
		BackupNodes:   []string{backupServer.URL},
		Timeout:       5 * time.Second,
		BlockLagLimit: 32,
	}
	
	logger := NewLogger(false)
	client := NewHTTPClient(config.Timeout)
	
	// Create test request
	requestBody := `{"jsonrpc":"2.0","method":"eth_getBalance","params":["0x123"],"id":1}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(requestBody))
	w := httptest.NewRecorder()
	
	handleRequest(w, req, config, logger, client)
	
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("handleRequest() status = %v, want %v", w.Code, http.StatusServiceUnavailable)
	}
}

// Test handleRequest with invalid JSON
func TestHandleRequestInvalidJSON(t *testing.T) {
	config := Config{
		PrimaryNode:   "http://localhost:8545",
		BackupNodes:   []string{},
		Timeout:       5 * time.Second,
		BlockLagLimit: 32,
	}
	
	logger := NewLogger(false)
	client := NewHTTPClient(config.Timeout)
	
	// Create test request with invalid JSON
	requestBody := `{"invalid": json}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(requestBody))
	w := httptest.NewRecorder()
	
	handleRequest(w, req, config, logger, client)
	
	if w.Code != http.StatusBadRequest {
		t.Errorf("handleRequest() status = %v, want %v", w.Code, http.StatusBadRequest)
	}
}

// Test handleRequest with primary node lagging
func TestHandleRequestPrimaryLagging(t *testing.T) {
	primaryServer := newMockServer()
	defer primaryServer.Close()
	backupServer := newMockServer()
	defer backupServer.Close()
	
	// Configure servers - primary lagging behind
	primaryServer.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1000","id":0}`)
	backupServer.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1050","id":0}`) // 80 blocks ahead
	backupServer.SetResponse("eth_getBalance", `{"jsonrpc":"2.0","result":"0x5678","id":1}`)
	
	config := Config{
		PrimaryNode:   primaryServer.URL,
		BackupNodes:   []string{backupServer.URL},
		Timeout:       5 * time.Second,
		BlockLagLimit: 32, // Primary is 80 blocks behind, should use backup
	}
	
	logger := NewLogger(false)
	client := NewHTTPClient(config.Timeout)
	
	// Create test request
	requestBody := `{"jsonrpc":"2.0","method":"eth_getBalance","params":["0x123"],"id":1}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(requestBody))
	w := httptest.NewRecorder()
	
	handleRequest(w, req, config, logger, client)
	
	if w.Code != http.StatusOK {
		t.Errorf("handleRequest() status = %v, want %v", w.Code, http.StatusOK)
	}
	
	expected := `{"jsonrpc":"2.0","result":"0x5678","id":1}`
	if strings.TrimSpace(w.Body.String()) != expected {
		t.Errorf("handleRequest() response = %v, want %v", w.Body.String(), expected)
	}
}

// Test handleRequest with primary node within lag limit
func TestHandleRequestPrimaryWithinLagLimit(t *testing.T) {
	primaryServer := newMockServer()
	defer primaryServer.Close()
	backupServer := newMockServer()
	defer backupServer.Close()
	
	// Configure servers - primary slightly behind but within limit
	primaryServer.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1000","id":0}`)
	backupServer.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1020","id":0}`) // 32 blocks ahead
	primaryServer.SetResponse("eth_getBalance", `{"jsonrpc":"2.0","result":"0x1234","id":1}`)
	
	config := Config{
		PrimaryNode:   primaryServer.URL,
		BackupNodes:   []string{backupServer.URL},
		Timeout:       5 * time.Second,
		BlockLagLimit: 32, // Primary is 32 blocks behind, should still use primary
	}
	
	logger := NewLogger(false)
	client := NewHTTPClient(config.Timeout)
	
	// Create test request
	requestBody := `{"jsonrpc":"2.0","method":"eth_getBalance","params":["0x123"],"id":1}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(requestBody))
	w := httptest.NewRecorder()
	
	handleRequest(w, req, config, logger, client)
	
	if w.Code != http.StatusOK {
		t.Errorf("handleRequest() status = %v, want %v", w.Code, http.StatusOK)
	}
	
	expected := `{"jsonrpc":"2.0","result":"0x1234","id":1}`
	if strings.TrimSpace(w.Body.String()) != expected {
		t.Errorf("handleRequest() response = %v, want %v", w.Body.String(), expected)
	}
}

// Test handleRequest with backup node failure
func TestHandleRequestBackupFailure(t *testing.T) {
	primaryServer := newMockServer()
	defer primaryServer.Close()
	backupServer := newMockServer()
	defer backupServer.Close()
	
	// Configure servers - primary healthy, backup fails on request
	primaryServer.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1000","id":0}`)
	backupServer.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1001","id":0}`)
	primaryServer.SetResponse("eth_getBalance", `{"jsonrpc":"2.0","result":"0x1234","id":1}`)
	backupServer.SetStatusCode("eth_getBalance", http.StatusInternalServerError)
	
	config := Config{
		PrimaryNode:   primaryServer.URL,
		BackupNodes:   []string{backupServer.URL},
		Timeout:       5 * time.Second,
		BlockLagLimit: 32,
	}
	
	logger := NewLogger(false)
	client := NewHTTPClient(config.Timeout)
	
	// Create test request
	requestBody := `{"jsonrpc":"2.0","method":"eth_getBalance","params":["0x123"],"id":1}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(requestBody))
	w := httptest.NewRecorder()
	
	handleRequest(w, req, config, logger, client)
	
	if w.Code != http.StatusBadGateway {
		t.Errorf("handleRequest() status = %v, want %v", w.Code, http.StatusBadGateway)
	}
}

// Benchmark tests
func BenchmarkGetNodeHeights(b *testing.B) {
	server1 := newMockServer()
	defer server1.Close()
	server2 := newMockServer()
	defer server2.Close()
	
	server1.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1000","id":0}`)
	server2.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x2000","id":0}`)
	
	nodes := []string{server1.URL, server2.URL}
	logger := NewLogger(false)
	client := NewHTTPClient(5 * time.Second)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		getNodeHeights(nodes, logger, client)
	}
}

func BenchmarkForwardRequest(b *testing.B) {
	server := newMockServer()
	defer server.Close()
	
	server.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1234","id":0}`)
	
	client := NewHTTPClient(5 * time.Second)
	requestBody := []byte(`{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		forwardRequest(server.URL, requestBody, client)
	}
}

// Helper function to create a test request
func createTestRequest(method, params string) *http.Request {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","method":"%s","params":%s,"id":1}`, method, params)
	return httptest.NewRequest("POST", "/", strings.NewReader(body))
}

// Test edge cases
func TestEdgeCases(t *testing.T) {
	// Test with empty nodes list
	t.Run("empty nodes list", func(t *testing.T) {
		logger := NewLogger(false)
		client := NewHTTPClient(5 * time.Second)
		
		heights, healthy := getNodeHeights([]string{}, logger, client)
		
		if len(heights) != 0 {
			t.Errorf("Expected empty heights slice, got %d", len(heights))
		}
		if len(healthy) != 0 {
			t.Errorf("Expected empty healthy slice, got %d", len(healthy))
		}
	})
	
	// Test with single node
	t.Run("single node", func(t *testing.T) {
		server := newMockServer()
		defer server.Close()
		
		server.SetResponse("eth_blockNumber", `{"jsonrpc":"2.0","result":"0x1000","id":0}`)
		
		logger := NewLogger(false)
		client := NewHTTPClient(5 * time.Second)
		
		heights, healthy := getNodeHeights([]string{server.URL}, logger, client)
		
		if len(heights) != 1 {
			t.Errorf("Expected 1 height, got %d", len(heights))
		}
		if len(healthy) != 1 {
			t.Errorf("Expected 1 health status, got %d", len(healthy))
		}
		if !healthy[0] {
			t.Error("Expected node to be healthy")
		}
		if heights[0] != 0x1000 {
			t.Errorf("Expected height 0x1000, got %d", heights[0])
		}
	})
} 