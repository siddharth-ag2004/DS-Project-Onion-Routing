package main

import (
	// "context"
	// "fmt"
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"

	// "crypto/rsa"
	"go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc/peer"

	// "google.golang.org/protobuf/proto"
	// ecies "github.com/ecies/go/v2"
	"crypto/rsa"
	"sync/atomic"

	encryption "onion_routing/encryption"
	routingpb "onion_routing/protofiles"
	utils "onion_routing/utils"

	"google.golang.org/grpc/credentials"
	// "google.golang.org/grpc/metadata"
)

// const (
// 	serverAddr = "localhost:23455"
// )

type RelayNode struct {
	Address string `json:"address"`
	PubKey *rsa.PublicKey `json:"pub_key"`
	Load int32 `json:"load"`
}

// cell := OnionCell{
// 	CellType:   1,                    // Create cell
// 	CircuitID:  1001,                 // Example Circuit ID
// 	Version:    1,                    // Version 1
// 	BackF:      1,                    // Backward cipher (e.g., DES)
// 	ForwF:      2,                    // Forward cipher (e.g., RC4)
// 	Port:       9002,                 // Port number
// 	IP:         [4]byte{192, 168, 1, 1}, // Destination IP
// 	Expiration: 1700000000,           // Expiration time
// 	KeySeed:    [16]byte{'1', '6', 'B', 'y', 't', 'e', 's', 'K', 'e', 'y', 'S', 'e', 'e', 'd', '!'},
// 	Payload:    []byte("Hello, Onion!"), // Payload
// }

type CircuitInfo struct {
	RequestType byte
	BackEncryption byte
	ForwardIP [4]byte
	ForwardPort uint16
	BackwardIP [4]byte
	BackwardPort uint16
	Expiration uint32
	ExpTime time.Time
	IsExitNode bool
	KeySeed [16]byte
	key1 [8]byte 
	key2 [16]byte
	key3 [16]byte
}


var (
	relayLogger *utils.Logger
	relayCredsAsServer credentials.TransportCredentials
	relayCredsAsClient credentials.TransportCredentials
	relayAddr string
	nodeID string 
	// pubKey *rsa.PublicKey
	// privateKey *rsa.PrivateKey
	privateKey *rsa.PrivateKey
	pubKey *rsa.PublicKey
	load int32
	circuitInfoMap = make(map[uint16]*CircuitInfo)	// map of circuit id to circuit info
	circuitInfoMapLock sync.Mutex
)

type RelayNodeServer struct {
	routingpb.UnimplementedRelayNodeServerServer
}

func handleCreateCell(cell encryption.OnionCell, ctx context.Context)(CircuitInfo){
	p, ok := peer.FromContext(ctx)
	if !ok {
		log.Println("Could not extract peer from context")
	} else {
		relayLogger.PrintLog("Request received from: %v", p.Addr.String())
	}

	_, backPort := utils.GetPortAndIP(p.Addr.String())
	//TODO: Temporarily making backIP same as forward IP as localhost
	backIPBytes := cell.IP
	backPortUint, _ := strconv.Atoi(string(backPort[:]))
	backPortUint16 := uint16(backPortUint)
	key1, key2, key3 := encryption.DeriveKeys(cell.KeySeed[:])

	cinfo := CircuitInfo{
		RequestType: cell.RequestType,
		BackEncryption: cell.BackEncryption,
		Expiration: cell.Expiration,
		ExpTime: time.Now().Add(time.Duration(cell.Expiration) * time.Second),
		KeySeed: cell.KeySeed,
		ForwardIP: cell.IP,
		ForwardPort: cell.Port,
		BackwardIP: backIPBytes,
		BackwardPort: backPortUint16,
		IsExitNode: (cell.IsExitNode != 0),  // converting byte to bool
		key1: [8]byte(key1),
		key2: [16]byte(key2),
		key3: [16]byte(key3),
	}

	return cinfo
}

// func decryptMessageHeader(messageHeader []byte, privateKey *rsa.PrivateKey){
// 	decryptedHeader, err := encryption.DecryptRSA(messageHeader, privateKey)
// 	return 
// }

func handleRequest(ctx context.Context, req *routingpb.RelayRequest) (CircuitInfo, []byte, error){
	encryptedMessageHeader := req.Message[:256]
	encryptedMessagePayload := req.Message[256:]
	decryptedMessageHeader, err := encryption.DecryptRSA(encryptedMessageHeader, privateKey)
	if err != nil {
		log.Fatalf("Failed to decrypt message: %v", err)
	}

	// log.Println("Decrypted message: ")
	rebuiltCell := encryption.RebuildMessage(decryptedMessageHeader)
	// log.Println(rebuiltCell.String()) 
	// log.Println("Size of decrypted message: ", len(decryptedMessageHeader))
	switch rebuiltCell.CellType {
	case byte(encryption.CREATE_CELL): // create cell

		log.Println("Create cell")
		circuitInfoMapLock.Lock()
		defer circuitInfoMapLock.Unlock()
		circuitInfo := handleCreateCell(rebuiltCell, ctx)
		atomic.AddInt32(&load, 1)
		log.Println("Creating", rebuiltCell.CircuitID)
		circuitInfoMap[rebuiltCell.CircuitID] = &circuitInfo
		log.Println("Create Cell Done-Debug Message")
		return circuitInfo, encryptedMessagePayload, nil

	case byte(encryption.DATA_CELL):
		log.Println("Data cell")
		circuitInfoMapLock.Lock()
		defer circuitInfoMapLock.Unlock()
		cinfo, exists := circuitInfoMap[rebuiltCell.CircuitID]
		if !exists {
			return CircuitInfo{}, make([]byte, 0), utils.ErrCircuitNotFound
		}
		cinfo.ExpTime = time.Now().Add(time.Duration(cinfo.Expiration) * time.Second)
		cinfo.RequestType = rebuiltCell.RequestType
		decryptedMessagePayload := encryption.DecryptRC4(encryptedMessagePayload, cinfo.key1[:])
		return *cinfo, decryptedMessagePayload, nil

	case byte(encryption.PADDING_CELL):
		log.Println("Padding cell")
	}
	return CircuitInfo{}, make([]byte, 0), nil
}

func handleResponse(circuitInfo CircuitInfo, respMessage []byte) ([]byte){
	encryptedRespMessage := encryption.EncryptRC4(respMessage, circuitInfo.key2[:])
	return encryptedRespMessage
}

func sendRequestToServer(serverAddr string, req *routingpb.RelayRequest, reqType int)(*routingpb.RelayResponse, error){
	log.Printf("Received Request Type : %d\n", reqType)
	conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(relayCredsAsClient))
	if err != nil {
		log.Println("Received Error:", err)
		return &routingpb.RelayResponse{}, err
	}
	defer conn.Close()
	client := routingpb.NewOnionRoutingServerClient(conn)
	var resp *routingpb.RelayResponse
	switch reqType {
	case 1:
		reqToServer := &routingpb.GreetRequest{Message: req.Message}
		relayLogger.PrintLog("Request sending to server: %v", req)
		respFromServer, err := client.GreetServer(context.Background(), reqToServer)
		if err != nil {
			// log.Println("Received Error:", err, "from IP", req)
			return &routingpb.RelayResponse{}, err
		}
		relayLogger.PrintLog("Response received from server: %v", respFromServer)
		resp = &routingpb.RelayResponse{Reply: respFromServer.Reply}
		
	case 2:
		reqToServer := &routingpb.FibonacciRequest{N : req.Message}
		relayLogger.PrintLog("Request sending to server: %v", req)
		respFromServer, err := client.CalculateFibonacci(context.Background(), reqToServer)
		if err != nil {
			// log.Println("Received Error:", err, "from IP", req)
			return &routingpb.RelayResponse{}, err
		}
		relayLogger.PrintLog("Response received from server: %v", respFromServer)
		resp = &routingpb.RelayResponse{Reply: respFromServer.Reply}
		
	case 3:
		reqToServer := &routingpb.GetRandomRequest{N : req.Message}
		relayLogger.PrintLog("Request sending to server: %v", req)
		respFromServer, err := client.GetRandomNumbers(context.Background(), reqToServer)
		if err != nil {
			// log.Println("Received Error:", err, "from IP", req)
			return &routingpb.RelayResponse{}, err
		}
		relayLogger.PrintLog("Response received from server: %v", respFromServer)
		resp = &routingpb.RelayResponse{Reply: respFromServer.Reply}
	}
	return resp, nil
}

func sendRequestToRelayNode(nodeAddr string, req *routingpb.RelayRequest)(*routingpb.RelayResponse, error){
	conn, err := grpc.NewClient(nodeAddr, grpc.WithTransportCredentials(relayCredsAsClient))
	if err != nil {
		log.Println("Received Error:", err)
		return &routingpb.RelayResponse{}, err
	}
	defer conn.Close()
	client := routingpb.NewRelayNodeServerClient(conn)

	relayLogger.PrintLog("Request sending to next Node: %v", req)
	resp, err := client.RelayNodeRPC(context.Background(), req)
	if err != nil {
		log.Println("Received Error:", err)
		return &routingpb.RelayResponse{}, err
	}
	relayLogger.PrintLog("Response received from next Node: %v", resp)
	return resp, nil
}

func (s *RelayNodeServer) RelayNodeRPC(ctx context.Context, req *routingpb.RelayRequest) (*routingpb.RelayResponse, error) {
	relayLogger.PrintLog("Request recieved from previous Node: %v", req)

	circuitInfo, forwardMessage, err := handleRequest(ctx, req)
	if err != nil {
		return &routingpb.RelayResponse{}, err
	}
	nextNodeAddr := fmt.Sprintf("localhost:%d",circuitInfo.ForwardPort)
	
	if len(forwardMessage) == 0 {  // handling padding cell + exitNode node in route-creation
		return &routingpb.RelayResponse{Reply: []byte("Exit Node Reached or Padding Cell")}, nil
	}
	log.Println("Sending to Node with Addr: ", nextNodeAddr)
 
	forwardReq := &routingpb.RelayRequest{Message: forwardMessage}
	if circuitInfo.IsExitNode {
		resp, err := sendRequestToServer(nextNodeAddr, forwardReq, int(circuitInfo.RequestType))
		if err != nil {
			return &routingpb.RelayResponse{}, err
		}
		respMessage := handleResponse(circuitInfo, []byte(resp.Reply))
		backwardResp := &routingpb.RelayResponse{Reply: respMessage}
		return backwardResp, nil
	}
	resp, err := sendRequestToRelayNode(nextNodeAddr, forwardReq)
	if err != nil {
		return &routingpb.RelayResponse{}, err
	}
	respMessage := handleResponse(circuitInfo, []byte(resp.Reply))
	backwardResp := &routingpb.RelayResponse{Reply: respMessage}
	return backwardResp, nil
}

func encryptPaddingMessage(message []byte, pubkey *rsa.PublicKey)([]byte, error){
	messageHeader := message[:32]
	messagePayload := message[32:]
	
	encryptedHeader, err := encryption.EncryptRSA(messageHeader, pubkey)
	if err != nil {
		return make([]byte, 0), err
	}
	encryptedMessage := append(encryptedHeader, messagePayload...)
	
	return encryptedMessage, nil
}

func paddingLoopRandom(etcdClient *clientv3.Client, selfAddr string) {
	count := 1
	for {
		// time.Sleep(10 * time.Second)
		time.Sleep(time.Duration(rand.Intn(10000)+15000) * time.Millisecond)
		// time.Sleep(time.Duration(10000) * time.Millisecond)
		nodes, err := GetAvailableRelayNodes(etcdClient)
		if err != nil || len(nodes) == 0 {
			log.Println("No available nodes for padding.")
			continue
		}

		others := make([]RelayNode, 0)
		for _, n := range nodes {
			if n.Address != selfAddr {
				others = append(others, n)
			}
		}
		if len(others) == 0 {
			log.Println("Only this relay is registered; skipping padding.")
			time.Sleep(2 * time.Second)
			continue
		}

		target := others[rand.Intn(len(others))]

		cell := encryption.PaddingCell(count)
		message := encryption.BuildMessage(cell)
		encryptedMessage, _ := encryptPaddingMessage(message, target.PubKey)

		conn, err := grpc.NewClient(target.Address, grpc.WithTransportCredentials(relayCredsAsClient))
		if err != nil {
			log.Printf("Padding failed: could not connect to %s: %v", target.Address, err)
			time.Sleep(1 * time.Second)
			continue
		}
		client := routingpb.NewRelayNodeServerClient(conn)

		fmt.Println("Size of encrypted message: ", len(encryptedMessage))

		resp, err := client.RelayNodeRPC(
			context.Background(),
			&routingpb.RelayRequest{Message: encryptedMessage},
		)
		
		if err != nil {
			log.Printf("Padding failed to %s: %v", target.Address, err)
		} else {
			fmt.Printf(
				"Padding sent to %s: %s\n",
				target.Address,
				string(resp.Reply),
			)
		}
		
		conn.Close()


		// count++
	}
}

func main(){
	args := os.Args[1:]
	if len(args) >= 1 {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			log.Fatalf("Invalid command line argument; expecting integer value")
		}
		nodeID = fmt.Sprintf("node%d",id)
	}

	privateKey, pubKey = genKeyPairs()

	relayCredsAsClient = credentials.NewTLS(utils.LoadClientTLSConfigWithKeyLog(
		"certificates/ca.crt", 
		"certificates/relay_node.crt", 
		"certificates/relay_node.key",
	))
	
	relayCredsAsServer = credentials.NewTLS(utils.LoadServerTLSConfigWithKeyLog(
		"certificates/ca.crt", 
		"certificates/relay_node.crt", 
		"certificates/relay_node.key",
	))
	
    var err error
	relayLogger = utils.NewLogger("logs/relay")
	relayAddr, err = utils.GetAvaliableAddress()
	if err != nil {
		log.Fatalf("Failed to get server address: %v", err)
	}

	// server initialization 
	listener, err := net.Listen("tcp", relayAddr)
	if err != nil {
		log.Fatalf("relay server failed to listen: %v", err)
	}
	defer listener.Close()

	server := grpc.NewServer(grpc.Creds(relayCredsAsServer))
	routingpb.RegisterRelayNodeServerServer(server, &RelayNodeServer{})
	log.Printf("Relay Node Server running on %s\n", relayAddr)
	

	// etcd registration
	etcdClient, err := initEtcdClient()
	if err != nil {
		log.Fatalf("Failed to initialize etcd client: %v", err)
	}
	defer etcdClient.Close()
	err = checkEtcdStatus(etcdClient)
	if err != nil {
		log.Fatalf("Etcd Server is unreachable: %v", err)
	}
	leaseId, err := createLease(etcdClient)
	if err != nil {
		log.Fatalf("Failed to create Etcd lease: %v", err)
	}
	err = registerWithEtcdServer(etcdClient, leaseId)
	if err != nil {
		log.Fatalf("Failed to register with Etcd: %v", err)
	}
	go keepAliveThread(etcdClient, leaseId)
	go periodicUpdateThread(etcdClient, leaseId)

	go paddingLoopRandom(etcdClient, relayAddr)
	go checkExpirations()
	
	err = server.Serve(listener)
	if err != nil {
		log.Fatalf("Relay Node server failed to server: %v", err)
	}
}