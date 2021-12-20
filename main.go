package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "expvar"

	"github.com/marpaia/graphite-golang"
	"github.com/recoilme/graceful"
	"github.com/recoilme/pudge"
	"github.com/tidwall/evio"
)

var (
	errClose       = errors.New("Error closed")
	version        = "0.2.0"
	port           = flag.Int("p", 8124, "TCP port number to listen on (default: 8124)")
	keepalive      = flag.Int("keepalive", 10, "keepalive connection, in seconds")
	readtimeout    = flag.Int("readtimeout", 5, "request header read timeout, in seconds")
	fwd            = flag.String("fwd", "http://localhost:8123", "forward to this server (clickhouse)")
	repl           = flag.String("repl", "", "replace this string on forward")
	delim          = flag.String("delim", ",", "body delimiter")
	syncsec        = flag.Int("syncsec", 2, "sync interval, in seconds")
	graphitehost   = flag.String("graphitehost", "", "graphite host")
	graphiteport   = flag.Int("graphiteport", 2023, "graphite port")
	graphiteprefix = flag.String("graphiteprefix", "relap.count.proxyhouse", "graphite prefix")
	grayloghost    = flag.String("grayloghost", "", "graylog host")
	graylogport    = flag.Int("graylogport", 12201, "graylog port")
	isdebug        = flag.Bool("isdebug", false, "debug requests")
	resendint      = flag.Int("resendint", 60, "resend error interval, in seconds")
	warnlevel      = flag.Int("w", 400, "error counts for warning level")
	critlevel      = flag.Int("c", 500, "error counts for error level")

	status           = "OK\r\n"
	graylog *Graylog = nil
)

const (
	ERROR_DIR = "errors"
)

type conn struct {
	is   evio.InputStream
	addr string
}

type Buffer struct {
	rowcount int
	buffer   []byte
}

type Store struct {
	sync.RWMutex
	Req          map[string]*Buffer
	cancelSyncer context.CancelFunc
}

var store = &Store{Req: make(map[string]*Buffer, 0)}
var totalConnections uint32 // Total number of connections opened since the server started running
var currConnections int32   // Number of open connections
var idleConnections int32   // Number of idle connections
var in uint32               //in requests
var out uint32              //out requests
var errorsCheck uint32      // Number of errors Check
var gr *graphite.Graphite
var buffersize = 1024 * 8
var hostname string

func main() {
	flag.Parse()
	//fix http client
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 1000

	store.backgroundSender(*syncsec)
	store.backgroundRecovery(*resendint)

	atomic.StoreUint32(&totalConnections, 0)
	atomic.StoreInt32(&currConnections, 0)
	atomic.StoreInt32(&idleConnections, 0)
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
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	hostname = strings.ReplaceAll(host, ".", "_")

	if *grayloghost != "" {
		graylog = NewGraylog(Graylog{Host: *grayloghost, Port: *graylogport})
		graylog.Info("Start proxyhouse")
	}

	_, err = os.Stat(ERROR_DIR)
	if err != nil {
		panic(err)
	}

	// Wait for interrupt signal to gracefully shutdown the server with
	// setup signal catching
	quit := make(chan os.Signal, 1)
	fallback := func() error {
		fmt.Println("Some signal - ignored")
		grlog(LEVEL_INFO, "Some signal - ignored")
		return nil
	}
	graceful.Unignore(quit, fallback, graceful.Terminate...)

	server := &http.Server{
		Addr:              ":" + fmt.Sprint(*port),
		ReadHeaderTimeout: time.Duration(*readtimeout) * time.Second,
		IdleTimeout:       time.Duration(*keepalive) * time.Second,
		ConnState:         statelistener,
	}
	http.HandleFunc("/", dorequest)
	http.HandleFunc("/status", showstatus)
	http.HandleFunc("/statistic", showstatistic)
	err = server.ListenAndServe()
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
		os.Exit(1)
	}
}

func grlog(level uint8, data ...interface{}) {
	if graylog != nil {
		graylog.Log(level, data...)
	} else {
		fmt.Println(data...)
	}
}

func dorequest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "404 not found.", http.StatusNotFound)
		return
	}

	switch r.Method {
	case "GET":
		date := time.Now().UTC().Format(http.TimeFormat)
		w.Header().Set("Date", date)
		w.Header().Set("Server", "proxyhouse "+version)
		w.Header().Set("Connection", "Closed")
		fmt.Fprint(w, "status = \"OK\"\r\n")
		return

	case "POST":
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()
		uri := r.URL.RawPath + "?" + r.URL.RawQuery
		if len(body) > 0 {
			delimiter := []byte(*delim)
			separator := []byte("),")
			addrows := 1
			q := r.URL.Query().Get("query")
			if strings.HasSuffix(q, "FORMAT TSV") || strings.HasSuffix(q, "FORMAT CSV") {
				delimiter = []byte("")
				separator = []byte("\n")
				addrows = 0
			}
			store.Lock()
			buf, ok := store.Req[uri]
			if !ok {
				buf = &Buffer{rowcount: 0, buffer: make([]byte, 0, buffersize)}
			} else {
				buf.buffer = append(buf.buffer, delimiter...)
			}
			buf.buffer = append(buf.buffer, body...)
			buf.rowcount += addrows + bytes.Count(body, separator)
			store.Req[uri] = buf

			store.Unlock()
			atomic.AddUint32(&in, 1)
			gr.SimpleSend(fmt.Sprintf("%s.requests_received", *graphiteprefix), "1")
			gr.SimpleSend(fmt.Sprintf("%s.byhost.%s.requests_received", *graphiteprefix, hostname), "1")
			table := extractTable(uri)
			gr.SimpleSend(fmt.Sprintf("%s.bytable.%s.requests_received", *graphiteprefix, table), "1")
			gr.SimpleSend(fmt.Sprintf("%s.bytes_received", *graphiteprefix), fmt.Sprintf("%d", len(body)))
			gr.SimpleSend(fmt.Sprintf("%s.byhost.%s.bytes_received", *graphiteprefix, hostname), fmt.Sprintf("%d", len(body)))
			gr.SimpleSend(fmt.Sprintf("%s.bytable.%s.bytes_received", *graphiteprefix, table), fmt.Sprintf("%d", len(body)))
			w.Header().Set("Server", "proxyhouse "+version)
			w.Header().Set("Content-type", "text/tab-separated-values; charset=UTF-8")
		} else {
			http.Error(w, "No data given.", http.StatusMethodNotAllowed)
		}

	default:
		http.Error(w, "Sorry, only GET and POST methods are supported.", http.StatusMethodNotAllowed)
	}
}

func showstatus(w http.ResponseWriter, r *http.Request) {
	errcount := 0
	list, err := filePathWalkDir(ERROR_DIR)
	if err == nil {
		errcount = len(list)
	}

	date := time.Now().UTC().Format(http.TimeFormat)
	w.Header().Set("Date", date)
	w.Header().Set("Server", "proxyhouse "+version)
	w.Header().Set("Connection", "Closed")
	if errcount >= *critlevel {
		w.WriteHeader(http.StatusInternalServerError)
	} else if errcount >= *warnlevel {
		w.WriteHeader(http.StatusBadRequest)
	}
	fmt.Fprintf(w, "status:%s", status)
}

func showstatistic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", "proxyhouse "+version)
	w.Header().Set("Connection", "Closed")
	fmt.Fprintf(w, "total connections:%d\r\n", atomic.LoadUint32(&totalConnections))
	fmt.Fprintf(w, "current connections:%d\r\n", atomic.LoadInt32(&currConnections))
	fmt.Fprintf(w, "idle connections:%d\r\n", atomic.LoadInt32(&idleConnections))
	fmt.Fprintf(w, "in requests:%d\r\n", atomic.LoadUint32(&in))
	fmt.Fprintf(w, "out requests:%d\r\n", atomic.LoadUint32(&out))
}

func statelistener(c net.Conn, cs http.ConnState) {
	switch cs {
	case http.StateNew:
		atomic.AddUint32(&totalConnections, 1)
		atomic.AddInt32(&currConnections, 1)
		atomic.AddInt32(&idleConnections, 1)
	case http.StateActive:
		atomic.AddInt32(&idleConnections, -1)
	case http.StateIdle:
		atomic.AddInt32(&idleConnections, 1)
	case http.StateClosed:
		atomic.AddInt32(&currConnections, -1)
		atomic.AddInt32(&idleConnections, -1)
	}
}

// backgroundSender runs continuously in the background and performs various
// operations such as forward requests.
func (store *Store) backgroundSender(interval int) {
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
				store.Lock()
				requests := store.Req
				store.Req = make(map[string]*Buffer)
				store.Unlock()
				//keys itterator
				for key, val := range requests {
					send(key, val.buffer, val.rowcount, 0)
					atomic.AddUint32(&out, 1)

				}
				time.Sleep(time.Duration(interval) * time.Second)
			}
		}
	}()
}

// backgroundRecovery run continuously in background and try recovery errors
func (store *Store) backgroundRecovery(interval int) {
	ctx, cancel := context.WithCancel(context.Background())
	store.cancelSyncer = cancel
	go func() {
		for {
			select {
			case <-ctx.Done():
				fmt.Println("backgroundManager - canceled")
				return
			default:
				nopanic := checkErr()
				if nopanic != nil {
					fmt.Println("nopanic:", nopanic.Error())
				}
			}
			time.Sleep(time.Duration(interval) * time.Second)
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

// вырезаем из строки password=xxxxx для логов
func hidePassword(str string) string {
	replace := "password="
	pos := strings.Index(str, replace)
	if pos < 0 {
		return str
	}
	pos2 := strings.Index(str[pos:], "&")
	if pos2 < 0 {
		return str[0:pos+len(replace)] + "*"
	}
	return str[0:pos+len(replace)] + "*" + str[pos+pos2:]
}

func saveToErrors(key string, val []byte, level int) {
	prefix := strconv.Itoa(level)
	if level >= 10 {
		prefix = "O"
	}
	db := fmt.Sprintf("%s/%s%d", ERROR_DIR, prefix, time.Now().UnixNano())
	pudge.Set(db, key, val)
	pudge.Close(db)
}

//sender
func send(key string, val []byte, rowcount int, level int) (err error) {
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

	bytes := len(val)
	gr.SimpleSend(fmt.Sprintf("%s.rows_sent", *graphiteprefix), fmt.Sprintf("%d", rowcount))
	gr.SimpleSend(fmt.Sprintf("%s.requests_sent", *graphiteprefix), "1")
	gr.SimpleSend(fmt.Sprintf("%s.byhost.%s.rows_sent", *graphiteprefix, hostname), fmt.Sprintf("%d", rowcount))
	gr.SimpleSend(fmt.Sprintf("%s.byhost.%s.requests_sent", *graphiteprefix, hostname), "1")
	gr.SimpleSend(fmt.Sprintf("%s.bytable.%s.rows_sent", *graphiteprefix, table), fmt.Sprintf("%d", rowcount))
	gr.SimpleSend(fmt.Sprintf("%s.bytable.%s.requests_sent", *graphiteprefix, table), "1")
	gr.SimpleSend(fmt.Sprintf("%s.bytes_sent", *graphiteprefix), fmt.Sprintf("%d", bytes))
	gr.SimpleSend(fmt.Sprintf("%s.byhost.%s.bytes_sent", *graphiteprefix, hostname), fmt.Sprintf("%d", bytes))
	gr.SimpleSend(fmt.Sprintf("%s.bytable.%s.bytes_sent", *graphiteprefix, table), fmt.Sprintf("%d", bytes))

	if err != nil {
		gr.SimpleSend(fmt.Sprintf("%s.ch_errors", *graphiteprefix), "1")
		gr.SimpleSend(fmt.Sprintf("%s.byhost.%s.ch_errors", *graphiteprefix, hostname), "1")
		gr.SimpleSend(fmt.Sprintf("%s.bytable.%s.ch_errors", *graphiteprefix, table), "1")
		grlog(LEVEL_ERR, "Create request error: ", hidePassword(uri), " error: ", err)
		if len(val) > 0 {
			saveToErrors(key, val, level+1)
		}
		return
	}
	resp, err := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if err == nil && resp.StatusCode != 200 {
		err = errors.New("Error: response code not 200")
	}
	if err != nil {
		grlog(LEVEL_ERR, "Request error: ", hidePassword(uri), " error: ", err)
		status = err.Error() + "\r\n"
		gr.SimpleSend(fmt.Sprintf("%s.ch_errors", *graphiteprefix), "1")
		gr.SimpleSend(fmt.Sprintf("%s.byhost.%s.ch_errors", *graphiteprefix, hostname), "1")
		gr.SimpleSend(fmt.Sprintf("%s.bytable.%s.ch_errors", *graphiteprefix, table), "1")
		if resp != nil {
			bodyResp, _ := ioutil.ReadAll(resp.Body)
			grlog(LEVEL_ERR, "Response: status: ", resp.StatusCode, " body: ", string(bodyResp))
		}
		if len(val) > 0 {
			saveToErrors(key, val, level+1)
		}
		return
	} else {
		status = "OK\r\n"
	}
	return
}

func checkErr() (err error) {
	list, err := filePathWalkDir(ERROR_DIR)
	if err != nil {
		if err.Error() != "lstat errors: no such file or directory" {
			return err
		}
		//send empty err if no errors
		return nil
	}
	sort.Sort(sort.StringSlice(list))
	fmt.Println("process files ", list)
	for _, file := range list {
		db, err := pudge.Open(ERROR_DIR+"/"+file, nil)
		grlog(LEVEL_ERR, "Proccessing error:", file)
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
			level, err := strconv.Atoi(file[0:1])
			if err != nil {
				// if filename first symbol not digit skip
				continue
			}
			send(string(key), val, 1, level)
			time.Sleep(time.Second)
		}
		db.DeleteFile()
	}
	return
}

func filePathWalkDir(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			grlog(LEVEL_ERR, "dirwalk error ", err)
			return err
		}
		filename := filepath.Base(path)
		if !info.IsDir() && !strings.HasSuffix(filename, ".idx") && !strings.HasPrefix(filename, "O") {
			files = append(files, filename)

		}
		return nil
	})
	return files, err
}
