# Ethereum JSON-RPC Proxy

A simple high-availability proxy server for Ethereum JSON-RPC endpoints. It forwards JSON-RPC requests to a primary node if it's up-to-date and responsive, or falls back to the best backup node otherwise.

This was created for my own personal use, and is provided "as is".

## Features

- Routes JSON-RPC requests to a **primary Ethereum node** or **backup nodes**
- **Health checks** based on `eth_blockNumber` with configurable timeout
- Detects **out-of-sync or unreachable nodes**
- Customizable **block lag tolerance**
- **Separate timeouts** for health checks and request forwarding
- **Structured logging** with configurable log levels
- **Error logging** to stderr, info logging to stdout

## Usage

### Command Line Flags

The proxy server can be configured using the following command line flags:

| Flag | Description                                   | Default                                                             |
| ---- | --------------------------------------------- | ------------------------------------------------------------------- |
| `-l` | Log level (debug, info, warn, error)          | `info`                                                              |
| `-p` | Port to listen on                             | `9545`                                                              |
| `-t` | Health check timeout in milliseconds          | `500`                                                               |
| `-b` | Block lag limit tolerance                     | `16`                                                                |
| `-u` | Node URLs (comma-separated, first is primary) | `http://localhost:8545,http://localhost:8546,http://localhost:8547` |

### Basic Usage

Run the proxy with default settings:

```bash
go run rpcproxy.go
```

### Advanced Configuration

Configure custom nodes and settings:

```bash
go run rpcproxy.go \
  -l debug \
  -p 8080 \
  -t 1000 \
  -b 16 \
  -u "http://primary-node:8545,http://backup1:8545,http://backup2:8545"
```

### Log Levels

- **debug**: Shows all log messages (DEBUG, INFO, WARN, ERROR)
- **info**: Shows INFO, WARN, and ERROR messages (default)
- **warn**: Shows only WARN and ERROR messages
- **error**: Shows only ERROR messages

### Make JSON-RPC Requests

Send any Ethereum JSON-RPC request to the proxy server:

```bash
curl -X POST http://localhost:9545 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'
```

## How It Works

1. The proxy receives a JSON-RPC request.
2. It queries all configured nodes with `eth_blockNumber` to determine:

   - Which nodes are healthy (HTTP 200 OK and responsive).
   - The highest block height across all nodes.

3. If the **primary node** is healthy and within the configured block lag limit of the highest block:

   - The request is forwarded to the primary with a 30-second timeout.

4. If the primary is down or out-of-sync:

   - The request is forwarded to the **healthiest backup node** with the highest block height.

## Dependencies

- Go 1.21 or higher (standard library only)

## License

MIT License - Copyright (c) 2024 Pete Kim (https://github.com/petejkim)
