# Ethereum JSON-RPC Proxy

A simple high-availability proxy server for Ethereum JSON-RPC endpoints. It forwards JSON-RPC requests to a primary node if it's up-to-date and responsive, or falls back to the best backup node otherwise.

This was created for my own personal use, and is provided "as is".

## Features

- Routes JSON-RPC requests to a **primary Ethereum node** or **backup nodes**
- **Health checks** based on `eth_blockNumber`
- Detects **out-of-sync or unreachable nodes**
- Customizable **block lag tolerance**
- **Timeout control** for node health checks and forwarding
- Optional **verbose logging** for debugging and monitoring

## Usage

### Command Line Flags

The proxy server can be configured using the following command line flags:

| Flag | Description                                        | Default                                                             |
| ---- | -------------------------------------------------- | ------------------------------------------------------------------- |
| `-v` | Enable verbose logging                             | `false`                                                             |
| `-d` | Enable debug logging (logs requests and responses) | `false`                                                             |
| `-p` | Port to listen on                                  | `9545`                                                              |
| `-t` | Timeout in seconds for RPC requests                | `3`                                                                 |
| `-b` | Block lag limit tolerance                          | `32`                                                                |
| `-u` | Node URLs (comma-separated, first is primary)      | `http://localhost:8545,http://localhost:8546,http://localhost:8547` |

### Basic Usage

Run the proxy with default settings:

```bash
go run rpcproxy.go
```

### Advanced Configuration

Configure custom nodes and settings:

```bash
go run rpcproxy.go \
  -v \
  -p 8080 \
  -t 5 \
  -b 16 \
  -u "http://primary-node:8545,http://backup1:8545,http://backup2:8545"
```

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

3. If the **primary node** is healthy and within 32 blocks (configurable) of the highest block:

   - The request is forwarded to the primary.

4. If the primary is down or out-of-sync:

   - The request is forwarded to the **healthiest backup node** with the highest block height.

## Dependencies

- Go 1.18 or higher (standard library only)

## License

MIT License - Copyright (c) 2024 Pete Kim (https://github.com/petejkim)
