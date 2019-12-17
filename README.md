

`proxyhouse`

[![GoDoc](https://img.shields.io/badge/api-reference-blue.svg?style=flat-square)](https://godoc.org/github.com/recoilme/proxyhouse)

proxyhouse is a fast experimental clickhouse proxy.


# Getting Started

## Installing

To start using `proxyhouse`, install Go and run `go get`:

```sh
$ go get -u github.com/recoilme/proxyhouse
$ GOOS=linux go build
```

This will retrieve and build the server. Or grab compiled binary version.

## Starting

Use `./proxyhouse --help` for full list of params. Example:

```sh
./proxyhouse -p 11215
# Start server on port 11215
```

or just ./proxyhouse - for start with default params

## How it work

`proxyhouse` is a proxy for insert request in clickhouse.

You send post request:


```$ echo '(1),(2),(3)' | curl 'http://localhost:8124/?query=INSERT%20INTO%20t%20FORMAT%20Values' --data-binary @-```


proxyhouse create map:

`requests['/?query=INSERT%20INTO%20t%20FORMAT%20Values']= '(1),(2),(3)'`


You send next request:

```$ echo '(4),(5),(6)' | curl 'http://localhost:8124/?query=INSERT%20INTO%20t%20FORMAT%20Values' --data-binary @-```


proxyhouse add `,` and body value too map:


`requests['/?query=INSERT%20INTO%20t%20FORMAT%20Values']= '(1),(2),(3),(4),(5),(6)'`

Every second - proxyhouse flush all gathered requests in clickhouse.

## Example (send 100 req parallel)

```
Requests: 

	http.NewRequest("POST", "http://127.0.0.1:8124/?query=INSERT%20INTO%20t%20VALUES", (fmt.Sprintf("(%d)", i)))

Result:

uri:
/?query=INSERT%20INTO%20t%20VALUES

body:
(75),(0),(50),(25),(76),(1),(77),(26),(51),(52),(2),(27),(28),(53),(78),(54),(29),(79),(55),(3),(80),(56),(30),(31),(4),(81),(57),(5),(32),(82),(58),(6),(83),(33),(59),(7),(84),(60),(85),(8),(34),(9),(61),(86),(35),(62),(10),(87),(11),(63),(88),(64),(12),(89),(36),(13),(65),(90),(37),(66),(91),(38),(67),(39),(92),(14),(40),(15),(93),(68),(41),(16),(69),(42),(94),(17),(70),(95),(43),(71),(18),(44),(96),(72),(19),(45),(20),(73),(97),(74),(46),(21),(98),(47),(22),(48),(23),(49),(24),(99)
```

## Graphite

Proxyhouse will send to Graphite this metrics:

 - count.proxyhouse.ch_errors //Clickhouse error
 - count.proxyhouse.wrong_requests // wrong request
 - count.proxyhouse.rows_sent // count sended values
 - count.proxyhouse.requests_sent // count sended requests
 - count.proxyhouse.requests_received // count recieved requests

## Failover

In case of errors:

- wrong request (not POST with INSERT)-> send to 400 to client and grafite wrong_requests
- clickhouse is down -> Send to graphite ch_errors count (+1) -> write packets to errors dir (by interval)
- every 60 steps - try to resend packets from errors folder -> on error - silently skip
- on start, if errors folder not empty -> try resend packets from errors folder to clickhouse -> on error - panic (manualy delete errors folder)

## Params

```
	port           = flag.Int("p", 8124, "TCP port number to listen on (default: 8124)")
	unixs          = flag.String("unixs", "", "unix socket")
	stdlib         = flag.Bool("stdlib", false, "use stdlib")
	noudp          = flag.Bool("noudp", true, "disable udp interface")
	workers        = flag.Int("workers", -1, "num workers")
	balance        = flag.String("balance", "random", "balance - random, round-robin or least-connections")
	keepalive      = flag.Int("keepalive", 10, "keepalive connection, in seconds")
	fwd            = flag.String("fwd", "http://localhost:8123", "forward to this server (clickhouse)")
	repl           = flag.String("repl", "http://localhost:8124", "replace this string on forward")
	delim          = flag.String("delim", ",", "body delimiter")
	syncsec        = flag.Int("syncsec", 2, "sync interval, in seconds")
	graphitehost   = flag.String("graphitehost", "", "graphite host")
	graphiteport   = flag.Int("graphiteport", 2023, "graphite port")
	graphiteprefix = flag.String("graphiteprefix", "relap.count.proxyhouse", "graphite prefix")
	isdebug        = flag.Bool("isdebug", false, "debug requests")
	resendint      = flag.Int("resendint", 60, "resend error interval, in steps")
```

## Benchmark

```
go version go1.13.5 darwin/amd64

     number of cpus: 8
     number of inserts: 10000
10,000 ops over 8 threads in 140ms, 71,241/sec, 14036 ns/op, 2.1 MB, 216 bytes/op

```

## Contact

Vadim Kulibaba [@recoilme](https://github.com/recoilme)

## License

`proxyhouse` source code is available under the MIT [License](/LICENSE).

