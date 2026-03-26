// cmd/tunnel: SSH port-forward tunnel using password auth.
// Usage: go run webdesktopd/cmd/tunnel --pass=secret --local=19080 --remote=18080
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	host      := flag.String("host", "127.0.0.1", "remote SSH host")
	port      := flag.String("port", "32233", "remote SSH port")
	user      := flag.String("user", "abb", "SSH user")
	pass      := flag.String("pass", "", "SSH password")
	localPort := flag.String("local", "19080", "local listen port")
	remotePort := flag.String("remote", "18080", "remote target port")
	flag.Parse()

	cfg := &ssh.ClientConfig{
		User:            *user,
		Auth:            []ssh.AuthMethod{ssh.Password(*pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	sshAddr := fmt.Sprintf("%s:%s", *host, *port)
	client, err := ssh.Dial("tcp", sshAddr, cfg)
	if err != nil {
		log.Fatalf("SSH dial: %v", err)
	}
	defer client.Close()

	localAddr := "127.0.0.1:" + *localPort
	ln, err := net.Listen("tcp", localAddr)
	if err != nil {
		log.Fatalf("listen %s: %v", localAddr, err)
	}
	log.Printf("Tunnel: localhost:%s → %s:127.0.0.1:%s", *localPort, sshAddr, *remotePort)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			return
		}
		go forward(conn, client, "127.0.0.1:"+*remotePort)
	}
}

func forward(local net.Conn, client *ssh.Client, remoteAddr string) {
	defer local.Close()
	remote, err := client.Dial("tcp", remoteAddr)
	if err != nil {
		log.Printf("dial remote %s: %v", remoteAddr, err)
		return
	}
	defer remote.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(remote, local); done <- struct{}{} }()  //nolint:errcheck
	go func() { io.Copy(local, remote); done <- struct{}{} }()  //nolint:errcheck
	<-done
}
