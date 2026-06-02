package utils

import (
	"time"
	"fmt"
	"net"
)

const (
	ServerAddr = "localhost:45034"
	EtcdServerAddr = "localhost:2379"
	EtcdTimeOutInterval time.Duration = 5
	EtcdLeaseTTL int64 = 3
	EtcdKeyPrefix string = "/relays/"
)

func GetAvaliablePort() (int, error) {
	listener, err := net.Listen("tcp", ":0") 
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func GetAvaliableAddress() (string, error) {
	port, err := GetAvaliablePort()
	if err != nil {
		return "", err
	}

	addr := fmt.Sprintf("127.0.0.1:%v", port)
	return addr, nil
}