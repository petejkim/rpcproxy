package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"log/slog"
)

type Config struct {
	PrimaryNode        string
	BackupNodes        []string
	HealthCheckTimeout time.Duration
	RequestTimeout     time.Duration
	BlockLagLimit      int64
}

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      uint64          `json:"id"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
	ID      uint64          `json:"id"`
}

var (
	config            Config
	logger            *slog.Logger
	healthCheckClient *HTTPClient
	requestClient     *HTTPClient
)

type HTTPClient struct {
	client *http.Client
}

func NewHTTPClient(timeout time.Duration) *HTTPClient {
	return &HTTPClient{
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func (hc *HTTPClient) Post(url string, contentType string, body io.Reader) (*http.Response, error) {
	return hc.client.Post(url, contentType, body)
}

func main() {
	var (
		port          int
		timeout       int
		blockLagLimit int
		nodes         string
		logLevel      string
	)

	flag.StringVar(&logLevel, "l", "info", "Log level (debug, info, warn, error)")
	flag.IntVar(&port, "p", 9545, "Port to listen on")
	flag.IntVar(&timeout, "t", 500, "Timeout in ms for health-check requests")
	flag.IntVar(&blockLagLimit, "b", 16, "Block lag limit")
	flag.StringVar(&nodes, "u", "http://localhost:8545,http://localhost:8546,http://localhost:8547", "Node URLs (comma-separated, first is primary)")

	flag.Parse()

	nodeList := strings.Split(nodes, ",")
	if len(nodeList) == 0 {
		log.Fatal("At least one node URL must be provided")
	}

	config = Config{
		PrimaryNode:        nodeList[0],
		BackupNodes:        nodeList[1:],
		HealthCheckTimeout: time.Duration(timeout) * time.Millisecond,
		RequestTimeout:     30 * time.Second,
		BlockLagLimit:      int64(blockLagLimit),
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(logLevel)); err != nil {
		log.Fatalf("Invalid log level: %s. Must be one of: debug, info, warn, error", logLevel)
	}

	logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	healthCheckClient = NewHTTPClient(config.HealthCheckTimeout)
	requestClient = NewHTTPClient(config.RequestTimeout)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handleRequest(w, r)
	})

	logArgs := []any{slog.Int("port", port), slog.String("node0", config.PrimaryNode)}
	for i, u := range config.BackupNodes {
		logArgs = append(logArgs, slog.String("node"+strconv.Itoa(i+1), u))
	}
	logger.Info("rpcproxy started", logArgs...)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request", http.StatusBadRequest)
		logger.Error("Failed to read request body", slog.Any("error", err))
		logger.Debug("Outgoing response", slog.Int("status", http.StatusBadRequest))
		return
	}
	defer r.Body.Close()

	logger.Debug("Incoming request",
		slog.String("method", r.Method),
		slog.String("url", r.URL.String()),
		slog.Any("headers", r.Header),
		slog.String("body", string(body)))

	if len(body) > 0 && body[0] == '[' {
		if err := json.Unmarshal(body, &[]JSONRPCRequest{}); err != nil {
			http.Error(w, "Invalid JSON-RPC batch request", http.StatusBadRequest)
			logger.Error("Invalid JSON-RPC batch request", slog.Any("error", err))
			logger.Debug("Outgoing response", slog.Int("status", http.StatusBadRequest))
			return
		}
	} else {
		if err := json.Unmarshal(body, &JSONRPCRequest{}); err != nil {
			http.Error(w, "Invalid JSON-RPC request", http.StatusBadRequest)
			logger.Error("Invalid JSON-RPC request", slog.Any("error", err))
			logger.Debug("Outgoing response", slog.Int("status", http.StatusBadRequest))
			return
		}
	}

	nodes := append([]string{config.PrimaryNode}, config.BackupNodes...)
	heights, healthy := getNodeHeights(nodes)

	maxHeight := int64(0)
	for _, h := range heights {
		if h > maxHeight {
			maxHeight = h
		}
	}

	logArgs := []any{slog.Int64("max_height", maxHeight)}
	for i, h := range heights {
		nodeLabel := "node" + strconv.Itoa(i)
		if healthy[i] {
			logArgs = append(logArgs, slog.Int64(nodeLabel, h))
		} else {
			logArgs = append(logArgs, slog.String(nodeLabel, "unreachable"))
		}
	}
	logger.Info("Node block heights", logArgs...)

	if healthy[0] && maxHeight-heights[0] <= config.BlockLagLimit {
		logger.Info("Forwarding request to primary node")
		resp, err := forwardRequest(config.PrimaryNode, body)
		if err == nil {
			logger.Debug("Outgoing response", slog.Int("status", http.StatusOK), slog.String("body", string(resp)))
			w.Write(resp)
			return
		}
		logger.Error("Primary node failed", slog.Any("error", err))
	}

	bestIndex := -1
	bestHeight := int64(-1)
	for i := 1; i < len(nodes); i++ {
		if healthy[i] && heights[i] > bestHeight {
			bestHeight = heights[i]
			bestIndex = i
		}
	}

	if bestIndex == -1 {
		http.Error(w, "All nodes unavailable", http.StatusServiceUnavailable)
		logger.Error("All nodes unavailable - no healthy backup nodes found")
		logger.Debug("Outgoing response", slog.Int("status", http.StatusServiceUnavailable))
		return
	}

	logger.Info("Forwarding request to backup node", slog.Int("index", bestIndex))

	resp, err := forwardRequest(nodes[bestIndex], body)
	if err != nil {
		http.Error(w, "Failed to forward request", http.StatusBadGateway)
		logger.Error("Failed to forward request to backup node", slog.Int("index", bestIndex), slog.Any("error", err))
		logger.Debug("Outgoing response", slog.Int("status", http.StatusBadGateway))
		return
	}

	w.Write(resp)
	logger.Debug("Outgoing response", slog.Int("status", http.StatusOK), slog.String("body", string(resp)))
}

func getNodeHeights(nodes []string) ([]int64, []bool) {
	type result struct {
		index  int
		height int64
		ok     bool
	}

	var wg sync.WaitGroup
	results := make(chan result, len(nodes))

	for i, node := range nodes {
		wg.Add(1)
		go func(index int, url string) {
			defer wg.Done()
			height, err := getBlockHeight(url, healthCheckClient)
			if err != nil {
				logger.Error("Error querying node", slog.Int("index", index), slog.String("url", url), slog.Any("error", err))
			}
			results <- result{index, height, err == nil}
		}(i, node)
	}

	wg.Wait()
	close(results)

	heights := make([]int64, len(nodes))
	healthy := make([]bool, len(nodes))

	for res := range results {
		heights[res.index] = res.height
		healthy[res.index] = res.ok
	}
	return heights, healthy
}

func getBlockHeight(url string, httpClient *HTTPClient) (int64, error) {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "eth_blockNumber",
		Params:  json.RawMessage("[]"),
		ID:      0,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return 0, err
	}

	var hexNum string
	if err := json.Unmarshal(rpcResp.Result, &hexNum); err != nil {
		return 0, err
	}
	return strconv.ParseInt(hexNum[2:], 16, 64)
}

func forwardRequest(url string, body []byte) ([]byte, error) {
	resp, err := requestClient.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
