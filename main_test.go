package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/lotsa"
)

func Test_Slice(t *testing.T) {
	delim := ","
	slices := bytes.Split([]byte(",(7499),(9999)"), []byte(delim))
	fmt.Println(len(slices))
}

//go test -timeout 50s github.com/recoilme/proxyhouse -run Test_Base
func Test_Base(t *testing.T) {
	log.SetOutput(ioutil.Discard) //disable log message on test
	go main()
	time.Sleep(1 * time.Second)
	addr := ":8124"
	//pipeline

	c, err := net.Dial("tcp", addr)
	if err != nil {
		panic(err)
	}
	defer c.Close()
	fmt.Println("Hello, test")

	lotsa.Output = os.Stdout
	lotsa.MemUsage = true

	println("-- bulk --")
	N := 10000
	fmt.Printf("\n")
	fmt.Printf("go version %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Printf("\n")
	fmt.Printf("     number of cpus: %d\n", runtime.NumCPU())
	fmt.Printf("     number of inserts: %d\n", N)
	lotsa.Ops(N, runtime.NumCPU(), func(i, _ int) {
		post(fmt.Sprintf("(%d)", i))
	})
	println("done")
	store.RLock()
	for req := range store.Req {
		slices := bytes.Split(store.Req[req].buffer, []byte(","))
		fmt.Printf("store:\n\nuri:%s\nbody:%d\n", req, len(slices))
	}
	store.RUnlock()
	time.Sleep(time.Duration(2) * time.Second)
}

func post(b string) {
	bod := strings.NewReader(b)
	req, err := http.NewRequest("POST", "http://127.0.0.1:8124/?query=INSERT%20INTO%20t%20VALUES", bod)
	if err != nil {
		panic(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	_, err = ioutil.ReadAll(resp.Body)

	if err != nil {
		panic(err)
	}
}
