package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "expvar"

	"github.com/recoilme/pudge"
	"github.com/tidwall/evio"
)

var (
	errClose  = errors.New("Error closed")
	version   = "0.0.1"
	port      = flag.Int("p", 8124, "TCP port number to listen on (default: 8124)")
	unixs     = flag.String("unixs", "", "unix socket")
	stdlib    = flag.Bool("stdlib", false, "use stdlib")
	noudp     = flag.Bool("noudp", false, "disable udp interface")
	loops     = flag.Int("loops", -1, "num loops")
	balance   = flag.String("balance", "random", "balance - random, round-robin or least-connections")
	keepalive = flag.Int("keepalive", 10, "keepalive connection, in seconds")
	fwd       = flag.String("fwd", "http://localhost:8123", "forward to this server (clickhouse)")
	delim     = flag.String("delim", ",", "body delimiter")
	syncsec   = flag.Int("syncsec", 2, "sync interval, in seconds")
)

type conn struct {
	is   evio.InputStream
	addr string
}

type Store struct {
	sync.RWMutex
	Req          map[string][]byte
	cancelSyncer context.CancelFunc
}

var store = &Store{Req: make(map[string][]byte, 0)}
var in uint32  //in requests
var out uint32 //out requests

func main() {
	flag.Parse()

	store.backgroundManager(*syncsec)

	var totalConnections uint32 // Total number of connections opened since the server started running
	var currConnections int32   // Number of open connections

	atomic.StoreUint32(&totalConnections, 0)
	atomic.StoreInt32(&currConnections, 0)
	atomic.StoreUint32(&in, 0)
	atomic.StoreUint32(&out, 0)

	checkErr()

	// Wait for interrupt signal to gracefully shutdown the server with
	// setup signal catching
	quit := make(chan os.Signal, 1)
	// catch all signals since not explicitly listing
	signal.Notify(quit)
	// method invoked upon seeing signal
	go func() {
		q := <-quit
		fmt.Printf("\nRECEIVED SIGNAL: %s\n", q)
		//ignore broken pipe?
		if q == syscall.SIGPIPE || q.String() == "broken pipe" || q.String() == "window size changes" {
			return
		}

		fmt.Printf("TotalConnections:%d, CurrentConnections:%d\r\n", atomic.LoadUint32(&totalConnections), atomic.LoadInt32(&currConnections))
		fmt.Printf("In:%d, Out:%d\r\n", atomic.LoadUint32(&in), atomic.LoadUint32(&out))

		time.Sleep(time.Duration(*syncsec*2) * time.Second)
		os.Exit(1)
	}()

	var events evio.Events
	switch *balance {
	default:
		log.Fatalf("invalid -balance flag: '%v'", balance)
	case "random":
		events.LoadBalance = evio.Random
	case "round-robin":
		events.LoadBalance = evio.RoundRobin
	case "least-connections":
		events.LoadBalance = evio.LeastConnections
	}

	events.NumLoops = *loops
	events.Serving = func(srv evio.Server) (action evio.Action) {
		fmt.Printf("proxyhouse started on port %d (loops: %d)\n", *port, srv.NumLoops)
		return
	}
	events.Opened = func(ec evio.Conn) (out []byte, opts evio.Options, action evio.Action) {
		atomic.AddUint32(&totalConnections, 1)
		atomic.AddInt32(&currConnections, 1)
		//fmt.Printf("opened: %v\n", ec.RemoteAddr())
		if (*keepalive) > 0 {
			opts.TCPKeepAlive = time.Second * (time.Duration(*keepalive))
			//fmt.Println("TCPKeepAlive:", opts.TCPKeepAlive)
		}
		//opts.ReuseInputBuffer = true // don't do it!
		ec.SetContext(&conn{})
		return
	}
	events.Closed = func(ec evio.Conn, err error) (action evio.Action) {
		//fmt.Printf("closed: %v\n", ec.RemoteAddr())
		atomic.AddInt32(&currConnections, -1)
		return
	}

	events.Data = func(ec evio.Conn, in []byte) (out []byte, action evio.Action) {
		if in == nil {
			fmt.Printf("wake from %s\n", ec.RemoteAddr())
			return nil, evio.Close
		}
		//println(string(in))
		var data []byte
		var c *conn
		if ec.Context() == nil {
			data = in
		} else {
			c = ec.Context().(*conn)
			data = c.is.Begin(in)
		}
		//responses := make([]byte, 0)
		for {
			leftover, response, err := proxy(data)
			if err != nil {
				if err != errClose {
					// bad thing happened
					fmt.Println(err.Error())
				}
				action = evio.Close
				break
			} else if len(leftover) == len(data) {
				// request not ready, yet
				break
			}
			// handle the request
			//println("handle the request", string(response))
			//responses = append(responses, response...)
			out = response
			data = leftover
		}
		//println("handle the responses", string(responses))
		//out = responses
		if c != nil {
			c.is.End(data)
		}

		return
	}
	var ssuf string
	if *stdlib {
		ssuf = "-net"
	}
	addrs := []string{fmt.Sprintf("tcp"+ssuf+"://:%d", *port)} //?reuseport=true
	if *unixs != "" {
		addrs = append(addrs, fmt.Sprintf("unix"+ssuf+"://%s", *unixs))
	}
	if !*noudp {
		addrs = append(addrs, fmt.Sprintf("udp"+ssuf+"://:%d", *port))
	}
	err := evio.Serve(events, addrs...)
	if err != nil {
		fmt.Println(err.Error())
		log.Fatal(err)
	}
}

func proxy(b []byte) ([]byte, []byte, error) {
	if len(b) == 0 {
		return b, nil, nil
	}

	buf := bufio.NewReader(bytes.NewReader(b))
	req, err := http.ReadRequest(buf)
	if err != nil {
		if err == io.EOF {
			return b[len(b):], nil, nil
			//println("EOF")
			//	break
		}
		println(err.Error())

		return b, nil, err
	}
	_ = req
	//fmt.Printf("req:%+v\n", req)
	bufbody := new(bytes.Buffer)
	io.Copy(bufbody, req.Body)
	req.Body.Close()
	req.Body = ioutil.NopCloser(bufbody)

	store.Lock()
	_, ok := store.Req[req.RequestURI]
	if !ok {
		store.Req[req.RequestURI] = make([]byte, 0)
	} else {
		store.Req[req.RequestURI] = append(store.Req[req.RequestURI], []byte(*delim)...)
	}
	store.Req[req.RequestURI] = append(store.Req[req.RequestURI], bufbody.Bytes()...)
	store.Unlock()
	atomic.AddUint32(&in, 1)
	return b[len(b):], []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"), nil
}

// backgroundManager runs continuously in the background and performs various
// operations such as forward requests.
func (store *Store) backgroundManager(interval int) {
	ctx, cancel := context.WithCancel(context.Background())
	store.cancelSyncer = cancel
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				//keys reader
				store.RLock()
				keys := make([]string, 0)
				for key := range store.Req {
					keys = append(keys, key)
				}
				store.RUnlock()

				//keys itterator
				for _, key := range keys {
					//read as fast as possible and return mutex
					store.Lock()
					val := new(bytes.Buffer)
					_, err := io.Copy(val, bytes.NewReader(store.Req[key]))
					delete(store.Req, key)
					if err != nil {
						fmt.Printf("%s\n", err)
						store.Unlock()
						continue
					}
					store.Unlock()
					//send 2 ch
					atomic.AddUint32(&out, 1)
					go send(key, val, true)
				}
				time.Sleep(time.Duration(interval) * time.Second)
			}
		}
	}()
}

//sender
func send(key string, val *bytes.Buffer, silent bool) (err error) {
	//send
	req, err := http.NewRequest("POST", fmt.Sprintf("%s%s", *fwd, key), val)
	if err != nil {
		fmt.Printf("%s\n", err)
		if silent {
			db := fmt.Sprintf("errors/%d", time.Now().Unix())
			pudge.Set(db, key, val.Bytes())
			pudge.Close(db)
		}
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("%s\n", err)
		if silent {
			db := fmt.Sprintf("errors/%d", time.Now().Unix())
			pudge.Set(db, key, val.Bytes())
			pudge.Close(db)
		}
		return
	}
	defer resp.Body.Close()
	return
}

func checkErr() {
	list, err := filePathWalkDir("errors")
	if err != nil {
		if err.Error() != "lstat errors: no such file or directory" {
			panic(err)
		} else {
			//no errors
			return
		}
	}
	sort.Sort(sort.StringSlice(list))
	for _, file := range list {
		//println(file)
		db, err := pudge.Open("errors/"+file, nil)

		if err != nil {
			panic(err)
		}
		keys, err := db.Keys(nil, 0, 0, true)
		if err != nil {
			panic(err)
		}
		for _, key := range keys {
			//println(key)
			var val []byte
			err := db.Get(key, &val)
			if err != nil {
				panic(err)
			}
			buf := new(bytes.Buffer)
			io.Copy(buf, bytes.NewReader(val))
			err = send(string(key), buf, false)

			if err != nil {
				panic(err)
			}
		}
		db.DeleteFile()
	}
}

func filePathWalkDir(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Println(err.Error())
			return err
		}
		if !info.IsDir() {
			if !strings.HasSuffix(path, ".idx") {
				files = append(files, filepath.Base(path))
			}

		}
		return nil
	})
	return files, err
}
