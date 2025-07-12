package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	PrimaryNode   string
	BackupNodes   []string
	Timeout       time.Duration
	BlockLagLimit int64
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

var verbose bool

type Logger struct {
	verbose bool
}

func NewLogger(verbose bool) *Logger {
	return &Logger{verbose: verbose}
}

func (l *Logger) Log(format string, args ...interface{}) {
	if l.verbose {
		log.Printf(format, args...)
	}
}

func (l *Logger) Logln(args ...interface{}) {
	if l.verbose {
		log.Println(args...)
	}
}

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
	var port int
	var timeout int
	var blockLagLimit int
	var nodes string

	flag.BoolVar(&verbose, "v", false, "Enable verbose logging")
	flag.IntVar(&port, "p", 9545, "Port to listen on")
	flag.IntVar(&timeout, "t", 3, "Timeout in seconds")
	flag.IntVar(&blockLagLimit, "b", 32, "Block lag limit")
	flag.StringVar(&nodes, "u", "http://localhost:8545,http://localhost:8546,http://localhost:8547", "Node URLs (comma-separated, first is primary)")
	flag.Parse()

	nodeList := strings.Split(nodes, ",")
	if len(nodeList) == 0 {
		log.Fatal("At least one node URL must be provided")
	}

	config := Config{
		PrimaryNode:   nodeList[0],
		BackupNodes:   nodeList[1:],
		Timeout:       time.Duration(timeout) * time.Second,
		BlockLagLimit: int64(blockLagLimit),
	}

	logger := NewLogger(verbose)

	httpClient := NewHTTPClient(config.Timeout)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handleRequest(w, r, config, logger, httpClient)
	})

	log.Println("Proxy server listening on port", port)
	logger.Logln("Primary node:", config.PrimaryNode)
	logger.Logln("Backup nodes:", strings.Join(config.BackupNodes, ", "))

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

func handleRequest(w http.ResponseWriter, r *http.Request, config Config, logger *Logger, httpClient *HTTPClient) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON-RPC request", http.StatusBadRequest)
		return
	}

	nodes := append([]string{config.PrimaryNode}, config.BackupNodes...)
	heights, healthy := getNodeHeights(nodes, logger, httpClient)

	maxHeight := int64(0)
	for _, h := range heights {
		if h > maxHeight {
			maxHeight = h
		}
	}

	logger.Logln("Node block heights:")
	for i, h := range heights {
		status := "unreachable"
		if healthy[i] {
			status = "healthy"
		}
		nodeLabel := "Primary"
		if i > 0 {
			nodeLabel = "Backup " + strconv.Itoa(i)
		}
		logger.Log("  %s: %d (%s)", nodeLabel, h, status)
	}

	if healthy[0] && maxHeight-heights[0] <= config.BlockLagLimit {
		logger.Logln("Forwarding request to primary node")
		resp, err := forwardRequest(config.PrimaryNode, body, httpClient)
		if err == nil {
			w.Write(resp)
			return
		}
		logger.Logln("Primary node failed. Falling back.")
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
		return
	}

	logger.Log("Forwarding request to backup node #%d", bestIndex)

	resp, err := forwardRequest(nodes[bestIndex], body, httpClient)
	if err != nil {
		http.Error(w, "Failed to forward request", http.StatusBadGateway)
		return
	}
	w.Write(resp)
}

func getNodeHeights(nodes []string, logger *Logger, httpClient *HTTPClient) ([]int64, []bool) {
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
			height, err := getBlockHeight(url, httpClient)
			if err != nil {
				logger.Log("Error querying node #%d: %v", index, err)
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

func forwardRequest(url string, body []byte, httpClient *HTTPClient) ([]byte, error) {
	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
