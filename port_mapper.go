package main

import (
	"errors"
	"os"
	"log"
	"fmt"
	"io"
	"strings"
)

var (
	ErrorHostNotMapped = errors.New("Host not mapped. ")
)

func NewHostMapper(file string) *HostMapper{
	mapper := &HostMapper{}
	mapper.HostMap = make(map[string]string)
	mapper.Load(file)
	return mapper
}

type HostMapper struct {
	HostMap map[string]string
}

func (hostMapper *HostMapper) Load(hostFile string) {
	file, err := os.Open(hostFile)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	log.Println("Loading map")
	for {
		var ip, host string
		if n, err := fmt.Fscanf(file, "%s %s\n", &ip, &host); err != io.EOF {
			if n == 2 && ip[0] != '#' {

				// 如果没有指定商品, 默认使用80端口
				ipSplits := strings.Split(ip, ":")
				if len(ipSplits)  == 1 {
					ip = ip + ":80"
				}

				log.Printf("%s -> %s", host, ip)
				hostMapper.HostMap[host] = ip
			}
		} else {
			break
		}
	}

	log.Println("Loading end")
}

func (hostMapper *HostMapper) FindMap(host string) (addr string, err error) {
	if addr, ok := hostMapper.HostMap[host]; ok {
		return addr, nil
	} else {
		return "", ErrorHostNotMapped
	}
}
