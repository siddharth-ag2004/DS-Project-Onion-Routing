# Onion Routing Network

A Tor-inspired onion routing network built in Go, designed to provide secure, anonymous communication between clients and servers. This project implements multi-layered encryption, randomized path selection, and traffic analysis resistance to ensure high privacy for data packets traveling across the network.

## Features

- **Multi-layered Encryption:** Uses AES-256 for symmetric encryption and RSA-4096 for secure key exchanges, ensuring robust data confidentiality.
- **Randomized Path Selection:** Routes packets through random relay nodes, obscuring the source and destination of the traffic.
- **Traffic Analysis Resistance:** Implements random packet padding to thwart traffic analysis and timing attacks.
- **Adaptive Load Balancing:** Dynamically distributes traffic across relays to minimize congestion, resulting in a ~25% reduction in response time.
- **Session Management:** Securely manages active communication sessions and handles node failures gracefully.
- **gRPC Communication:** Utilizes Protocol Buffers (Protobuf) and gRPC for fast and reliable inter-node communication.
- **Service Discovery:** Uses `etcd` for managing active relay nodes and directory services.

## Architecture

The network consists of several key components:

1. **Client (`/client`):** Initiates the communication, requests a path from the directory server, encrypts the payload in multiple layers (the "onion"), and sends it to the entry node.
2. **Directory Server (`/directory`):** Maintains a list of active relay nodes and provides random paths to clients.
3. **Relay Nodes (`/relay`):** The intermediaries in the network. Each node peels off one layer of encryption using its private key and forwards the packet to the next node.
4. **Server (`/server`):** The final destination that receives the decrypted message and processes the request.

## Prerequisites

- **Go 1.18+**
- **Protocol Buffers Compiler (`protoc`)**
- **etcd:** Used for distributed key-value store and directory service.

## Getting Started

### 1. Start `etcd`
Ensure `etcd` is installed and running locally on port `2379`:
```bash
make etcd
```

### 2. Generate Protobuf Files
Compile the gRPC services:
```bash
make proto
```

### 3. Start the Directory Server
```bash
make directory
```

### 4. Start Relay Nodes
Open multiple terminal tabs to start several relay nodes with unique IDs:
```bash
make relay RELAY_NODE_ID=1
make relay RELAY_NODE_ID=2
make relay RELAY_NODE_ID=3
```

### 5. Start the Destination Server
```bash
make server
```

### 6. Run the Client
Start a client to send traffic through the onion network:
```bash
make client CLIENT_ID=1001
```

## Cleaning Up
To clear logs generated during execution:
```bash
make clean_logs
```
