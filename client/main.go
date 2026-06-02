package main

import (
	"context"
	"flag"
	"fmt"

	"log"
	"time"

	encryption "onion_routing/encryption"
	routingpb "onion_routing/protofiles"
	utils "onion_routing/utils"

	"crypto/rand"
	"crypto/rsa"

	"google.golang.org/grpc"
	// "google.golang.org/grpc/mem"
	// "google.golang.org/protobuf/internal/encoding/messageset"
)

const MAX_ATTEMPTS = 5

var (
	clientLogger *utils.Logger
	nodes        []RelayNode
	keySeeds = [3][16]byte{}
	// connected = 0
)



// func getPortAndIP(address string) (uint16, [4]byte) {
// 	parts := strings.Split(address, ":")
// 	port, _ := strconv.Atoi(parts[1])
// 	ipBytes := [4]byte{192, 168, 1, 1}
// 	return uint16(port), ipBytes
// }

func encryptCreateMessage(message []byte, pubkey *rsa.PublicKey)([]byte, error){
	messageHeader := message[:32]
	messagePayload := message[32:]
	
	encryptedHeader, err := encryption.EncryptRSA(messageHeader, pubkey)
	if err != nil {
		return make([]byte, 0), err
	}
	encryptedMessage := append(encryptedHeader, messagePayload...)
	log.Printf("Length of encrypted Message - Header:%d, PayLoad:%d", len(encryptedHeader), len(messagePayload))
	
	return encryptedMessage, nil
}

func encryptDataMessage(message []byte, pubkey *rsa.PublicKey, keySeed [16]byte)([]byte, error) {
	key1, _, _ := encryption.DeriveKeys(keySeed[:])
	messageHeader := message[:32]
	messagePayload := message[32:]
	encryptedHeader, err := encryption.EncryptRSA(messageHeader, pubkey)
	if err != nil {
		return make([]byte, 0), err
	}
	encryptedPayload := encryption.EncryptRC4(messagePayload, key1)
	encryptedMessage := append(encryptedHeader, encryptedPayload...)
	return encryptedMessage, nil
}

func buildLayer(cellType int, serverAddr string, circuitID uint16, keySeed [16]byte, pubkey *rsa.PublicKey, payload []byte, isExitNode byte, reqType byte)([]byte, error){
	server_port, server_ip := utils.GetPortAndIP(serverAddr)  // added server address
	var err error
	var encrypted []byte
	
	switch cellType {
	case 1:
		cell := encryption.CreateCell(server_ip, server_port, payload, circuitID, keySeed, isExitNode)
		message := encryption.BuildMessage(cell)
		encrypted, err = encryptCreateMessage(message, pubkey)
	case 2:
		cell := encryption.DataCell(payload, circuitID, isExitNode, reqType)
		message := encryption.BuildMessage(cell)
		encrypted, err = encryptDataMessage(message, pubkey, keySeed)
	}

	if err != nil {
		return make([]byte, 0), err
	}
	return encrypted, err
}

func DecryptResponse(respMessage []byte) (string){
	// to do : change here to use different key2 for different layers based on different keyseed
	_, key2, _ := encryption.DeriveKeys(keySeeds[0][:])
	_, key5, _ := encryption.DeriveKeys(keySeeds[1][:])
	_, key8, _ := encryption.DeriveKeys(keySeeds[2][:])
	decryptedMessage := encryption.DecryptRC4(respMessage, key2)
	decryptedMessage = encryption.DecryptRC4(decryptedMessage, key5)
	decryptedMessage = encryption.DecryptRC4(decryptedMessage, key8)
	return string(decryptedMessage)
}

func startCreationRoute(client routingpb.RelayNodeServerClient, chosen_nodes []RelayNode, circuitID uint16)(error) {
	// Innermost Layer (node 3)
	keySeeds = [3][16]byte{}
	for i := 0; i < 3; i++ {
		rand.Read(keySeeds[i][:])
	}

	encryptedMessage, err := buildLayer(1, utils.ServerAddr, circuitID, keySeeds[2], chosen_nodes[2].PubKey, []byte(""), byte(1), 1)
	if err != nil {
		return err
	}

	encryptedMessage, err = buildLayer(1, chosen_nodes[2].Address, circuitID, keySeeds[1], chosen_nodes[1].PubKey, encryptedMessage, byte(0), 1)
	if err != nil {
		return err
	}

	encryptedMessage, err = buildLayer(1, chosen_nodes[1].Address, circuitID, keySeeds[0], chosen_nodes[0].PubKey, encryptedMessage, byte(0), 1)
	if err != nil {
		return err
	}

	req := &routingpb.RelayRequest{Message: encryptedMessage}

	clientLogger.PrintLog("Request sending to server: %v", req)
	start := time.Now()
	_, err = client.RelayNodeRPC(context.Background(), req)
	duration := time.Since(start)
	if err != nil {
		return err
	}

	clientLogger.PrintLog("Connected using Onion-Routing")
	clientLogger.PrintLog("Request-Response time: %v", duration)
	log.Printf("Connected to TOR Server")
	log.Printf("Request-Response time: %v", duration)
	return nil
}

func sendRequest(client routingpb.RelayNodeServerClient, chosen_nodes []RelayNode, circuitID uint16, message string, reqType int) (error) {
	encryptedMessage, err := buildLayer(2, utils.ServerAddr, circuitID, keySeeds[2], chosen_nodes[2].PubKey, []byte(message), byte(1), byte(reqType))
	if err != nil {
		return err
	}

	encryptedMessage, err = buildLayer(2, chosen_nodes[2].Address, circuitID, keySeeds[1], chosen_nodes[1].PubKey, encryptedMessage, byte(0), byte(reqType))
	if err != nil {
		return err
	}

	encryptedMessage, err = buildLayer(2, chosen_nodes[1].Address, circuitID, keySeeds[0], chosen_nodes[0].PubKey, encryptedMessage, byte(0), byte(reqType))
	if err != nil {
		return err
	}
	req := &routingpb.RelayRequest{Message: encryptedMessage}

	clientLogger.PrintLog("Request sending to server: %v", req)
	start := time.Now()
	resp, err := client.RelayNodeRPC(context.Background(), req)
	duration := time.Since(start)
	if err != nil {
		log.Println("Error:", err)
		return err
	}
	clientLogger.PrintLog("Response received from server(Decrypted): %v", DecryptResponse(resp.Reply))
	clientLogger.PrintLog("Request-Response time: %v", duration)
	log.Printf("Response received from server(Decrypted): %s", DecryptResponse(resp.Reply))
	log.Printf("Request-Response time: %v", duration)

	return nil
}

func main() {
	circuitIDFlag := flag.Int("id", 1001, "circuit id for the client")
	flag.Parse()
	creds := utils.LoadCredentialsAsClient("certificates/ca.crt",
		"certificates/client.crt",
		"certificates/client.key")

	etcdClient, err := initEtcdClient()
	if err != nil {
		log.Fatalf("Failed to initialize etcd client: %v", err)
	}
	defer etcdClient.Close()

	err = checkEtcdStatus(etcdClient)
	if err != nil {
		log.Fatalf("Etcd Server is unreachable: %v", err)
	}
	clientLogger = utils.NewLogger("logs/client")

	nodes, err = getAvailableRelayNodes(etcdClient)
	if err != nil {
		log.Fatalf("Error while fetching available relays: %v", err)
	}
	if len(nodes) < 3 {
		log.Fatalf("Insufficient Relay Nodes available");
	}
	choosen_nodes := GetNodesInRoute(nodes)

	conn, err := grpc.NewClient(choosen_nodes[0].Address, grpc.WithTransportCredentials(creds))
	if err != nil {
		log.Fatalf("Error while connecting to server: %v\n", err)
	}
	defer conn.Close()

	client := routingpb.NewRelayNodeServerClient(conn)

	circuitID := *circuitIDFlag
	err = startCreationRoute(client, choosen_nodes, uint16(circuitID))
	if err != nil {
		log.Fatalf("Failed to Create Route: %v", err)
	}

	for {
		var reqType int 
		fmt.Printf("Enter Request Type: ")
		fmt.Scan(&reqType)
		if reqType > 3 || reqType < 0 {
			log.Printf("Invalid request type, Enter 1,2 or 3")
			continue;
		}
		if( reqType == 0) {
			log.Printf("Exiting Client")
			break;
		}
		var message string

		switch reqType {
		case 1:
			message = "Hi, This is Client"

		case 2:
			var n int
			fmt.Print("Enter n for Fibonacci: ")
			fmt.Scan(&n)
			message = fmt.Sprintf("%d", n)

		case 3:
			var n int
			fmt.Print("Enter n for random numbers: ")
			fmt.Scan(&n)
			message = fmt.Sprintf("%d", n)
		}
		
		for i := 0 ; i < MAX_ATTEMPTS ; i++{
			log.Printf("Attempt-[%d]:Sending Request with reqType-%d, message : %s\n", (i + 1), reqType, message)
			err = sendRequest(client, choosen_nodes, uint16(circuitID), message, reqType)
			if err == nil {
				break
			} else if utils.IsEqual(err, utils.ErrCircuitNotFound) {
				circuitID += 1
				log.Printf("Previous Route Not Exist, Creating New Route...\n")
				err = startCreationRoute(client, choosen_nodes, uint16(circuitID))
				if err != nil {
					log.Fatalf("Failed to Create Route: %v", err)
				}
			} else {
				log.Fatalf("Failed to Send Request; %v", err)
			}
		}
	}
	// for _ = range 10 {
	// 	sendRequest(client, choosen_nodes, uint16(circuitID), message)
	// }
}
