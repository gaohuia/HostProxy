package main

import (
	"log"
	"net"
	"io"
	"bufio"
	"strings"
	"fmt"
	"time"
	"tls"
)

func NewProxyServer() (*ProxyServer) {
	return &ProxyServer{
		ConnectionTimeout: 3,
	}
}

type ProxyServer struct {
	ConnectionTimeout uint
	ServerConnections []net.Conn
	ClientConnections []net.Conn
	Mapper *HostMapper
}

func (mapper *ProxyServer) LoadMapper() {
	mapper.Mapper = NewHostMapper("mapper.txt")
}

func (mapper *ProxyServer) StripSocket(connections []net.Conn, conn net.Conn) []net.Conn {
	result := make([]net.Conn, 0, len(connections))
	for _, connection := range connections {
		if conn == connection {
			continue
		}

		result = append(result, connection)
	}
	return result
}

func (mapper *ProxyServer) ReadHeader(conn net.Conn) ([]string, io.Reader) {
	headers := make([]string, 0, 20)

	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}

		line = strings.Trim(line, "\r\n")
		if len(line) == 0 {
			if len(headers) > 0 {
				break
			}
		} else {
			headers = append(headers, line)
		}
	}

	return headers, reader
}

func (mapper *ProxyServer) WriteResponse(conn net.Conn, content string) {
	length := len(content)
	response := []string{
		"HTTP/1.1 200 OK",
		"Content-Type: text/html",
		fmt.Sprintf("Content-Length: %d", length),
		"",
		content,
	}

	writer := bufio.NewWriter(conn)
	for _, line := range response {
		log.Print(line)
		writer.WriteString(line + "\r\n")
	}

	writer.Flush()
}

func (mapper *ProxyServer) Bridge(conn1 io.Writer, conn2 io.Reader, r chan int) {
	log.Println("Bridge built")
	io.Copy(conn1, conn2)
	log.Println("Bridge end")
	r <- 1
}

func (mapper *ProxyServer) FlushSockets() {
	log.Println("FlushSockets")

	for _, conn := range mapper.ServerConnections {
		conn.Close()
	}

	for _, conn := range mapper.ClientConnections {
		conn.Close()
	}

	mapper.ServerConnections = make([]net.Conn, 0, 20)
	mapper.ClientConnections = make([]net.Conn, 0, 20)
}


func (mapper *ProxyServer) HandlerConnection(conn net.Conn) {
	defer conn.Close()

	var host, requestLine string
	headers, reader := mapper.ReadHeader(conn)

	if len(headers) > 1 {
		requestLine = headers[0]
		requestLineSplit := strings.Split(requestLine, " ")
		if requestLineSplit[1] == "/flushSockets" {
			mapper.FlushSockets()
			mapper.LoadMapper()
			mapper.WriteResponse(conn, "ok")
			return
		}

		for _, line := range headers[1:] {
			split := strings.SplitN(line, ":", 2)
			if len(split) == 2 {
				key, value := split[0], strings.Trim(split[1], " ")
				if key == "Host" {
					host = value
				}
			}
		}
	} else {
		log.Println("Bad header")
		for _, line := range headers {
			log.Println(line)
		}
		mapper.WriteResponse(conn, "Bad header")
		return
	}

	log.Println(requestLine)
	log.Printf("Host: %s", host)

	addr, err := mapper.Mapper.FindMap(host)
	if err != nil {
		mapper.WriteResponse(conn, fmt.Sprintf("Can't detect host %s", host))
		return
	}

	dial, err := net.DialTimeout("tcp", addr, time.Second * time.Duration(mapper.ConnectionTimeout))
	if err != nil {
		mapper.WriteResponse(conn, fmt.Sprintf("%s", err))
		return
	}
	defer dial.Close()

	r1, r2 := make(chan int), make(chan int)
	go mapper.Bridge(dial, reader, r1)
	go mapper.Bridge(conn, dial, r2)

	writer := bufio.NewWriter(dial)
	for _, line := range headers {
		writer.WriteString(line + "\r\n")
	}
	writer.WriteString("\r\n")
	writer.Flush()

	mapper.ServerConnections = append(mapper.ServerConnections, conn)
	mapper.ClientConnections = append(mapper.ClientConnections, dial)

	select {
	case <- r1:
	case <- r2:
	}

	mapper.ServerConnections = mapper.StripSocket(mapper.ServerConnections, conn)
	mapper.ClientConnections = mapper.StripSocket(mapper.ClientConnections, dial)
}

func (mapper *ProxyServer) HandlerTLS(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)

	var serverName string
	packet := tls.ReadPacket(reader)
	if packet.Header.ContentType == 22 {
		hello := tls.ReadClientHello(packet)

		for _, e := range hello.Extension {
			if e.ExtensionType == 0 {
				log.Printf("ServerName: %s", string(e.Data[5:]))
				serverName = string(e.Data[5:])
			}
		}
	}

	if len(serverName) > 0 {
		addr, err := mapper.Mapper.FindMap(serverName)
		if err != nil {
			mapper.WriteResponse(conn, fmt.Sprintf("Can't detect host %s", serverName))
			return
		}

		addr = strings.Replace(addr, ":80", ":443", -1)
		log.Printf("making connection to %s", addr)
		dial, err := net.DialTimeout("tcp", addr, time.Second * time.Duration(mapper.ConnectionTimeout))
		if err != nil {
			mapper.WriteResponse(conn, fmt.Sprintf("%s", err))
			return
		}
		defer dial.Close()

		r1, r2 := make(chan int), make(chan int)
		go mapper.Bridge(dial, reader, r1)
		go mapper.Bridge(conn, dial, r2)

		writer := bufio.NewWriter(dial)
		tls.WritePacketTo(packet, writer)
		writer.Flush()

		mapper.ServerConnections = append(mapper.ServerConnections, conn)
		mapper.ClientConnections = append(mapper.ClientConnections, dial)

		select {
		case <- r1:
		case <- r2:
		}

		mapper.ServerConnections = mapper.StripSocket(mapper.ServerConnections, conn)
		mapper.ClientConnections = mapper.StripSocket(mapper.ClientConnections, dial)
	}
}

func (mapper *ProxyServer) Listen(listen string, handler func(net.Conn)) {
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		log.Printf("Listen on %s failed, %s\n", listen, err)
		return
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Accept failed, %s", err)
		}

		go handler(conn)
	}
}

func (mapper *ProxyServer) Start() {
	log.Println("ProxyServer starting")

	mapper.LoadMapper()

	go mapper.Listen(":80", mapper.HandlerConnection)
	go mapper.Listen(":443", mapper.HandlerTLS)

	c := make(chan int)
	<- c
}