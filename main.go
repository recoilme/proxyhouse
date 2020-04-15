package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
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

	"github.com/marpaia/graphite-golang"
	"github.com/recoilme/pudge"
	"github.com/tidwall/evio"
)

var (
	errClose       = errors.New("Error closed")
	version        = "0.1.6"
	port           = flag.Int("p", 8124, "TCP port number to listen on (default: 8124)")
	unixs          = flag.String("unixs", "", "unix socket")
	stdlib         = flag.Bool("stdlib", false, "use stdlib")
	noudp          = flag.Bool("noudp", true, "disable udp interface")
	workers        = flag.Int("workers", -1, "num workers")
	balance        = flag.String("balance", "random", "balance - random, round-robin or least-connections")
	keepalive      = flag.Int("keepalive", 10, "keepalive connection, in seconds")
	fwd            = flag.String("fwd", "http://localhost:8123", "forward to this server (clickhouse)")
	repl           = flag.String("repl", "", "replace this string on forward")
	delim          = flag.String("delim", ",", "body delimiter")
	syncsec        = flag.Int("syncsec", 2, "sync interval, in seconds")
	graphitehost   = flag.String("graphitehost", "", "graphite host")
	graphiteport   = flag.Int("graphiteport", 2023, "graphite port")
	graphiteprefix = flag.String("graphiteprefix", "relap.count.proxyhouse", "graphite prefix")
	isdebug        = flag.Bool("isdebug", false, "debug requests")
	resendint      = flag.Int("resendint", 60, "resend error interval, in steps")

	err400 = []byte("HTTP/1.1 400 OK\r\nContent-Length: 0\r\n\r\n")
	status = "OK\r\n"
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
var in uint32          //in requests
var out uint32         //out requests
var errorsCheck uint32 // Number of errors Check
var gr *graphite.Graphite
var buffersize = 1024 * 8

func main() {
	flag.Parse()
	//fix http client
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 1000

	store.backgroundManager(*syncsec)

	var totalConnections uint32 // Total number of connections opened since the server started running
	var currConnections int32   // Number of open connections

	atomic.StoreUint32(&totalConnections, 0)
	atomic.StoreInt32(&currConnections, 0)
	atomic.StoreUint32(&in, 0)
	atomic.StoreUint32(&out, 0)
	atomic.StoreUint32(&errorsCheck, 0)

	if *graphitehost != "" {
		g, err := graphite.NewGraphiteUDP(*graphitehost, *graphiteport)
		if err != nil {
			panic(err)
		}
		gr = g
	} else {
		gr = graphite.NewGraphiteNop(*graphitehost, *graphiteport)
	}

	letspanic := checkErr()
	if letspanic != nil {
		panic(letspanic)
	}

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
		store.cancelSyncer()
		fmt.Printf("TotalConnections:%d, CurrentConnections:%d\r\n", atomic.LoadUint32(&totalConnections), atomic.LoadInt32(&currConnections))
		fmt.Printf("In:%d, Out:%d\r\n", atomic.LoadUint32(&in), atomic.LoadUint32(&out))

		time.Sleep(time.Duration(*syncsec) * time.Second)
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

	events.NumLoops = *workers
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
			leftover, request := httpproto(data)
			response, err := parsereq(request)
			if err != nil {
				if bytes.Equal(err400, response) {
					out = response
					gr.SimpleSend(fmt.Sprintf("%s.wrong_requests", *graphiteprefix), "1")
				}
				if err != errClose {
					// bad thing happened
					fmt.Println(err.Error())
				}
				if err.Error() == "Close" {
					out = response //костыль для нагиос, простите
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

func parsereq(b []byte) ([]byte, error) {

	if i := bytes.Index(b, crlf); i >= 0 {
		method, uri, _, err := scanRequestLine(b[:i+len(crlf)])
		_ = version
		if err != nil {
			return err400, err
		}
		if method != "POST" {
			if method == "GET" && uri == "/status" {
				resp := fmt.Sprintf("status:%s", status)

				date := time.Now().UTC().Format(http.TimeFormat)
				reply := fmt.Sprintf("HTTP/1.1 200 Ok\r\nDate: %s\r\nServer: proxyhouse %s\r\nContent-Type: text/html\r\nContent-Length: %d\r\nConnection: Closed\r\n\r\n%s", date, version, len(resp), resp)
				status = "OK\r\n"
				return []byte(reply), errors.New("Close")
			}
			fmt.Println("Error, not post uri request:", string(b[:i+len(crlf)]))
			return err400, errors.New("Only POST supported")
		}
		if j := bytes.Index(b, crlfcrlf); j >= 0 {
			body := b[j+len(crlfcrlf):]
			if len(body) > 0 {
				store.Lock()
				_, ok := store.Req[uri]
				if !ok {
					store.Req[uri] = make([]byte, 0, buffersize)
				} else {
					store.Req[uri] = append(store.Req[uri], []byte(*delim)...)
				}
				store.Req[uri] = append(store.Req[uri], body...)

				store.Unlock()
				atomic.AddUint32(&in, 1)
				gr.SimpleSend(fmt.Sprintf("%s.requests_received", *graphiteprefix), "1")
				table := extractTable(uri)
				gr.SimpleSend(fmt.Sprintf("%s.bytable.%s.requests_received", *graphiteprefix, table), "1")
			}
		}
	}
	return []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"), nil
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
				fmt.Println("backgroundManager - canceled")
				return
			default:
				atomic.AddUint32(&errorsCheck, 1)
				currCheck := atomic.LoadUint32(&errorsCheck)
				if currCheck%uint32(*resendint) == 0 {
					nopanic := checkErr()
					if nopanic != nil {
						fmt.Println("nopanic:", nopanic.Error())
					}
				}
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
					val := store.Req[key]
					//val := new(bytes.Buffer)
					//_, err := io.Copy(val, bytes.NewReader(store.Req[key]))
					send(key, val, true)
					delete(store.Req, key)
					store.Unlock()
					//send 2 ch
					atomic.AddUint32(&out, 1)

				}
				time.Sleep(time.Duration(interval) * time.Second)
			}
		}
	}()
}

func extractTable(key string) string {
	table := "unknown"
	lowkey := strings.ToLower(key)
	if strings.Contains(lowkey, "insert%20into%20") {
		from := strings.Index(lowkey, "insert%20into%20")
		if from >= 0 {
			from += len("insert%20into%20")
			to := strings.Index(lowkey[from:], "%20")
			if to > 0 {
				table = lowkey[from:(to + from)]
			}
		}
	}
	if table == "unknown" {
		if strings.Contains(lowkey, "insert+into+") {
			from := strings.Index(lowkey, "insert+into+")
			if from >= 0 {
				from += len("insert+into+")
				to := strings.Index(lowkey[from:], "+")
				if to > 0 {
					table = lowkey[from:(to + from)]
				}
			}
		}
	}
	return table
}

//sender
func send(key string, val []byte, silent bool) (err error) {
	if *isdebug {
		fmt.Printf("time:%s\tkey:%s\tval:%s\n", time.Now(), key, val)
	}
	//send
	table := extractTable(key)
	uri := key
	if strings.HasPrefix(uri, "/") {
		uri = *fwd + uri
	} else {
		uri = strings.Replace(uri, *repl, *fwd, 1)
	}
	req, err := http.NewRequest("POST", uri /*fmt.Sprintf("%s%s", *fwd, key)*/, bytes.NewBuffer(val))

	slices := bytes.Split(val, []byte(*delim))
	gr.SimpleSend(fmt.Sprintf("%s.rows_sent", *graphiteprefix), fmt.Sprintf("%d", len(slices)))
	gr.SimpleSend(fmt.Sprintf("%s.requests_sent", *graphiteprefix), "1")
	gr.SimpleSend(fmt.Sprintf("%s.bytable.%s.rows_sent", *graphiteprefix, table), fmt.Sprintf("%d", len(slices)))
	gr.SimpleSend(fmt.Sprintf("%s.bytable.%s.requests_sent", *graphiteprefix, table), "1")

	if err != nil {
		gr.SimpleSend(fmt.Sprintf("%s.ch_errors", *graphiteprefix), "1")
		fmt.Printf("%s\n", err)
		if silent && len(val) > 0 {
			db := fmt.Sprintf("errors/%d", time.Now().UnixNano())
			pudge.Set(db, key, val)
			pudge.Close(db)
		}
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err == nil && resp.StatusCode != 200 {
		err = errors.New("Error: response code not 200")
	}
	if err != nil {
		fmt.Printf("%s\n", err)
		status = err.Error() + "\r\n"
		gr.SimpleSend(fmt.Sprintf("%s.ch_errors", *graphiteprefix), "1")
		if resp != nil {
			bodyResp, _ := ioutil.ReadAll(resp.Body)
			fmt.Printf("Response: status: %d body:%s \n", resp.StatusCode, bodyResp)
		}
		if silent && len(val) > 0 {

			db := fmt.Sprintf("errors/%d", time.Now().UnixNano())
			pudge.Set(db, key, val)
			pudge.Close(db)
		}
		return
	}
	defer resp.Body.Close()
	return
}

func checkErr() (err error) {
	list, err := filePathWalkDir("errors")
	if err != nil {
		if err.Error() != "lstat errors: no such file or directory" {
			return err
		}
		//send empty err if no errors
		return nil
	}
	sort.Sort(sort.StringSlice(list))
	for _, file := range list {
		db, err := pudge.Open("errors/"+file, nil)
		fmt.Println("Proccessing error:", file)
		if err != nil {
			return err
		}
		keys, err := db.Keys(nil, 0, 0, true)
		if err != nil {
			return err
		}
		for _, key := range keys {
			//println(key)
			var val []byte
			err := db.Get(key, &val)
			if err != nil {
				return err
			}
			//buf := new(bytes.Buffer)
			//io.Copy(buf, bytes.NewReader(val))
			err = send(string(key), val, false)

			if err != nil {
				return err
			}
		}
		db.DeleteFile()
		// sleep 3 seconds to prevent throttling CH
		time.Sleep(3 * time.Second)
	}
	return
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
